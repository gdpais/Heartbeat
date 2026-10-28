// Package main is the entry point for the API service, Heartbeat's control
// plane (ADR 0013).
//
// The binary reads its configuration from environment variables, serves the
// HTTP API, and blocks until it receives SIGINT or SIGTERM.
//
// Environment variables:
//
//	HEARTBEAT_API_LISTEN_ADDR  HTTP listen address, host:port (default ":8081")
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"heartbeat/apps/api/internal/app"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(logger)
	cfg, err := loadConfig()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	cfg.Logger = logger
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err = app.Run(ctx, cfg)
	stop()
	if err != nil {
		logger.Error("api exited with error", "error", err)
		os.Exit(1)
	}
}

// loadConfig reads the service configuration from the environment. Unset
// variables take their defaults; a value that cannot work is an error rather
// than a silent fallback, so a typo stops startup instead of running with a
// setting nobody chose.
func loadConfig() (app.Config, error) {
	cfg := app.Config{
		ListenAddr: env("HEARTBEAT_API_LISTEN_ADDR", ":8081"),
	}
	if _, port, err := net.SplitHostPort(cfg.ListenAddr); err != nil || port == "" {
		return app.Config{}, fmt.Errorf("HEARTBEAT_API_LISTEN_ADDR %q: want host:port, such as :8081 or 127.0.0.1:8081", cfg.ListenAddr)
	}
	return cfg, nil
}

// env returns the value of the environment variable named key, or fallback if
// the variable is unset or empty.
func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
