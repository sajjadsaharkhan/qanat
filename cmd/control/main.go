// Command control is Qanat's control plane: the Admin API, dashboard sign-in
// and the usage aggregation worker (PRD §5). It owns schema migrations.
package main

import (
	"context"
	"net/http"
	"os"
	"time"

	"qanat/internal/config"
	"qanat/internal/control"
	"qanat/internal/core/secretbox"
	"qanat/internal/platform"
	"qanat/internal/store"
)

func main() {
	log := platform.Logger("control")
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

	if err := platform.WaitFor(ctx, log, "postgres", pg.Ping); err != nil {
		os.Exit(1)
	}
	if err := store.Migrate(ctx, pg); err != nil {
		log.Error("migrate", "err", err)
		os.Exit(1)
	}
	log.Info("schema up to date")

	var box *secretbox.Box
	if cfg.MasterKey != "" {
		if box, err = secretbox.New(cfg.MasterKey); err != nil {
			log.Error("QANAT_MASTER_KEY", "err", err)
			os.Exit(2)
		}
	}

	// The single consumer of the usage stream (D17).
	worker := control.NewWorker(pg, rdb, log)
	workerCtx, stopWorker := context.WithCancel(context.Background())
	defer stopWorker()
	go worker.Run(workerCtx)

	api := control.New(pg, rdb, log, control.Options{
		RootPassword:        cfg.RootPassword,
		GatewayURL:          cfg.PublicGatewayURL,
		SecureCookies:       !cfg.InsecureCookies,
		Box:                 box,
		Worker:              worker,
		TestMode:            cfg.TestMode,
		OIDCIssuer:          cfg.OIDCIssuer,
		OIDCInternalURL:     cfg.OIDCInternalURL,
		OIDCClientID:        cfg.OIDCClientID,
		OIDCClientSecret:    cfg.OIDCClientSecret,
		BootstrapAdminEmail: cfg.BootstrapAdminEmail,
		WebURL:              cfg.WebURL,
		ProxyIPEchoURL:      cfg.ProxyIPEchoURL,
	})
	if err := api.Bootstrap(ctx); err != nil {
		log.Error("bootstrap", "err", err)
		os.Exit(1)
	}
	log.Info("admin api ready", "root_login", cfg.RootPassword != "")

	// Provider OAuth token refresh (REQ-OAUTH-03).
	go api.RunTokenRefresh(workerCtx)

	mux := http.NewServeMux()
	api.Register(mux)
	platform.Health(mux, func(ctx context.Context) error {
		if err := pg.Ping(ctx); err != nil {
			return err
		}
		return rdb.Ping(ctx).Err()
	})

	srv := &http.Server{Addr: cfg.ControlAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	if err := platform.Serve(ctx, log, srv, 30*time.Second); err != nil {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
}
