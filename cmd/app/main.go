package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dockside-gg/game-panel/internal/config"
	"github.com/dockside-gg/game-panel/internal/db"
	"github.com/dockside-gg/game-panel/internal/engineclient"
	"github.com/dockside-gg/game-panel/internal/healthcheck"
	"github.com/dockside-gg/game-panel/internal/httpapi"
	"github.com/dockside-gg/game-panel/internal/logging"
	"github.com/dockside-gg/game-panel/internal/store"
	"github.com/dockside-gg/game-panel/internal/templates"
)

func main() {
	healthcheck.MaybeRun(os.Args)

	cfg, err := config.Load("app")
	if err != nil {
		slog.Error("configuration failed", "error", err)
		os.Exit(1)
	}
	logger := logging.New(cfg.LogLevel).With("component", "app", "instance_id", cfg.InstanceID)

	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(rootCtx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := db.Migrate(rootCtx, pool); err != nil {
		logger.Error("database migration failed", "error", err)
		os.Exit(1)
	}
	templateCount, err := templates.Seed(rootCtx, pool)
	if err != nil {
		logger.Error("embedded template catalog initialization failed", "error", err)
		os.Exit(1)
	}
	logger.Info("embedded template catalog ready", "templates", templateCount)
	dataStore := store.New(pool)
	if err := dataStore.EnsureInstallation(
		rootCtx,
		cfg.InstanceID,
		cfg.PublicURL.String(),
		cfg.DiscordClientID,
		cfg.BootstrapToken,
		cfg.MFAPolicy,
	); err != nil {
		logger.Error("installation initialization failed", "error", err)
		os.Exit(1)
	}

	engine := engineclient.New(cfg.EngineURL, cfg.EngineToken)
	api, err := httpapi.New(cfg, dataStore, pool, engine, logger)
	if err != nil {
		logger.Error("http api initialization failed", "error", err)
		os.Exit(1)
	}
	server := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	go func() {
		<-rootCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("http shutdown failed", "error", err)
		}
	}()

	logger.Info("app listening", "address", cfg.ListenAddress, "public_url", cfg.PublicURL.String())
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("http server failed", "error", err)
		os.Exit(1)
	}
}
