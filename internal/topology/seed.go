package topology

import (
	"context"
	"database/sql"
	"fmt"

	"compras-remotas/internal/catalog"
	"compras-remotas/internal/config"
)

type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

const (
	kioskUpsert = `INSERT INTO kiosk (id, name)
		SELECT $1, $2 WHERE to_regclass('kiosk') IS NOT NULL
		ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, updated_at = now()`

	machineUpsert = `INSERT INTO machine (kiosk_id, type, name, batidos_device_id, batidos_device_no,
		batidos_device_type_id, snacks_mid)
		SELECT $1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''), NULLIF($7, '')
		WHERE to_regclass('machine') IS NOT NULL
		ON CONFLICT (kiosk_id, type) DO UPDATE SET name = EXCLUDED.name,
			batidos_device_id = EXCLUDED.batidos_device_id,
			batidos_device_no = EXCLUDED.batidos_device_no,
			batidos_device_type_id = EXCLUDED.batidos_device_type_id,
			snacks_mid = EXCLUDED.snacks_mid`

	comboUpsert = `INSERT INTO combo (id, name, price_cents, currency, active)
		SELECT $1, $2, $3, COALESCE(NULLIF($4, ''), 'ARS'), $5
		WHERE to_regclass('combo') IS NOT NULL
		ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, price_cents = EXCLUDED.price_cents,
			currency = EXCLUDED.currency, active = EXCLUDED.active, updated_at = now()`

	comboItemUpsert = `INSERT INTO combo_item (combo_id, position, machine_type, sku)
		SELECT $1, $2, $3, $4 WHERE to_regclass('combo_item') IS NOT NULL
		ON CONFLICT (combo_id, position) DO UPDATE SET machine_type = EXCLUDED.machine_type,
			sku = EXCLUDED.sku`
)

func Seed(ctx context.Context, db Execer, kiosks []config.Kiosk, combos []catalog.Combo) error {
	for _, kiosk := range kiosks {
		if _, err := db.ExecContext(ctx, kioskUpsert, kiosk.ID, kiosk.Name); err != nil {
			return fmt.Errorf("no se pudo sembrar el mueble %s: %w", kiosk.ID, err)
		}

		for _, machine := range kiosk.Machines {
			_, err := db.ExecContext(ctx, machineUpsert, kiosk.ID, string(machine.Type), machine.Name,
				machine.DeviceID, machine.DeviceNo, machine.DeviceTypeID, machine.MID)
			if err != nil {
				return fmt.Errorf("no se pudo sembrar la máquina de %s del mueble %s: %w", machine.Type, kiosk.ID, err)
			}
		}
	}

	for _, combo := range combos {
		_, err := db.ExecContext(ctx, comboUpsert, combo.ID, combo.Name, combo.PriceCents, combo.Currency, combo.Active)
		if err != nil {
			return fmt.Errorf("no se pudo sembrar el combo %s: %w", combo.ID, err)
		}

		for position, item := range combo.Items {
			if _, err := db.ExecContext(ctx, comboItemUpsert, combo.ID, position, string(item.MachineType), item.SKU); err != nil {
				return fmt.Errorf("no se pudo sembrar el ítem %d del combo %s: %w", position+1, combo.ID, err)
			}
		}
	}

	return nil
}
