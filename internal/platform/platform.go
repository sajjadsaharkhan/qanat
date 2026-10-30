// Package platform holds the runtime plumbing both binaries share: logging,
// connections to PostgreSQL and Redis, and graceful HTTP serving.
package platform

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Logger returns the structured JSON logger (PRD §15.1). Never log bodies.
func Logger(service string) *slog.Logger {
	l := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", service)
	slog.SetDefault(l)
	return l
}

// SignalContext is cancelled on SIGINT or SIGTERM.
func SignalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// Postgres opens a pool. It does not dial; use WaitFor to block until up.
func Postgres(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	return pgxpool.NewWithConfig(ctx, cfg)
}

// Redis returns a client. Short timeouts: Redis sits on the hot path, and a
// slow Redis must degrade quickly rather than stall streams (§15.3).
func Redis(addr string) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:         addr,
		DialTimeout:  2 * time.Second,
		ReadTimeout:  500 * time.Millisecond,
		WriteTimeout: 500 * time.Millisecond,
		PoolSize:     64,
		MinIdleConns: 4,
	})
}

// WaitFor retries ping until it succeeds or ctx ends, logging each failure.
func WaitFor(ctx context.Context, log *slog.Logger, what string, ping func(context.Context) error) error {
	for delay := 250 * time.Millisecond; ; delay = min(delay*2, 5*time.Second) {
		pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := ping(pctx)
		cancel()
		if err == nil {
			return nil
		}
		log.Warn("waiting for dependency", "dependency", what, "err", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

// Serve runs srv until ctx is cancelled, then shuts down gracefully, giving
// in-flight requests up to grace to finish (PRD §15.3: 10 minutes for streams).
func Serve(ctx context.Context, log *slog.Logger, srv *http.Server, grace time.Duration) error {
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return err
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	log.Info("listening", "addr", ln.Addr().String())

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down", "grace", grace.String())
	sctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}

// Health serves /healthz (process alive) and /readyz (ready() == nil).
func Health(mux *http.ServeMux, ready func(context.Context) error) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if err := ready(ctx); err != nil {
			http.Error(w, "not ready: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}
