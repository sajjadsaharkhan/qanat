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
	"qanat/internal/core/quota"
	"qanat/internal/core/secretbox"
	"qanat/internal/gateway"
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

	var box *secretbox.Box
	if cfg.MasterKey != "" {
		if box, err = secretbox.New(cfg.MasterKey); err != nil {
			log.Error("QANAT_MASTER_KEY", "err", err)
			os.Exit(2)
		}
	}

	pg, err := platform.Postgres(ctx, cfg.PostgresDSN)
	if err != nil {
		log.Error("postgres", "err", err)
		os.Exit(2)
	}
	defer pg.Close()
	rdb := platform.Redis(cfg.RedisAddr)
	defer rdb.Close()

	snap := gateway.NewStore(pg, box, log)
	watchCtx, stopWatch := context.WithCancel(context.Background())
	defer stopWatch()
	go snap.Run(watchCtx)

	pub := gateway.NewPublisher(rdb, log)
	gw := gateway.New(snap, quota.NewStore(rdb), pub, pg, log, cfg.TestMode)

	go gw.RunProxyHealth(watchCtx, rdb)

	mux := http.NewServeMux()
	gw.Register(mux)
	platform.Health(mux, gw.Ready)

	srv := &http.Server{Addr: cfg.GatewayAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	if err := platform.Serve(ctx, log, srv, shutdownGrace); err != nil {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
	// Streams are done; flush buffered usage before exiting (§15.3).
	drain, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pub.Close(drain)
}
