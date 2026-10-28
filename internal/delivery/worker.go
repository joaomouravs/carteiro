package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/joaomouravs/carteiro/internal/crypto"
	"github.com/joaomouravs/carteiro/internal/netguard"
	"github.com/joaomouravs/carteiro/internal/signing"
	"github.com/joaomouravs/carteiro/internal/store"
)

// Delivery statuses, as stored in deliveries.status.
const (
	statusPending   = "pending"
	statusSucceeded = "succeeded"
	statusDead      = "dead"
)

const (
	// leaseMargin is added to the HTTP timeout to get the lease: the time
	// to record the result, plus slack for a slow database or a GC pause.
	// A lease shorter than the request would let another worker reclaim a
	// delivery that is still being sent.
	leaseMargin = 30 * time.Second
	// maxStoredBody is how much of a response body is kept (FR-16).
	maxStoredBody = 4 << 10
	// maxDrainedBody is how much more is read and thrown away, so the
	// connection can be reused. A receiver that sends more loses its
	// keep-alive connection, not Carteiro's time.
	maxDrainedBody = 64 << 10
	// maxStoredError bounds the error text kept for an attempt.
	maxStoredError = 1 << 10
)

const userAgent = "Carteiro/1 (+https://github.com/joaomouravs/carteiro)"

// Options configures a Worker.
type Options struct {
	// Concurrency is how many deliveries are sent at the same time.
	Concurrency int
	// Timeout bounds each HTTP request, from dialing to the end of the
	// response body.
	Timeout time.Duration
	// PollInterval is how long the worker waits before looking again when
	// the queue has no due deliveries.
	PollInterval time.Duration
	Backoff      Backoff
	// NetPolicy decides which addresses deliveries may reach (NFR-SEC-03).
	NetPolicy netguard.Policy
	// An endpoint is disabled once it has failed DisableMinFailures times in
	// a row over at least DisableAfter (FR-08). Both must hold: a busy
	// endpoint can fail ten times in a minute of downtime, and a quiet one
	// can go days between two failures.
	DisableAfter       time.Duration
	DisableMinFailures int
}

// Reasons an endpoint is disabled, as stored in endpoints.disabled_reason.
const (
	// reasonGone: the receiver answered 410 Gone, which means "this URL is
	// not coming back"; there is no point in retrying.
	reasonGone = "gone"
	// reasonFailing: it kept failing for the configured period.
	reasonFailing = "failing"
)

// Worker claims due deliveries and sends them. Any number of workers, in one
// process or many, can run against the same database (NFR-REL-05).
type Worker struct {
	logger *slog.Logger
	pool   *pgxpool.Pool
	q      *store.Queries
	cipher *crypto.Cipher
	client *http.Client
	opts   Options
	lease  time.Duration
}

// New returns a Worker. It does nothing until Run is called.
func New(logger *slog.Logger, pool *pgxpool.Pool, cipher *crypto.Cipher, opts Options) *Worker {
	return &Worker{
		logger: logger,
		pool:   pool,
		q:      store.New(pool),
		cipher: cipher,
		client: netguard.NewClient(opts.NetPolicy, opts.Timeout),
		opts:   opts,
		lease:  opts.Timeout + leaseMargin,
	}
}

// job is a claimed delivery with what sending it needs.
type job struct {
	store.GetDeliveryJobsRow
	// attempt is this attempt's number and the fencing token.
	attempt int32
}

// Run sends deliveries until ctx is cancelled. It then stops claiming, waits
// for the deliveries in flight to finish (each is bounded by Options.Timeout)
// and returns. A delivery that never finishes, because the process is killed,
// is not lost: its lease expires and another worker reclaims it.
func (w *Worker) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	defer wg.Wait()

	// slots holds a token per free concurrency slot; freed is signaled each
	// time a delivery finishes.
	slots := make(chan struct{}, w.opts.Concurrency)
	for range w.opts.Concurrency {
		slots <- struct{}{}
	}
	freed := make(chan struct{}, w.opts.Concurrency)

	// In-flight deliveries outlive ctx, as above, so they run on a context
	// that keeps ctx's values but not its cancellation.
	sendCtx := context.WithoutCancel(ctx)

	for {
		// Claim only as many deliveries as there are free slots. Claiming
		// more would leave the extra ones waiting in memory while their
		// leases run out.
		free := 0
	take:
		for {
			select {
			case <-slots:
				free++
			default:
				break take
			}
		}
		if free == 0 {
			select {
			case <-freed:
				continue
			case <-ctx.Done():
				return nil
			}
		}

		jobs, err := w.claim(ctx, free)
		if err != nil && ctx.Err() == nil {
			w.logger.Error("claim deliveries", "err", err)
		}
		for _, j := range jobs {
			free--
			wg.Go(func() {
				defer func() {
					slots <- struct{}{}
					select {
					case freed <- struct{}{}:
					default:
					}
				}()
				w.deliver(sendCtx, j)
			})
		}
		for range free {
			slots <- struct{}{}
		}

		if ctx.Err() != nil {
			return nil
		}
		// A full batch means more work is probably waiting: look again at
		// once. Otherwise the queue is drained (or the database is
		// failing), so wait before polling it again.
		if len(jobs) > 0 && free == 0 {
			continue
		}
		select {
		case <-time.After(w.opts.PollInterval):
		case <-ctx.Done():
			return nil
		}
	}
}

