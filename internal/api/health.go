package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// Pinger reports whether a dependency is reachable. *pgxpool.Pool satisfies it.
type Pinger interface {
	Ping(ctx context.Context) error
}

// handleHealthz is the liveness probe: if the process answers, it is alive.
// It deliberately ignores the database, so a PostgreSQL outage does not make
// an orchestrator restart every instance, which would not fix anything.
func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReadyz is the readiness probe: the instance should receive traffic
// only while it can reach the database.
func handleReadyz(logger *slog.Logger, db Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		if err := db.Ping(ctx); err != nil {
			logger.WarnContext(ctx, "readiness check failed", "error", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}
