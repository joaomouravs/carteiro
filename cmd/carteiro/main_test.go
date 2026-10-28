package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func listen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

// TestShutdownWaitsForInFlightRequests is NFR-REL-04: a request that is
// running when SIGTERM arrives still completes.
func TestShutdownWaitsForInFlightRequests(t *testing.T) {
	started := make(chan struct{})
	slow := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		time.Sleep(300 * time.Millisecond)
		_, _ = io.WriteString(w, "done")
	})
	ln := listen(t)
	url := "http://" + ln.Addr().String()
	ctx, cancel := context.WithCancel(context.Background())

	served := make(chan error, 1)
	go func() { served <- serveUntilDone(ctx, cancel, &http.Server{Handler: slow}, ln, 5*time.Second, discard) }()

	type result struct {
		body string
		err  error
	}
	got := make(chan result, 1)
	go func() {
		resp, err := http.Get(url)
		if err != nil {
			got <- result{err: err}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		got <- result{string(body), err}
	}()

	<-started
	cancel() // what SIGTERM does in production

	if r := <-got; r.err != nil || r.body != "done" {
		t.Errorf("in-flight request: body %q, err %v; want it to complete", r.body, r.err)
	}
	if err := <-served; err != nil {
		t.Errorf("serveUntilDone: %v", err)
	}

	client := http.Client{Timeout: time.Second}
	if resp, err := client.Get(url); err == nil {
		_ = resp.Body.Close()
		t.Error("server still accepts connections after shutdown")
	}
}

// TestShutdownGivesUpAfterTimeout checks that a stuck request cannot keep the
// process alive forever.
func TestShutdownGivesUpAfterTimeout(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	stuck := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(started)
		<-release
	})
	ln := listen(t)
	ctx, cancel := context.WithCancel(context.Background())

	served := make(chan error, 1)
	go func() {
		served <- serveUntilDone(ctx, cancel, &http.Server{Handler: stuck}, ln, 100*time.Millisecond, discard)
	}()
	go func() {
		if resp, err := http.Get("http://" + ln.Addr().String()); err == nil {
			_ = resp.Body.Close()
		}
	}()

	<-started
	cancel()

	select {
	case err := <-served:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("err = %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serveUntilDone did not return after its timeout")
	}
}