// claim leases up to limit deliveries, expired leases first, and loads them.
func (w *Worker) claim(ctx context.Context, limit int) ([]job, error) {
	leaseMs := w.lease.Milliseconds()
	tokens := make(map[uuid.UUID]int32, limit)

	// Reclaiming first means a crashed worker's deliveries are not starved
	// by a constant stream of new ones.
	expired, err := w.q.ReclaimExpiredDeliveries(ctx, store.ReclaimExpiredDeliveriesParams{Limit: int64(limit), LeaseMs: leaseMs})
	if err != nil {
		return nil, fmt.Errorf("reclaim: %w", err)
	}
	for _, d := range expired {
		tokens[d.ID] = d.AttemptCount
	}
	if remaining := limit - len(expired); remaining > 0 {
		due, err := w.q.ClaimDueDeliveries(ctx, store.ClaimDueDeliveriesParams{Limit: int64(remaining), LeaseMs: leaseMs})
		if err != nil {
			// The reclaimed deliveries are leased but would not be sent;
			// they come back when the lease expires.
			return nil, fmt.Errorf("claim: %w", err)
		}
		for _, d := range due {
			tokens[d.ID] = d.AttemptCount
		}
	}
	if len(tokens) == 0 {
		return nil, nil
	}

	ids := make([]uuid.UUID, 0, len(tokens))
	for id := range tokens {
		ids = append(ids, id)
	}
	rows, err := w.q.GetDeliveryJobs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("load claimed deliveries: %w", err)
	}
	// A delivery whose endpoint was deleted after the claim is simply
	// missing here; the cascade already removed it.
	jobs := make([]job, len(rows))
	for i, r := range rows {
		jobs[i] = job{GetDeliveryJobsRow: r, attempt: tokens[r.ID]}
	}
	return jobs, nil
}

// result is what one HTTP attempt produced.
type result struct {
	status     int // 0 if no response arrived
	body       string
	retryAfter time.Duration
	err        error
	// internal means the attempt failed on Carteiro's side, before any
	// request was sent. It is not held against the endpoint's health.
	internal bool
}

// disableReason decides whether an attempt's outcome disables the endpoint,
// given its failure streak after the attempt.
func (w *Worker) disableReason(res result, success bool, h store.RecordEndpointResultRow) string {
	switch {
	case success:
		return ""
	case res.status == http.StatusGone:
		return reasonGone
	case int(h.ConsecutiveFailures) >= w.opts.DisableMinFailures && h.FailingLongEnough:
		return reasonFailing
	}
	return ""
}

