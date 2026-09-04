package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/huangxinxinyu/nano-notebook/internal/app"
)

type migrationConfig struct {
	ApplicationDatabaseURL string
}

func main() {
	if err := runMigrations(context.Background(), loadMigrationConfig()); err != nil {
		slog.Error("migrations failed", "error", err)
		os.Exit(1)
	}
	slog.Info("Application migrations applied")
}

func loadMigrationConfig() migrationConfig {
	return migrationConfig{
		ApplicationDatabaseURL: env("NANO_DATABASE_URL", "postgres://nano:nano@localhost:55432/nano?sslmode=disable"),
	}
}

func runMigrations(ctx context.Context, config migrationConfig) error {
	db, err := app.OpenDB(ctx, config.ApplicationDatabaseURL)
	if err != nil {
		return fmt.Errorf("open Application database: %w", err)
	}
	defer db.Close()
	if err := app.RunMigrations(ctx, db); err != nil {
		return fmt.Errorf("run Application migrations: %w", err)
	}
	return nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
