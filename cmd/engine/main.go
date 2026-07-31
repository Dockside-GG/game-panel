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

	"github.com/dockside-gg/game-panel/internal/buildinfo"
	"github.com/dockside-gg/game-panel/internal/config"
	"github.com/dockside-gg/game-panel/internal/engine"
	"github.com/dockside-gg/game-panel/internal/healthcheck"
	"github.com/dockside-gg/game-panel/internal/logging"
)

func main() {
	healthcheck.MaybeRun(os.Args)
	if engine.MaybeRunUpdateHelper(os.Args) {
		return
	}
	cfg, err := config.Load("engine")
	if err != nil {
		slog.Error("configuration failed", "error", err)
		os.Exit(1)
	}
	build := buildinfo.Current()
	logger := logging.New(cfg.LogLevel).With(
		"component", "engine",
		"instance_id", cfg.InstanceID,
		"version", build.Version,
		"revision", build.Revision,
	)
	runtime, err := engine.New(cfg, logger)
	if err != nil {
		logger.Error("engine initialization failed", "error", err)
		os.Exit(1)
	}
	defer runtime.Close()

	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	server := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           runtime.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	go func() {
		<-rootCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	logger.Info("engine listening", "address", cfg.ListenAddress)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("engine server failed", "error", err)
		os.Exit(1)
	}
}
