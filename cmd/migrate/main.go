package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"compras-remotas/internal/logging"
	"compras-remotas/internal/migrate"
	"compras-remotas/migrations"
)

const applyTimeout = 60 * time.Second

func main() {
	logger := logging.New(os.Stdout, slog.LevelInfo)

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		logger.Error("falta la variable DATABASE_URL con la cadena de conexión de postgres")
		os.Exit(1)
	}

	if err := run(dsn, logger); err != nil {
		logger.Error("no se pudo migrar el esquema", "error", err)
		os.Exit(1)
	}
}

func run(dsn string, logger *slog.Logger) error {
	ctx, cancel := context.WithTimeout(context.Background(), applyTimeout)
	defer cancel()

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("no se pudo abrir la conexión con postgres: %w", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("no se pudo conectar con postgres: %w", err)
	}

	applied, err := migrate.Apply(ctx, db, migrations.Files)
	if err != nil {
		return err
	}

	if len(applied) == 0 {
		logger.Info("el esquema ya está al día")
		return nil
	}

	for _, version := range applied {
		logger.Info("migración aplicada", "version", version)
	}

	return nil
}
