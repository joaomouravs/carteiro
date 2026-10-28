// Package delivery sends webhooks: it claims due deliveries from the queue,
// posts them to their endpoints, and records every attempt.
package delivery

import (
	"math"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"
)

// Backoff decides when a failed delivery is retried (FR-14) and when it is
// given up on (FR-15). See docs/architecture.md, "Retry schedule".
type Backoff struct {
	// Base is the ceiling of the first retry delay. Each later failure
	// doubles the ceiling, up to Cap.
	Base time.Duration
	// Cap is the longest Carteiro waits between two attempts.
	Cap time.Duration
	// MaxAttempts is how many attempts a delivery gets before it is dead.
	MaxAttempts int
}

// DefaultBackoff retries for between about 7 and 14.5 hours (jitter decides
// where), long enough to ride out a receiver's overnight outage.
var DefaultBackoff = Backoff{Base: 30 * time.Second, Cap: 6 * time.Hour, MaxAttempts: 12}

// Next returns how long to wait after attempt number attempt failed, or false
// if that was the last attempt. retryAfter is what the receiver asked for in a
// Retry-After header, or zero.
func (b Backoff) Next(attempt int, retryAfter time.Duration) (time.Duration, bool) {
	// The top-level functions of math/rand/v2 are safe for concurrent use
	// and seeded randomly, which is all jitter needs; it is not a secret.
	return b.next(attempt, retryAfter, rand.Int64N)
}

// next is Next with the random source passed in, so tests are deterministic.
func (b Backoff) next(attempt int, retryAfter time.Duration, int64n func(int64) int64) (time.Duration, bool) {
	if attempt >= b.MaxAttempts {
		return 0, false
	}
	// Retry-After is a lower bound: the receiver asked Carteiro not to come
	// back sooner, but nothing says the backoff should shrink because of it.
	// It is capped, so a receiver cannot park a delivery for days (FR-14).
	return min(max(b.delay(attempt, int64n), retryAfter), b.Cap), true
}

// delay applies equal jitter to the ceiling: a random duration in
// [ceiling/2, ceiling]. Jitter spreads out the retries of deliveries that
// failed together, so a receiver coming back from an outage is not hit by all
// of them at the same instant. I keep the lower half fixed so there is always
// a minimum wait; "full jitter", in [0, ceiling], can retry almost at once.
func (b Backoff) delay(attempt int, int64n func(int64) int64) time.Duration {
	c := b.ceiling(attempt)
	half := c / 2
	return half + time.Duration(int64n(int64(c-half)+1))
}

// ceiling is min(Cap, Base × 2^(attempt-1)), computed by doubling so it never
// overflows: the loop stops as soon as one more doubling would pass the cap.
func (b Backoff) ceiling(attempt int) time.Duration {
	d := b.Base
	for i := 1; i < attempt; i++ {
		if d > b.Cap/2 {
			return b.Cap
		}
		d *= 2
	}
	return min(d, b.Cap)
}

// maxRetryAfterSeconds keeps a huge header value from overflowing a
// time.Duration. Next caps the result far below this anyway.
const maxRetryAfterSeconds = math.MaxInt64 / int64(time.Second)

// parseRetryAfter reads a Retry-After header (RFC 9110, section 10.2.3),
// which is either whole seconds or an HTTP date. Anything else, or a date in
// the past, means no preference and returns zero.
func parseRetryAfter(header string, now time.Time) time.Duration {
	if header == "" {
		return 0
	}
	// ParseUint, unlike Atoi, rejects signs, so "-5" and "+5" are invalid
	// as the RFC's grammar (1*DIGIT) says.
	if secs, err := strconv.ParseUint(header, 10, 64); err == nil {
		return time.Duration(min(secs, uint64(maxRetryAfterSeconds))) * time.Second
	}
	if t, err := http.ParseTime(header); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}
