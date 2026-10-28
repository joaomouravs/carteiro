// Command receiver is an example webhook receiver for Carteiro.
//
// It shows what every receiver should do, whatever its language:
//
//  1. Verify the signature over the raw request body, before parsing it, with
//     an official Standard Webhooks library. Parsing and re-encoding the JSON
//     first changes bytes and breaks the signature.
//  2. Deduplicate by webhook-id: Carteiro delivers at least once, so the same
//     event can arrive twice.
//  3. Answer quickly with a 2xx, and do slow work afterwards. A slow answer
//     counts as a timeout and gets the webhook sent again.
//
// It lives in its own Go module so its dependency on the official library
// never reaches the Carteiro binary.
//
// Usage:
//
//	RECEIVER_SECRET=whsec_... go run .
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"
)

// maxBody bounds what the receiver reads. Carteiro's payloads are at most
// 256 KiB, plus the envelope.
const maxBody = 1 << 20

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("receiver failed", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	secret := os.Getenv("RECEIVER_SECRET")
	if secret == "" {
		return errors.New("RECEIVER_SECRET is required: the whsec_ secret returned when the endpoint was created")
	}
	addr := os.Getenv("RECEIVER_ADDR")
	if addr == "" {
		addr = ":9000"
	}
	wh, err := standardwebhooks.NewWebhook(secret)
	if err != nil {
		return fmt.Errorf("RECEIVER_SECRET: %w", err)
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           newHandler(wh, logger),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	logger.Info("receiver listening", "addr", addr, "path", "/webhooks")
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// envelope is the body Carteiro sends.
type envelope struct {
	Type      string          `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

type handler struct {
	wh     *standardwebhooks.Webhook
	seen   *recentIDs
	logger *slog.Logger
	// handle is the application's own work. It is a field so tests can see
	// what was handled.
	handle func(id string, e envelope)
}

func newHandler(wh *standardwebhooks.Webhook, logger *slog.Logger) *handler {
	h := &handler{wh: wh, seen: newRecentIDs(10_000), logger: logger}
	h.handle = func(id string, e envelope) {
		logger.Info("webhook received", "webhook_id", id, "type", e.Type, "data", string(e.Data))
	}
	return h
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/webhooks" {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
		return
	}

	// 1. Verify first, on the exact bytes received. Verify also rejects a
	// timestamp more than 5 minutes off, so a captured request cannot be
	// replayed later.
	if err := h.wh.Verify(body, r.Header); err != nil {
		h.logger.Warn("rejected webhook", "err", err)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	// 2. Deduplicate. Answer 2xx to a duplicate: it was handled, and an
	// error would only make Carteiro send it again.
	id := r.Header.Get("webhook-id")
	if !h.seen.add(id) {
		h.logger.Info("duplicate webhook ignored", "webhook_id", id)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	var e envelope
	if err := json.Unmarshal(body, &e); err != nil {
		// Signed by Carteiro but not an envelope: retrying will not fix it.
		http.Error(w, "unexpected body", http.StatusBadRequest)
		return
	}
	// 3. Answer fast. A real receiver would enqueue the work here, or do
	// it in the same database transaction that records the id as seen.
	h.handle(id, e)
	w.WriteHeader(http.StatusNoContent)
}

// recentIDs remembers the last n webhook ids, enough to catch the duplicates
// that retries produce. It is in memory, so a restart forgets it. A real
// receiver stores processed ids in its database with a UNIQUE constraint,
// written in the same transaction as the work, so a webhook takes effect once
// even across restarts and concurrent requests.
type recentIDs struct {
	mu    sync.Mutex
	set   map[string]struct{}
	order []string // ring buffer of the ids in set
	next  int
}

func newRecentIDs(n int) *recentIDs {
	return &recentIDs{set: make(map[string]struct{}, n), order: make([]string, n)}
}

// add records id and reports whether it was new.
func (r *recentIDs) add(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.set[id]; ok {
		return false
	}
	if old := r.order[r.next]; old != "" {
		delete(r.set, old)
	}
	r.order[r.next] = id
	r.next = (r.next + 1) % len(r.order)
	r.set[id] = struct{}{}
	return true
}
