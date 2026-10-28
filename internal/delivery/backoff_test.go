package delivery

import (
	"math/rand/v2"
	"testing"
	"time"
)

func TestCeilingDoublesUntilTheCap(t *testing.T) {
	b := DefaultBackoff
	want := []time.Duration{
		30 * time.Second, // after attempt 1
		time.Minute,
		2 * time.Minute,
		4 * time.Minute,
		8 * time.Minute,
		16 * time.Minute,
		32 * time.Minute,
		64 * time.Minute,
		128 * time.Minute,
		256 * time.Minute,
		6 * time.Hour, // 512 min would pass the cap
	}
	for i, w := range want {
		if got := b.ceiling(i + 1); got != w {
			t.Errorf("ceiling(%d) = %s, want %s", i+1, got, w)
		}
	}
}

func TestCeilingDoesNotOverflow(t *testing.T) {
	// base × 2^(n-1) passes the int64 range of time.Duration (about 292
	// years) at n = 30 for a 30s base. A naive multiplication would wrap to
	// a negative or tiny delay and retry immediately.
	b := DefaultBackoff
	for _, n := range []int{29, 30, 31, 63, 64, 65, 1000} {
		if got := b.ceiling(n); got != b.Cap {
			t.Errorf("ceiling(%d) = %s, want the cap %s", n, got, b.Cap)
		}
	}
}

func TestDelayIsJitteredWithinTheUpperHalf(t *testing.T) {
	b := DefaultBackoff
	r := rand.New(rand.NewPCG(1, 2))
	for n := 1; n <= 12; n++ {
		c := b.ceiling(n)
		lo, hi := c, time.Duration(0)
		for range 200 {
			d := b.delay(n, r.Int64N)
			if d < c/2 || d > c {
				t.Fatalf("delay(%d) = %s, outside [%s, %s]", n, d, c/2, c)
			}
			lo, hi = min(lo, d), max(hi, d)
		}
		// The point of jitter is spreading retries out. Over 200 draws the
		// values must cover most of the range, not cluster on one value.
		if hi-lo < c/4 {
			t.Errorf("delay(%d) spread %s..%s, want most of [%s, %s]", n, lo, hi, c/2, c)
		}
	}
}

func TestNext(t *testing.T) {
	b := Backoff{Base: 30 * time.Second, Cap: 10 * time.Minute, MaxAttempts: 3}
	// alwaysMax makes the jitter pick the top of the range, so the expected
	// values are exact.
	alwaysMax := func(n int64) int64 { return n - 1 }

	tests := []struct {
		name       string
		attempt    int
		retryAfter time.Duration
		wantDelay  time.Duration
		wantRetry  bool
	}{
		{"first failure", 1, 0, 30 * time.Second, true},
		{"second failure", 2, 0, time.Minute, true},
		{"attempts exhausted", 3, 0, 0, false},
		{"past the maximum", 4, 0, 0, false},

		// Retry-After is a lower bound: the receiver asked not to come back
		// sooner, so the larger of it and the backoff wins.
		{"Retry-After longer than the backoff", 1, 2 * time.Minute, 2 * time.Minute, true},
		{"Retry-After shorter than the backoff", 2, 5 * time.Second, time.Minute, true},
		{"Retry-After capped", 1, 24 * time.Hour, 10 * time.Minute, true},
		{"Retry-After does not revive a dead delivery", 3, time.Minute, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, retry := b.next(tt.attempt, tt.retryAfter, alwaysMax)
			if d != tt.wantDelay || retry != tt.wantRetry {
				t.Errorf("next = (%s, %v), want (%s, %v)", d, retry, tt.wantDelay, tt.wantRetry)
			}
		})
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		header string
		want   time.Duration
	}{
		{"120", 2 * time.Minute},
		{"0", 0},
		{"Wed, 28 Oct 2026 12:05:00 GMT", 5 * time.Minute}, // HTTP-date form
		{"Wed, 28 Oct 2026 11:00:00 GMT", 0},               // in the past
		{"", 0},
		{"-5", 0},
		{"soon", 0},
		{"1.5", 0}, // the header takes whole seconds only
	}
	for _, tt := range tests {
		t.Run(tt.header, func(t *testing.T) {
			if got := parseRetryAfter(tt.header, now); got != tt.want {
				t.Errorf("parseRetryAfter(%q) = %s, want %s", tt.header, got, tt.want)
			}
		})
	}
}

// TestDefaultRetryWindow pins the numbers docs/architecture.md quotes: with
// the defaults, a delivery is retried for between half of 14h31m30s and all of
// it before it is marked dead.
func TestDefaultRetryWindow(t *testing.T) {
	b := DefaultBackoff
	var total time.Duration
	for n := 1; n < b.MaxAttempts; n++ {
		total += b.ceiling(n)
	}
	if want := 14*time.Hour + 31*time.Minute + 30*time.Second; total != want {
		t.Errorf("total of the ceilings = %s, want %s", total, want)
	}
}
