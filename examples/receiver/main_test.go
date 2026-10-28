package main

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"

	"github.com/joaomouravs/carteiro/internal/crypto"
	"github.com/joaomouravs/carteiro/internal/signing"
)

// These tests sign with Carteiro's own code and verify with the official
// library: they are the proof that the two agree (ADR 0002). Carteiro's
// signing tests check a fixed vector; these check the real code paths, the
// "whsec_" format and the rotation header included.

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

type receiverTest struct {
	t       *testing.T
	h       *handler
	handled []string
}

// newReceiver configures the receiver with secret, formatted exactly as
// Carteiro returns it when an endpoint is created.
func newReceiver(t *testing.T, secret []byte) *receiverTest {
	t.Helper()
	wh, err := standardwebhooks.NewWebhook(crypto.FormatEndpointSecret(secret))
	if err != nil {
		t.Fatal(err)
	}
	rt := &receiverTest{t: t, h: newHandler(wh, discard)}
	rt.h.handle = func(id string, _ envelope) { rt.handled = append(rt.handled, id) }
	return rt
}

// send posts body signed as Carteiro signs it, at time ts, and returns the
// status code.
func (rt *receiverTest) send(secrets [][]byte, id string, ts time.Time, body string, tamper func(*http.Request)) int {
	rt.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/webhooks", bytes.NewReader([]byte(body)))
	if err := signing.SetHeaders(req.Header, secrets, id, ts, []byte(body)); err != nil {
		rt.t.Fatal(err)
	}
	if tamper != nil {
		tamper(req)
	}
	rec := httptest.NewRecorder()
	rt.h.ServeHTTP(rec, req)
	return rec.Code
}

const body = `{"type":"invoice.paid","timestamp":"2026-10-28T12:00:00Z","data":{"invoice":"in_1"}}`

func TestAcceptsWhatCarteiroSigns(t *testing.T) {
	secret := crypto.NewEndpointSecret()
	rt := newReceiver(t, secret)
	if code := rt.send([][]byte{secret}, "evt_1", time.Now(), body, nil); code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", code)
	}
	if len(rt.handled) != 1 || rt.handled[0] != "evt_1" {
		t.Errorf("handled = %v, want [evt_1]", rt.handled)
	}
}

// TestAcceptsEitherSecretDuringRotation is FR-06 seen from the receiver:
// Carteiro signs with the new and the old secret, and a receiver that still
// has the old one, or already has the new one, accepts the request.
func TestAcceptsEitherSecretDuringRotation(t *testing.T) {
	newSecret, oldSecret := crypto.NewEndpointSecret(), crypto.NewEndpointSecret()
	for name, has := range map[string][]byte{"new": newSecret, "old": oldSecret} {
		rt := newReceiver(t, has)
		if code := rt.send([][]byte{newSecret, oldSecret}, "evt_1", time.Now(), body, nil); code != http.StatusNoContent {
			t.Errorf("receiver with the %s secret: status = %d, want 204", name, code)
		}
	}
}

func TestRejects(t *testing.T) {
	secret := crypto.NewEndpointSecret()
	tests := []struct {
		name    string
		secrets [][]byte
		ts      time.Time
		tamper  func(*http.Request)
	}{
		{"another endpoint's secret", [][]byte{crypto.NewEndpointSecret()}, time.Now(), nil},
		{"tampered body", [][]byte{secret}, time.Now(), func(r *http.Request) {
			r.Body = io.NopCloser(bytes.NewReader([]byte(`{"type":"invoice.paid","data":{"invoice":"in_2"}}`)))
		}},
		{"replayed after 10 minutes", [][]byte{secret}, time.Now().Add(-10 * time.Minute), nil},
		{"missing signature", [][]byte{secret}, time.Now(), func(r *http.Request) { r.Header.Del("webhook-signature") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := newReceiver(t, secret)
			if code := rt.send(tt.secrets, "evt_1", tt.ts, body, tt.tamper); code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", code)
			}
			if len(rt.handled) != 0 {
				t.Errorf("a rejected webhook was handled")
			}
		})
	}
}

// TestDuplicatesAreHandledOnce plays a retry: the receiver processed the
// webhook but Carteiro never saw the answer, so it sends it again.
func TestDuplicatesAreHandledOnce(t *testing.T) {
	secret := crypto.NewEndpointSecret()
	rt := newReceiver(t, secret)
	for range 3 {
		// A retry has a new timestamp and signature, but the same id.
		if code := rt.send([][]byte{secret}, "evt_1", time.Now(), body, nil); code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 for a duplicate too", code)
		}
	}
	rt.send([][]byte{secret}, "evt_2", time.Now(), body, nil)
	if len(rt.handled) != 2 {
		t.Errorf("handled = %v, want evt_1 and evt_2 once each", rt.handled)
	}
}

func TestRecentIDsForgetsTheOldest(t *testing.T) {
	r := newRecentIDs(2)
	for _, id := range []string{"a", "b", "c"} {
		r.add(id)
	}
	if !r.add("a") {
		t.Error(`"a" is still remembered after 2 newer ids filled the buffer`)
	}
	if r.add("c") {
		t.Error(`"c", the newest id, was forgotten`)
	}
}

func TestOnlyPostToWebhooks(t *testing.T) {
	rt := newReceiver(t, crypto.NewEndpointSecret())
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/webhooks", nil),
		httptest.NewRequest(http.MethodPost, "/other", nil),
	} {
		rec := httptest.NewRecorder()
		rt.h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: status = %d, want 404", req.Method, req.URL.Path, rec.Code)
		}
	}
}