// deliver makes one attempt and records it.
func (w *Worker) deliver(ctx context.Context, j job) {
	started := time.Now()
	res := w.send(ctx, j)
	duration := time.Since(started)

	success := res.err == nil && res.status >= 200 && res.status <= 299
	status, retryIn := statusSucceeded, (*int64)(nil)
	if !success {
		// The schedule restarts at a replay (FR-18), so it counts attempts
		// from there; the attempt number itself keeps growing.
		if delay, ok := w.opts.Backoff.Next(int(j.attempt-j.ReplayedAtAttempt), res.retryAfter); ok {
			ms := delay.Milliseconds()
			status, retryIn = statusPending, &ms
		} else {
			status = statusDead
		}
	}

	attempt := store.InsertAttemptParams{
		DeliveryID:    j.ID,
		AttemptNumber: j.attempt,
		StartedAt:     started,
		DurationMs:    duration.Milliseconds(),
	}
	if res.status != 0 {
		code, body := int64(res.status), res.body
		attempt.ResponseStatus, attempt.ResponseBody = &code, &body
	}
	if res.err != nil {
		msg := sanitizeText(res.err.Error(), maxStoredError)
		attempt.Error = &msg
	}

	log := w.logger.With("delivery_id", j.ID, "endpoint_id", j.EndpointID, "attempt", j.attempt)
	var (
		fenced   bool
		disabled string // the reason, if this attempt disabled the endpoint
	)
	err := store.InTx(ctx, w.pool, func(q *store.Queries) error {
		if err := q.InsertAttempt(ctx, attempt); err != nil {
			return fmt.Errorf("insert attempt: %w", err)
		}
		// Every attempt to the same endpoint updates its row, so concurrent
		// deliveries to one endpoint take turns on that row lock for the
		// few milliseconds this transaction lasts. It cannot deadlock: these
		// transactions lock their own delivery, then the endpoint. Killing
		// pending deliveries locks more deliveries after the endpoint, but
		// only pending ones, and the only transactions that lock a pending
		// delivery are claims, which read endpoints without locking them.
		if !res.internal {
			h, err := q.RecordEndpointResult(ctx, store.RecordEndpointResultParams{
				ID: j.EndpointID, Success: success, FailingForMs: w.opts.DisableAfter.Milliseconds(),
			})
			if err != nil {
				return fmt.Errorf("record endpoint result: %w", err)
			}
			if reason := w.disableReason(res, success, h); reason != "" && h.Status == "active" {
				if _, err := q.DisableEndpoint(ctx, store.DisableEndpointParams{ID: j.EndpointID, Reason: &reason}); err != nil {
					return fmt.Errorf("disable endpoint: %w", err)
				}
				if _, err := q.KillPendingDeliveries(ctx, j.EndpointID); err != nil {
					return fmt.Errorf("kill pending deliveries: %w", err)
				}
				disabled, h.Status = reason, "disabled"
			}
			// No retry for an endpoint that is disabled, by this attempt or
			// before it (this delivery was already in flight then).
			if h.Status != "active" && status == statusPending {
				status, retryIn = statusDead, nil
			}
		}
		n, err := q.CompleteDelivery(ctx, store.CompleteDeliveryParams{
			ID: j.ID, AttemptCount: j.attempt, Status: status, RetryInMs: retryIn,
		})
		fenced = n == 0
		return err
	})
	switch {
	case err != nil:
		// The lease will expire and the delivery will be sent again: a
		// duplicate, which at-least-once delivery allows (ADR 0004).
		log.Error("record attempt", "err", err)
	case disabled != "":
		log.Warn("endpoint disabled; its pending deliveries are now dead", "reason", disabled,
			"response_status", res.status, "err", res.err)
	case fenced:
		log.Warn("lease lost before the result was recorded; result discarded", "status", status)
	case status == statusSucceeded:
		log.Debug("delivered", "response_status", res.status, "duration_ms", duration.Milliseconds())
	default:
		log.Info("delivery attempt failed", "next_status", status,
			"response_status", res.status, "err", res.err, "duration_ms", duration.Milliseconds())
	}
}

// envelope is the body of every webhook: the shape the Standard Webhooks
// specification recommends, with the producer's payload under "data".
type envelope struct {
	Type      string          `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

// errUnreadableSecret means an endpoint secret did not decrypt, which points
// at a changed CARTEIRO_ENCRYPTION_KEY rather than at the receiver.
var errUnreadableSecret = errors.New("internal error: endpoint secret could not be decrypted")

// send makes the HTTP request for j.
func (w *Worker) send(ctx context.Context, j job) result {
	secrets := make([][]byte, len(j.SecretCiphertexts))
	for i, ct := range j.SecretCiphertexts {
		s, err := w.cipher.Decrypt(ct, j.EndpointID[:])
		if err != nil {
			w.logger.Error("decrypt endpoint secret", "endpoint_id", j.EndpointID, "err", err)
			return result{err: errUnreadableSecret, internal: true}
		}
		secrets[i] = s
	}

	body, err := json.Marshal(envelope{Type: j.EventType, Timestamp: j.EventCreatedAt.UTC(), Data: j.Payload})
	if err != nil {
		return result{err: fmt.Errorf("encode body: %w", err), internal: true}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, j.URL, bytes.NewReader(body))
	if err != nil {
		return result{err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	// The signature timestamp is the time of this attempt, not of the event:
	// receivers reject old timestamps, and a retry hours later must pass.
	// webhook-id is the event id, the same on every attempt, so receivers
	// can deduplicate (ADR 0004).
	if err := signing.SetHeaders(req.Header, secrets, j.EventID.String(), time.Now(), body); err != nil {
		return result{err: err, internal: true}
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return result{err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	res := result{status: resp.StatusCode}
	stored, err := io.ReadAll(io.LimitReader(resp.Body, maxStoredBody))
	res.body = sanitizeText(string(stored), maxStoredBody)
	if err == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainedBody))
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
		res.retryAfter = parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
	}
	return res
}

// sanitizeText makes s storable in a PostgreSQL text column, which rejects
// both invalid UTF-8 and NUL bytes: either would make the attempt fail to
// record, letting a receiver break its own attempt log with one byte. It also
// cuts s to at most maxBytes, on a character boundary.
func sanitizeText(s string, maxBytes int) string {
	// utf8.RuneError is U+FFFD, the character that marks replaced input.
	s = strings.ToValidUTF8(s, string(utf8.RuneError))
	s = strings.ReplaceAll(s, "\x00", string(utf8.RuneError))
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
