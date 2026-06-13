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
//	HEARTBEAT_ADMIN_TOKEN               Bearer token for admin reload endpoint
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"heartbeat/services/db-collector/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg := app.Config{
		ListenAddr:       env("HEARTBEAT_DB_COLLECTOR_LISTEN_ADDR", ":8082"),
		IntegrationsPath: env("HEARTBEAT_INTEGRATIONS_PATH", "config/integrations.yaml"),
		AdminToken:       os.Getenv("HEARTBEAT_ADMIN_TOKEN"),
		WatchInterval:    durationEnv("HEARTBEAT_CONFIG_WATCH_INTERVAL", 0),
	}
	if err := app.Run(ctx, cfg); err != nil {
		log.Fatal(err)
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
