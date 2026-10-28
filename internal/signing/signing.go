// Package signing signs webhook deliveries following the Standard Webhooks
// specification (FR-13, ADR 0002), so receivers can check that a request came
// from Carteiro, was not modified, and is not a replay of an old request.
//
// The signature is an HMAC-SHA256 over "id.timestamp.body", sent with the id
// and the timestamp in three headers:
//
//	webhook-id:        <event id>
//	webhook-timestamp: <unix seconds>
//	webhook-signature: v1,<base64 signature> [v1,<base64 signature> ...]
//
// The signature header holds one signature per secret, separated by spaces.
// While a secret is being rotated, deliveries are signed with both the new and
// the old secret, and a receiver accepts the request if any one matches, so it
// can switch secrets without downtime (FR-06).
package signing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Header names, lowercase as in the specification. http.Header canonicalizes
// them when they are set, and HTTP header names are case-insensitive anyway.
const (
	HeaderID        = "webhook-id"
	HeaderTimestamp = "webhook-timestamp"
	HeaderSignature = "webhook-signature"
)

// version identifies the HMAC-SHA256 scheme. The specification reserves
// "v1a" for ed25519 signatures, which Carteiro does not produce.
const version = "v1"

// DefaultTolerance is how far a timestamp may be from the receiver's clock,
// in either direction, before Verify rejects it. It is the value the official
// libraries use: wide enough for ordinary clock drift, short enough that a
// captured request cannot be replayed for long.
const DefaultTolerance = 5 * time.Minute

var (
	// ErrInvalidID means the id is empty or contains a ".". The signed
	// content is "id.timestamp.body", so a "." in the id would let a different
	// id and timestamp produce the same content, and the same signature.
	ErrInvalidID = errors.New("signing: id must be non-empty and contain no '.'")
	// ErrNoSecrets means Sign was given no secret to sign with.
	ErrNoSecrets = errors.New("signing: no secrets")

	// ErrMissingHeaders means a request lacks one of the three headers.
	ErrMissingHeaders = errors.New("signing: missing webhook headers")
	// ErrInvalidTimestamp means the timestamp header is not a unix time.
	ErrInvalidTimestamp = errors.New("signing: invalid webhook-timestamp")
	// ErrTimestampOutOfRange means the timestamp is further than the
	// tolerance from the current time.
	ErrTimestampOutOfRange = errors.New("signing: webhook-timestamp outside tolerance")
	// ErrNoMatchingSignature means no signature in the header matches.
	ErrNoMatchingSignature = errors.New("signing: no matching signature")
)

// Sign returns the value of the webhook-signature header for a delivery: one
// signature per secret, in the order given. Secrets are the raw bytes, not
// the "whsec_" form shown to users.
func Sign(secrets [][]byte, id string, timestamp time.Time, body []byte) (string, error) {
	if id == "" || strings.Contains(id, ".") {
		return "", ErrInvalidID
	}
	if len(secrets) == 0 {
		return "", ErrNoSecrets
	}
	sigs := make([]string, len(secrets))
	for i, secret := range secrets {
		sigs[i] = version + "," + base64.StdEncoding.EncodeToString(mac(secret, id, timestamp.Unix(), body))
	}
	return strings.Join(sigs, " "), nil
}

// SetHeaders signs a delivery and sets the three Standard Webhooks headers on
// h.
func SetHeaders(h http.Header, secrets [][]byte, id string, timestamp time.Time, body []byte) error {
	sig, err := Sign(secrets, id, timestamp, body)
	if err != nil {
		return err
	}
	h.Set(HeaderID, id)
	h.Set(HeaderTimestamp, strconv.FormatInt(timestamp.Unix(), 10))
	h.Set(HeaderSignature, sig)
	return nil
}

// Verify checks a received delivery the way a receiver must: the timestamp is
// within tolerance of now, and at least one signature in the header was made
// with secret over this exact body.
//
// Carteiro does not receive webhooks; Verify exists so tests can check
// deliveries end to end, and as a short reference for what receivers do.
// Receivers should use an official Standard Webhooks library instead.
func Verify(secret []byte, h http.Header, body []byte, now time.Time, tolerance time.Duration) error {
	id, ts, sigHeader := h.Get(HeaderID), h.Get(HeaderTimestamp), h.Get(HeaderSignature)
	if id == "" || ts == "" || sigHeader == "" {
		return ErrMissingHeaders
	}
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return ErrInvalidTimestamp
	}
	// Checking the timestamp first is safe: it is part of the signed content,
	// so an attacker who changes it to pass this check breaks the signature.
	if d := now.Sub(time.Unix(unix, 0)); d > tolerance || d < -tolerance {
		return ErrTimestampOutOfRange
	}

	want := mac(secret, id, unix, body)
	for _, field := range strings.Fields(sigHeader) {
		v, encoded, ok := strings.Cut(field, ",")
		if !ok || v != version {
			// Signatures of other schemes, such as v1a, are skipped rather
			// than rejected, as the specification asks.
			continue
		}
		got, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			continue
		}
		// hmac.Equal takes the same time wherever the first difference is.
		// A plain comparison would return sooner the earlier it found one,
		// and an attacker timing many requests could learn a valid
		// signature byte by byte.
		if hmac.Equal(got, want) {
			return nil
		}
	}
	return ErrNoMatchingSignature
}

// mac computes HMAC-SHA256(secret, "id.timestamp.body").
func mac(secret []byte, id string, unix int64, body []byte) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(id))
	m.Write([]byte{'.'})
	m.Write(strconv.AppendInt(nil, unix, 10))
	m.Write([]byte{'.'})
	m.Write(body)
	return m.Sum(nil)
}
