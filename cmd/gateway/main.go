// Command gateway is Qanat's data plane: it moves streams between Claude Code
// and upstream providers. It is stateless and has no synchronous database
// dependency on the hot path (PRD §5).
package main

import (
	"context"
	"net/http"
	"os"
	"time"

	"qanat/internal/config"
	"qanat/internal/platform"
)

// shutdownGrace lets in-flight streams finish on restart (PRD §15.3).
const shutdownGrace = 10 * time.Minute

func main() {
	log := platform.Logger("gateway")
	cfg, err := config.Load()
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(2)
	}
	ctx, stop := platform.SignalContext()
	defer stop()

	pg, err := platform.Postgres(ctx, cfg.PostgresDSN)
	if err != nil {
		log.Error("postgres", "err", err)
		os.Exit(2)
	}
	defer pg.Close()
	rdb := platform.Redis(cfg.RedisAddr)
	defer rdb.Close()

	mux := http.NewServeMux()
	platform.Health(mux, func(ctx context.Context) error {
		if err := pg.Ping(ctx); err != nil {
			return err
		}
		return rdb.Ping(ctx).Err()
	})

	srv := &http.Server{Addr: cfg.GatewayAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	if err := platform.Serve(ctx, log, srv, shutdownGrace); err != nil {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
}
