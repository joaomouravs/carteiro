package api

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
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

// validateEndpointURL checks a receiver URL (FR-07) and returns it normalized.
//
// FR-07 also rejects hosts that resolve to blocked IP ranges. That check
// arrives with internal/netguard in M2, where it also guards every
// connection, which is the check that actually stops SSRF: a hostname can
// resolve to a safe address now and to an internal one at delivery time.
func (s *Server) validateEndpointURL(raw string) (string, []fieldError) {
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
