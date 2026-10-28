// Command carteiro is a webhook delivery service.
//
// Usage:
//
//	carteiro serve    run the HTTP API
//	carteiro migrate  apply pending database migrations
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joaomouravs/carteiro/internal/api"
	"github.com/joaomouravs/carteiro/internal/config"
	"github.com/joaomouravs/carteiro/internal/crypto"
	"github.com/joaomouravs/carteiro/internal/store"
)

const usage = "usage: carteiro <serve|migrate>"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "carteiro:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 1 {
		return errors.New(usage)
	}

	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		return fmt.Errorf("invalid configuration:\n%w", err)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))

	// ctx is cancelled on the first SIGINT (Ctrl+C) or SIGTERM (docker stop,
	// Kubernetes), which starts a graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch args[0] {
	case "serve":
		return serve(ctx, stop, cfg, logger)
	case "migrate":
		// Migrations run as a separate step, not on every serve, so a deploy
		// applies them once, before any new instance starts taking traffic.
		return store.Migrate(ctx, cfg.DatabaseURL, logger)
	default:
		return fmt.Errorf("unknown command %q\n%s", args[0], usage)
	}
}

func serve(ctx context.Context, stop context.CancelFunc, cfg config.Config, logger *slog.Logger) error {
	cipher, err := crypto.NewCipher(cfg.EncryptionKey)
	if err != nil {
		return err
	}
	pool, err := store.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if cfg.AllowHTTPEndpoints {
		logger.Warn("plain-http endpoint URLs are allowed; use only for local development")
	}

	srv := &http.Server{
		Handler: api.New(logger, pool, api.Options{
			AdminToken:         cfg.AdminToken,
			Cipher:             cipher,
			AllowHTTPEndpoints: cfg.AllowHTTPEndpoints,
		}).Handler(),
		// Without these, a client that opens a connection and sends bytes
		// slowly (Slowloris) holds a goroutine and a socket forever.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	// Listening before starting the goroutine makes a bind error (port in
	// use) fail here, synchronously.
	ln, err := new(net.ListenConfig).Listen(ctx, "tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	logger.Info("http server listening", "addr", ln.Addr().String())
	return serveUntilDone(ctx, stop, srv, ln, cfg.ShutdownTimeout, logger)
}

// serveUntilDone serves on ln until ctx is cancelled, then shuts srv down,
// giving in-flight requests up to timeout to finish.
func serveUntilDone(ctx context.Context, stop context.CancelFunc, srv *http.Server, ln net.Listener, timeout time.Duration, logger *slog.Logger) error {
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	select {
	case err := <-serveErr:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}
	// Restore default signal handling, so a second Ctrl+C kills the process
	// immediately instead of waiting for the shutdown timeout.
	stop()

	logger.Info("shutting down", "timeout", timeout.String())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// Shutdown stops accepting connections and waits for in-flight requests.
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}
	logger.Info("shutdown complete")
	return nil
}
