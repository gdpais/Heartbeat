// Package main is the entry point for the db-collector service.
//
// The binary reads its configuration from environment variables, wires up the
// collector runtime, and blocks until it receives SIGINT or SIGTERM.
//
// Environment variables:
//
//	HEARTBEAT_DB_COLLECTOR_LISTEN_ADDR  HTTP listen address (default ":8082")
//	HEARTBEAT_INTEGRATIONS_PATH         Path to integrations YAML file
//	                                    (default "config/integrations.yaml")
//	HEARTBEAT_ADMIN_TOKEN               Bearer token for the admin endpoints
//	                                    (GET /admin/config, POST /admin/config/reload);
//	                                    unset disables them
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"heartbeat/services/db-collector/internal/app"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	cfg := app.Config{
		ListenAddr:                      env("HEARTBEAT_DB_COLLECTOR_LISTEN_ADDR", ":8082"),
		IntegrationsPath:                env("HEARTBEAT_INTEGRATIONS_PATH", "config/integrations.yaml"),
		AdminToken:                      os.Getenv("HEARTBEAT_ADMIN_TOKEN"),
		WatchInterval:                   durationEnv("HEARTBEAT_CONFIG_WATCH_INTERVAL", 0),
		SQLServerTrustServerCertificate: boolEnv("HEARTBEAT_DB_COLLECTOR_SQLSERVER_TRUST_SERVER_CERTIFICATE", false),
		Logger:                          logger,
	}
	err := app.Run(ctx, cfg)
	stop()
	if err != nil {
		logger.Error("db-collector exited with error", "error", err)
		os.Exit(1)
	}
}

// env returns the value of the environment variable named key, or fallback if
// the variable is unset or empty.
func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func boolEnv(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "TRUE", "True", "yes", "YES", "Yes", "on", "ON", "On":
		return true
	case "0", "false", "FALSE", "False", "no", "NO", "No", "off", "OFF", "Off":
		return false
	default:
		return fallback
	}
}
