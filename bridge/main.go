package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/asimzeeshan/WhatsApp-bridge/bridge/api"
	"github.com/asimzeeshan/WhatsApp-bridge/bridge/client"
	"github.com/asimzeeshan/WhatsApp-bridge/bridge/config"
	"github.com/asimzeeshan/WhatsApp-bridge/bridge/connection"
	"github.com/asimzeeshan/WhatsApp-bridge/bridge/store"
)

func main() {
	configPath := flag.String("config", "config.toml", "path to config file")
	flag.Parse()

	// Setup structured logging to stderr (stdout reserved for nothing — bridge uses HTTP)
	level := slog.LevelInfo
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	// Load config
	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Parse log level from config
	switch cfg.Bridge.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	logger = slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	logger.Info("starting WhatsApp bridge", "addr", cfg.Bridge.Addr, "data_dir", cfg.Bridge.DataDir, "db_driver", cfg.Bridge.Database.Driver)

	// Open database
	db, err := store.OpenWithDriver(cfg.Bridge.Database.Driver, cfg.Bridge.Database.DSN, cfg.Bridge.DataDir, logger)
	if err != nil {
		logger.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	// NOTE: db.Close() is called in the signal handler, NOT via defer,
	// to avoid double-close panic (Writer.Stop closes the channel once).

	// Connection manager
	connMgr := connection.NewManager(cfg.Bridge.DataDir, cfg.Bridge.CooldownMinutes, logger)

	// Check cooldown before connecting
	if err := connMgr.CheckCooldown(); err != nil {
		logger.Error("cooldown active", "error", err)
		os.Exit(2)
	}

	// Create bridge client for event handling
	bridge := client.New(db, cfg, logger)
	bridge.OnPermanentDisconnect = func(reason string, writeCooldown bool) {
		connMgr.HandlePermanentDisconnect(reason, writeCooldown)
	}

	// Connect to WhatsApp
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := connMgr.Connect(ctx); err != nil {
		logger.Error("failed to connect to WhatsApp", "error", err)
		logger.Info("bridge will start without WhatsApp connection — connect via QR later")
	} else {
		// Register event handlers (also stores the client reference for media downloads)
		bridge.RegisterEventHandlers(connMgr.Client)
	}

	// Start API server
	srv := api.NewServer(db, connMgr, cfg, logger)

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		logger.Info("received signal, shutting down", "signal", sig)

		// 1. Stop accepting new HTTP requests
		if err := srv.Shutdown(); err != nil {
			logger.Error("HTTP shutdown error", "error", err)
		}

		// 2. Stop reconnection and disconnect WhatsApp
		connMgr.Stop()

		// 3. Close database (drains writer)
		if err := db.Close(); err != nil {
			logger.Error("database close error", "error", err)
		}

		logger.Info("shutdown complete")
		os.Exit(0)
	}()

	// Start serving
	if err := srv.Start(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}
