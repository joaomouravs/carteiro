package api

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/joaomouravs/carteiro/internal/crypto"
)

type secretRow struct {
	plain     string // in the whsec_ form, decrypted with the test cipher
	expiresIn *time.Duration
}

// validSecrets returns the secrets deliveries to the endpoint are signed
// with now, newest first.
func validSecrets(t *testing.T, endpointID uuid.UUID) []secretRow {
	t.Helper()
	rows, err := testPool.Query(context.Background(), `
		SELECT secret_ciphertext, extract(epoch FROM expires_at - now()) * 1000
		FROM endpoint_secrets
		WHERE endpoint_id = $1 AND (expires_at IS NULL OR expires_at > now())
		ORDER BY created_at DESC`, endpointID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cipher := testCipher(t)
	var out []secretRow
	for rows.Next() {
		var ct []byte
		var ms *float64
		if err := rows.Scan(&ct, &ms); err != nil {
			t.Fatal(err)
		}
		plain, err := cipher.Decrypt(ct, endpointID[:])
		if err != nil {
			t.Fatalf("stored secret does not decrypt: %v", err)
		}
		row := secretRow{plain: crypto.FormatEndpointSecret(plain)}
		if ms != nil {
			d := time.Duration(*ms) * time.Millisecond
			row.expiresIn = &d
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func near(d *time.Duration, want time.Duration) bool {
	return d != nil && *d > want-time.Minute && *d <= want
}

func TestRotateSecret(t *testing.T) {
	c := newClient(t)
	key := c.producer()
	ep := c.createEndpoint(key, map[string]any{"url": "https://example.com/hook"})
	path := "/v1/endpoints/" + ep.ID.String() + "/rotate-secret"

	rec := c.do("POST", path, key, nil)
	wantStatus(t, rec, http.StatusOK)
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store: the response carries a secret", cc)
	}
	rotated := decode[endpointResponse](t, rec)
	if rotated.Secret == "" || rotated.Secret == ep.Secret {
		t.Fatalf("rotated secret = %q, want a new one", rotated.Secret)
	}

	got := validSecrets(t, ep.ID)
	if len(got) != 2 {
		t.Fatalf("%d valid secrets, want the new and the old one", len(got))
	}
	if got[0].plain != rotated.Secret || got[0].expiresIn != nil {
		t.Errorf("current secret = %+v, want the one returned, with no expiry", got[0])
	}
	if got[1].plain != ep.Secret || !near(got[1].expiresIn, 24*time.Hour) {
		t.Errorf("old secret = %+v, want it valid for the 24h grace period", got[1])
	}
	if s := decode[endpointResponse](t, c.do("GET", "/v1/endpoints/"+ep.ID.String(), key, nil)); s.Secret != "" {
		t.Error("GET returned the secret; only creation and rotation may")
	}
}

func TestRotateSecretGracePeriod(t *testing.T) {
	c := newClient(t)
	key := c.producer()

	t.Run("zero revokes the old secret at once", func(t *testing.T) {
		ep := c.createEndpoint(key, map[string]any{"url": "https://example.com/hook"})
		rotated := decode[endpointResponse](t, c.do("POST", "/v1/endpoints/"+ep.ID.String()+"/rotate-secret", key,
			map[string]any{"grace_period_seconds": 0}))
		if got := validSecrets(t, ep.ID); len(got) != 1 || got[0].plain != rotated.Secret {
			t.Errorf("valid secrets = %+v, want only the new one", got)
		}
	})

	t.Run("a second rotation shortens earlier grace periods", func(t *testing.T) {
		ep := c.createEndpoint(key, map[string]any{"url": "https://example.com/hook"})
		path := "/v1/endpoints/" + ep.ID.String() + "/rotate-secret"
		wantStatus(t, c.do("POST", path, key, nil), http.StatusOK) // 24h
		wantStatus(t, c.do("POST", path, key, map[string]any{"grace_period_seconds": 3600}), http.StatusOK)
		got := validSecrets(t, ep.ID)
		if len(got) != 3 {
			t.Fatalf("%d valid secrets, want 3", len(got))
		}
		for _, s := range got[1:] {
			if !near(s.expiresIn, time.Hour) {
				t.Errorf("old secret expires in %v, want 1h: no old secret outlives the latest grace period", s.expiresIn)
			}
		}
	})

	for name, body := range map[string]any{
		"negative":        map[string]any{"grace_period_seconds": -1},
		"over seven days": map[string]any{"grace_period_seconds": 7*24*3600 + 1},
	} {
		t.Run(name, func(t *testing.T) {
			ep := c.createEndpoint(key, map[string]any{"url": "https://example.com/hook"})
			p := wantProblem(t, c.do("POST", "/v1/endpoints/"+ep.ID.String()+"/rotate-secret", key, body), http.StatusUnprocessableEntity)
			if len(p.Errors) != 1 || p.Errors[0].Field != "grace_period_seconds" {
				t.Errorf("errors = %+v", p.Errors)
			}
			if got := validSecrets(t, ep.ID); len(got) != 1 {
				t.Errorf("a rejected rotation changed the secrets: %+v", got)
			}
		})
	}
}

// TestConcurrentRotations rotates one endpoint from many requests at once.
// Without the endpoint row lock, two of them retire the same current secret
// and both insert a new one, and the unique index fails the second (500).
func TestConcurrentRotations(t *testing.T) {
	c := newClient(t)
	key := c.producer()
	ep := c.createEndpoint(key, map[string]any{"url": "https://example.com/hook"})
	path := "/v1/endpoints/" + ep.ID.String() + "/rotate-secret"

	const n = 10
	codes := make(chan int, n)
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() { codes <- c.do("POST", path, key, nil).Code })
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != http.StatusOK {
			t.Errorf("a concurrent rotation answered %d, want 200", code)
		}
	}

	var current int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM endpoint_secrets WHERE endpoint_id = $1 AND expires_at IS NULL`, ep.ID).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if current != 1 {
		t.Errorf("%d current secrets, want exactly 1", current)
	}
	if got := validSecrets(t, ep.ID); len(got) != n+1 {
		t.Errorf("%d valid secrets, want %d: the original and one per rotation", len(got), n+1)
	}
}

func TestRotateSecretIsScopedToItsApplication(t *testing.T) {
	c := newClient(t)
	ep := c.createEndpoint(c.producer(), map[string]any{"url": "https://example.com/hook"})
	other := c.createKey(c.createApp("other").ID).Key
	wantProblem(t, c.do("POST", "/v1/endpoints/"+ep.ID.String()+"/rotate-secret", other, nil), http.StatusNotFound)
	if got := validSecrets(t, ep.ID); len(got) != 1 {
		t.Errorf("another application rotated the secret: %+v", got)
	}
}
