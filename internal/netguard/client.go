package netguard

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// ErrBlocked means a connection was refused because its address is blocked.
// Clients from NewClient return it wrapped, so callers check it with
// errors.Is.
var ErrBlocked = errors.New("netguard: address blocked")

// NewClient returns the HTTP client that delivers webhooks (NFR-SEC-03,
// NFR-SEC-04). timeout bounds a whole request, from dialing to reading the
// end of the response body.
//
// The check runs in the dialer, on the IP address each connection is about
// to use. Checking the hostname when an endpoint is created is not enough:
// DNS can answer with a public address then and an internal one at delivery
// time (DNS rebinding). Checking the resolved address in the dialer leaves no
// gap between the check and the connection.
func NewClient(p Policy, timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		// Control runs for every connection attempt, after DNS and before
		// connect. A hostname with several addresses gets one call each.
		Control: p.control,
	}
	transport := &http.Transport{
		// No proxy, whatever HTTP_PROXY says. Through a proxy, the dialer
		// connects to the proxy, so the check would inspect the proxy's
		// address and the proxy would then fetch any URL, internal or not.
		Proxy:       nil,
		DialContext: dialer.DialContext,
		// A custom DialContext turns HTTP/2 off unless this is set.
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		// Redirects are not followed (NFR-SEC-04). A public receiver could
		// otherwise answer "302 Location: http://169.254.169.254/" and have
		// Carteiro make the internal request for it. The 3xx response is
		// returned as is, and the delivery counts it as a failure.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// control is a net.Dialer Control hook. address is the resolved "ip:port"
// the socket is about to connect to.
func (p Policy) control(network, address string, _ syscall.RawConn) error {
	if network != "tcp4" && network != "tcp6" {
		return fmt.Errorf("%w: network %s", ErrBlocked, network)
	}
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		// Fail closed: an address we cannot read is not one we can vouch for.
		return fmt.Errorf("%w: unparsable address %q", ErrBlocked, address)
	}
	if !p.Allows(ap.Addr()) {
		return fmt.Errorf("%w: %s", ErrBlocked, ap.Addr())
	}
	return nil
}
