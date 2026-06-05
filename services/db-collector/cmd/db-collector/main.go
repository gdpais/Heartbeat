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
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"heartbeat/services/db-collector/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg := app.Config{
		ListenAddr:       env("HEARTBEAT_DB_COLLECTOR_LISTEN_ADDR", ":8082"),
		IntegrationsPath: env("HEARTBEAT_INTEGRATIONS_PATH", "config/integrations.yaml"),
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
