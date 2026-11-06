package postgres

import (
	"context"
	"fmt"

	"compras-remotas/internal/catalog"
	"compras-remotas/internal/config"
	"compras-remotas/internal/topology"
)

func (s *Store) Seed(ctx context.Context, kiosks []config.Kiosk, combos []catalog.Combo) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("no se pudo abrir la transacción del sembrado: %w", err)
	}
	defer tx.Rollback()

	if err := topology.Seed(ctx, tx, kiosks, combos); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("no se pudo confirmar el sembrado: %w", err)
	}

	return nil
}
