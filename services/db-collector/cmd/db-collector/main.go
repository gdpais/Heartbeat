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

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
