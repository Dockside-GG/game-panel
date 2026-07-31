package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/dockside-gg/game-panel/internal/config"
	"github.com/dockside-gg/game-panel/internal/db"
	"github.com/dockside-gg/game-panel/internal/engineclient"
	"github.com/dockside-gg/game-panel/internal/logging"
	"github.com/dockside-gg/game-panel/internal/worker"
)

func main() {
	cfg, err := config.Load("worker")
	if err != nil {
		slog.Error("configuration failed", "error", err)
		os.Exit(1)
	}
	logger := logging.New(cfg.LogLevel).With("component", "worker", "instance_id", cfg.InstanceID)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		logger.Error("database migration failed", "error", err)
		os.Exit(1)
	}
	engine := engineclient.New(cfg.EngineURL, cfg.EngineToken)
	runner, err := worker.New(cfg, pool, engine, logger)
	if err != nil {
		logger.Error("worker initialization failed", "error", err)
		os.Exit(1)
	}
	if err := runner.Run(ctx); err != nil {
		logger.Error("worker failed", "error", err)
		os.Exit(1)
	}
}
