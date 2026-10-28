package api

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxURLLen         = 2048
	maxDescriptionLen = 1000
	maxEventTypes     = 100
	maxEventTypeLen   = 200
)

// eventTypePattern accepts names such as "invoice.paid" or "user_created.v2".
// A restricted alphabet keeps event types safe to show in logs, URLs and UIs.
var eventTypePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// hostLookupTimeout bounds the DNS lookup in validateEndpointURL, so a slow
// resolver cannot hold an API request for long.
const hostLookupTimeout = 5 * time.Second

// validateEndpointURL checks a receiver URL (FR-07) and returns it normalized.
//
// It also rejects hosts that resolve to a blocked address. That check gives
// the producer an early, clear error, but it is not what stops SSRF: a
// hostname can resolve to a safe address now and to an internal one at
// delivery time. The delivery client checks every connection again
// (netguard.NewClient).
func (s *Server) validateEndpointURL(ctx context.Context, raw string) (string, []fieldError) {
	invalid := func(msg string) (string, []fieldError) { return "", []fieldError{{"url", msg}} }

	raw = strings.TrimSpace(raw)
	if raw == "" {
		return invalid("is required")
	}
	if len(raw) > maxURLLen {
		return invalid(fmt.Sprintf("must have at most %d characters", maxURLLen))
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Hostname() == "" {
		return invalid("must be an absolute URL, such as https://example.com/webhooks")
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && s.allowHTTPEndpoints:
	default:
		return invalid("must use https")
	}
	if u.User != nil {
		// Credentials in the URL would be stored, displayed and logged in
		// clear. Receivers authenticate requests by their signature.
		return invalid("must not contain credentials")
	}
	if u.Fragment != "" {
		return invalid("must not contain a fragment")
	}

	// The cheap checks come first, so a malformed URL never costs a lookup.
	ctx, cancel := context.WithTimeout(ctx, hostLookupTimeout)
	defer cancel()
	addrs, err := s.resolver.LookupNetIP(ctx, "ip", u.Hostname())
	if err != nil {
		// A host that does not resolve yet is accepted. Receivers are often
		// configured before their DNS exists: the endpoint's secret is
		// needed to deploy the receiver, and only creating the endpoint
		// returns it. Nothing is lost, since the dialer checks the address
		// it actually connects to, and a typo shows up as failed attempts.
		return u.String(), nil
	}
	// Every address must be allowed, not just one: a hostname can resolve to
	// a public and an internal address at once, and the client may dial
	// either. The message does not say which address was refused, so the API
	// cannot be used to map internal DNS names.
	for _, a := range addrs {
		if !s.netPolicy.Allows(a) {
			return invalid("must not point to a loopback, private or other internal network address")
		}
	}
	return u.String(), nil
}

func validateDescription(desc string) (string, []fieldError) {
	desc = strings.TrimSpace(desc)
	if utf8.RuneCountInString(desc) > maxDescriptionLen {
		return "", []fieldError{{"description", fmt.Sprintf("must have at most %d characters", maxDescriptionLen)}}
	}
	return desc, nil
}

func validEventType(t string) bool {
	return len(t) <= maxEventTypeLen && eventTypePattern.MatchString(t)
}

// validateEventTypes checks an endpoint's subscription list and removes
// duplicates, keeping the order given. It never returns nil, because an empty
// list is meaningful: the endpoint receives every event type.
func validateEventTypes(types []string) ([]string, []fieldError) {
	if len(types) > maxEventTypes {
		return nil, []fieldError{{"event_types", fmt.Sprintf("must have at most %d entries", maxEventTypes)}}
	}
	out := make([]string, 0, len(types))
	seen := make(map[string]bool, len(types))
	var errs []fieldError
	for i, t := range types {
		t = strings.TrimSpace(t)
		if !validEventType(t) {
			errs = append(errs, fieldError{
				fmt.Sprintf("event_types[%d]", i),
				"must be 1 to 200 letters, digits, '.', '_' or '-', starting with a letter or digit",
			})
			continue
		}
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out, errs
}
