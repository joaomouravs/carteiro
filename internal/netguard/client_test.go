package netguard

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPolicyAllows(t *testing.T) {
	tests := []struct {
		addr                    string
		byDefault, privateAllow bool
	}{
		{"8.8.8.8", true, true},
		{"2001:4860:4860::8888", true, true},

		// Opened by AllowPrivateNetworks.
		{"127.0.0.1", false, true},
		{"::1", false, true},
		{"10.0.0.1", false, true},
		{"172.20.0.5", false, true}, // a typical docker compose network
		{"192.168.1.10", false, true},
		{"fd00::1", false, true},
		{"::ffff:192.168.1.10", false, true},

		// Blocked even then.
		{"169.254.169.254", false, false},
		{"::ffff:169.254.169.254", false, false},
		{"fe80::1", false, false},
		{"100.64.0.1", false, false},
		{"0.0.0.0", false, false},
		{"::", false, false},
		{"224.0.0.1", false, false},
		{"255.255.255.255", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			ip := netip.MustParseAddr(tt.addr)
			if got := (Policy{}).Allows(ip); got != tt.byDefault {
				t.Errorf("default policy: Allows = %v, want %v", got, tt.byDefault)
			}
			if got := (Policy{AllowPrivateNetworks: true}).Allows(ip); got != tt.privateAllow {
				t.Errorf("AllowPrivateNetworks: Allows = %v, want %v", got, tt.privateAllow)
			}
		})
	}
	if (Policy{AllowPrivateNetworks: true}).Allows(netip.Addr{}) {
		t.Error("the zero Addr is allowed, want blocked under every policy")
	}
}

// get sends a GET request and returns the response status code.
func get(t *testing.T, c *http.Client, url string) (int, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

func TestClientBlocksAtConnectTime(t *testing.T) {
	var hit atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit.Store(true) }))
	t.Cleanup(srv.Close)
	c := NewClient(Policy{}, 5*time.Second)

	// srv.URL is an IP literal; "localhost" goes through DNS first. Both
	// must be refused, because the check sees the address being dialed, not
	// the name in the URL.
	port := srv.URL[strings.LastIndex(srv.URL, ":"):]
	for _, url := range []string{srv.URL, "http://localhost" + port} {
		if _, err := get(t, c, url); !errors.Is(err, ErrBlocked) {
			t.Errorf("GET %s: error = %v, want ErrBlocked", url, err)
		}
	}
	if hit.Load() {
		t.Error("the blocked server received a request")
	}
}

func TestClientBlocksMetadataEvenForDevelopment(t *testing.T) {
	// Nothing listens there in a test, but the dialer refuses before
	// connecting, so the error is ErrBlocked rather than a timeout.
	c := NewClient(Policy{AllowPrivateNetworks: true}, 5*time.Second)
	if _, err := get(t, c, "http://169.254.169.254/latest/meta-data/"); !errors.Is(err, ErrBlocked) {
		t.Errorf("error = %v, want ErrBlocked", err)
	}
}

func TestClientAllowsPrivateNetworksWhenConfigured(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	status, err := get(t, NewClient(Policy{AllowPrivateNetworks: true}, 5*time.Second), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusNoContent {
		t.Errorf("status = %d, want 204", status)
	}
}

func TestClientDoesNotFollowRedirects(t *testing.T) {
	var followed atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/hook", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/internal", http.StatusFound)
	})
	mux.HandleFunc("/internal", func(http.ResponseWriter, *http.Request) { followed.Store(true) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	status, err := get(t, NewClient(Policy{AllowPrivateNetworks: true}, 5*time.Second), srv.URL+"/hook")
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusFound || followed.Load() {
		t.Errorf("status = %d, followed = %v; want the 302 itself, not followed", status, followed.Load())
	}
}

func TestClientIgnoresProxyEnvironment(t *testing.T) {
	// A test that sets HTTP_PROXY would prove little: Go never proxies
	// requests to localhost, and it reads the variables only once per
	// process. Checking the configuration is the direct test.
	tr, ok := NewClient(Policy{}, time.Second).Transport.(*http.Transport)
	if !ok {
		t.Fatal("Transport is not an *http.Transport")
	}
	if tr.Proxy != nil {
		t.Error("Transport.Proxy is set; a proxy would bypass the dialer check")
	}
}
