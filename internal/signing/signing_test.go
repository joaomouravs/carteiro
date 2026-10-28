package signing

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The test vector from the official Standard Webhooks Go library
// (libraries/go/webhook_test.go, TestWebhookSign). Matching it byte for byte
// is what makes Carteiro compatible with every official library.
const (
	vectorSecret    = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
	vectorID        = "msg_p5jXN8AQM9LWM0D4loKWxJek"
	vectorUnix      = 1614265330
	vectorBody      = `{"test": 2432232314}`
	vectorSignature = "v1,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE="
)

var vectorTime = time.Unix(vectorUnix, 0)

// decodeSecret turns the "whsec_" form shown to users back into raw bytes.
func decodeSecret(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(s, "whsec_"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSignSpecVector(t *testing.T) {
	secret := decodeSecret(t, vectorSecret)
	got, err := Sign([][]byte{secret}, vectorID, vectorTime, []byte(vectorBody))
	if err != nil {
		t.Fatal(err)
	}
	if got != vectorSignature {
		t.Errorf("Sign = %s, want %s", got, vectorSignature)
	}
}

func TestSignOneSignaturePerSecret(t *testing.T) {
	newSecret, oldSecret := []byte("new secret, 24 bytes long"), []byte("old secret, 24 bytes long")
	body := []byte(vectorBody)

	both, err := Sign([][]byte{newSecret, oldSecret}, vectorID, vectorTime, body)
	if err != nil {
		t.Fatal(err)
	}
	onlyNew, _ := Sign([][]byte{newSecret}, vectorID, vectorTime, body)
	onlyOld, _ := Sign([][]byte{oldSecret}, vectorID, vectorTime, body)
	if want := onlyNew + " " + onlyOld; both != want {
		t.Errorf("Sign with two secrets = %q, want %q", both, want)
	}
}

func TestSignRejects(t *testing.T) {
	secrets := [][]byte{[]byte("secret")}
	tests := []struct {
		name    string
		secrets [][]byte
		id      string
		want    error
	}{
		{"empty id", secrets, "", ErrInvalidID},
		{"id with a dot", secrets, "msg.1", ErrInvalidID},
		{"no secrets", nil, vectorID, ErrNoSecrets},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Sign(tt.secrets, tt.id, vectorTime, nil); !errors.Is(err, tt.want) {
				t.Errorf("Sign error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestSetHeaders(t *testing.T) {
	h := http.Header{}
	err := SetHeaders(h, [][]byte{decodeSecret(t, vectorSecret)}, vectorID, vectorTime, []byte(vectorBody))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		HeaderID:        vectorID,
		HeaderTimestamp: "1614265330",
		HeaderSignature: vectorSignature,
	}
	for name, value := range want {
		if got := h.Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
}

func TestVerify(t *testing.T) {
	secret := decodeSecret(t, vectorSecret)
	oldSecret := []byte("old secret, 24 bytes long")
	body := []byte(vectorBody)

	// headers returns the vector's headers with some values replaced.
	headers := func(replace map[string]string) http.Header {
		h := http.Header{}
		h.Set(HeaderID, vectorID)
		h.Set(HeaderTimestamp, "1614265330")
		h.Set(HeaderSignature, vectorSignature)
		for name, value := range replace {
			if value == "" {
				h.Del(name)
			} else {
				h.Set(name, value)
			}
		}
		return h
	}
	rotation, err := Sign([][]byte{secret, oldSecret}, vectorID, vectorTime, body)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		secret []byte
		header http.Header
		body   string
		now    time.Time
		want   error
	}{
		{"valid", secret, headers(nil), vectorBody, vectorTime, nil},
		{"valid at the edge of the tolerance", secret, headers(nil), vectorBody, vectorTime.Add(DefaultTolerance), nil},
		{"valid with the receiver's clock behind", secret, headers(nil), vectorBody, vectorTime.Add(-DefaultTolerance), nil},

		// Rotation: the header carries both signatures, and a receiver with
		// either secret accepts the request.
		{"rotation, receiver has the new secret", secret, headers(map[string]string{HeaderSignature: rotation}), vectorBody, vectorTime, nil},
		{"rotation, receiver has the old secret", oldSecret, headers(map[string]string{HeaderSignature: rotation}), vectorBody, vectorTime, nil},

		// Fields that are not v1 signatures are skipped, not fatal.
		{"other schemes and junk are skipped", secret, headers(map[string]string{HeaderSignature: "v1a,abc ,,, v1,not-base64 " + vectorSignature}), vectorBody, vectorTime, nil},
		{"same signature under another version", secret, headers(map[string]string{HeaderSignature: strings.Replace(vectorSignature, "v1,", "v2,", 1)}), vectorBody, vectorTime, ErrNoMatchingSignature},

		// Anything signed that changes must break the signature.
		{"tampered body", secret, headers(nil), `{"test": 2432232315}`, vectorTime, ErrNoMatchingSignature},
		{"tampered id", secret, headers(map[string]string{HeaderID: "msg_other"}), vectorBody, vectorTime, ErrNoMatchingSignature},
		{"tampered timestamp", secret, headers(map[string]string{HeaderTimestamp: "1614265331"}), vectorBody, vectorTime, ErrNoMatchingSignature},
		{"wrong secret", oldSecret, headers(nil), vectorBody, vectorTime, ErrNoMatchingSignature},

		// Replay protection.
		{"too old", secret, headers(nil), vectorBody, vectorTime.Add(DefaultTolerance + time.Second), ErrTimestampOutOfRange},
		{"too far in the future", secret, headers(nil), vectorBody, vectorTime.Add(-DefaultTolerance - time.Second), ErrTimestampOutOfRange},

		// Malformed requests.
		{"missing id", secret, headers(map[string]string{HeaderID: ""}), vectorBody, vectorTime, ErrMissingHeaders},
		{"missing timestamp", secret, headers(map[string]string{HeaderTimestamp: ""}), vectorBody, vectorTime, ErrMissingHeaders},
		{"missing signature", secret, headers(map[string]string{HeaderSignature: ""}), vectorBody, vectorTime, ErrMissingHeaders},
		{"timestamp not a number", secret, headers(map[string]string{HeaderTimestamp: "2021-02-25T15:02:10Z"}), vectorBody, vectorTime, ErrInvalidTimestamp},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Verify(tt.secret, tt.header, []byte(tt.body), tt.now, DefaultTolerance)
			if !errors.Is(err, tt.want) {
				t.Errorf("Verify error = %v, want %v", err, tt.want)
			}
		})
	}
}
