package postgres_test

import (
	"context"
	"database/sql"
	"testing"

	"compras-remotas/internal/catalog"
	"compras-remotas/internal/config"
	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/machine"
	"compras-remotas/internal/store"
	"compras-remotas/internal/store/postgres"
)

func seededTopology() ([]config.Kiosk, []catalog.Combo) {
	return []config.Kiosk{
			{
				ID:   "kiosk-docker",
				Name: "Mueble docker",
				Machines: []config.Machine{
					{
						Type:         machine.Batidos,
						Name:         "Batidos Powermix",
						DeviceID:     "dev-docker",
						DeviceNo:     "devno-docker",
						DeviceTypeID: "dtype-docker",
					},
					{
						Type: machine.Snacks,
						Name: "Snacks Powermix",
						MID:  "mid-docker",
					},
				},
			},
		}, []catalog.Combo{
			{
				ID:         "combo-docker",
				Name:       "Batido + Alfajor",
				PriceCents: 420000,
				Currency:   "ARS",
				Active:     true,
				Items: []catalog.Item{
					{MachineType: "batidos", SKU: "DEMO-BATIDO-VAINILLA"},
					{MachineType: "snacks", SKU: "DEMO-SNACK-ALFAJOR"},
				},
			},
		}
}

func seededStore(t *testing.T) (*postgres.Store, *sql.DB) {
	t.Helper()

	target := dsn(t)

	migrateOnce.Do(func() { migrateFailed = applyMigrations(target) })
	if migrateFailed != nil {
		t.Fatalf("no se pudieron aplicar las migraciones: %v", migrateFailed)
	}

	admin, err := sql.Open("pgx", target)
	if err != nil {
		t.Fatalf("no se pudo abrir la base de prueba: %v", err)
	}
	t.Cleanup(func() {
		if err := admin.Close(); err != nil {
			t.Errorf("no se pudo cerrar la base de prueba: %v", err)
		}
	})

	if _, err := admin.Exec(`TRUNCATE webhook_event, dispatch_attempt, order_line, orders, payment_intent,
		catalog_snapshot, combo_item, combo, machine, kiosk RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("no se pudieron vaciar las tablas: %v", err)
	}

	st, err := postgres.Open(target)
	if err != nil {
		t.Fatalf("no se pudo abrir el store: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("no se pudo cerrar el store: %v", err)
		}
	})

	return st, admin
}

func TestSeedWritesTheTopologyThatAnOrderNeeds(t *testing.T) {
	ctx := context.Background()
	st, admin := seededStore(t)
	kiosks, combos := seededTopology()

	if err := st.Seed(ctx, kiosks, combos); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	if err := st.Seed(ctx, kiosks, combos); err != nil {
		t.Fatalf("Seed repetido: %v", err)
	}

	var machines int
	if err := admin.QueryRow(`SELECT count(*) FROM machine WHERE kiosk_id = $1`, "kiosk-docker").Scan(&machines); err != nil {
		t.Fatalf("no se pudieron contar las máquinas: %v", err)
	}
	if machines != 2 {
		t.Fatalf("máquinas del mueble sembrado = %d, se esperaban 2", machines)
	}

	var items int
	if err := admin.QueryRow(`SELECT count(*) FROM combo_item WHERE combo_id = $1`, "combo-docker").Scan(&items); err != nil {
		t.Fatalf("no se pudieron contar los ítems: %v", err)
	}
	if items != 2 {
		t.Fatalf("ítems del combo sembrado = %d, se esperaban 2", items)
	}

	order := store.Order{
		ID:          "order-docker",
		KioskID:     "kiosk-docker",
		ComboID:     "combo-docker",
		AmountCents: 420000,
		Currency:    "ARS",
		Status:      store.OrderPendingPayment,
		CreatedAt:   moment(),
		Lines: []store.Line{
			{
				ID:       "order-docker-batidos",
				Position: 1,
				Machine: dispenser.MachineRef{
					KioskID:      "kiosk-docker",
					Kind:         "batidos",
					Name:         "Batidos Powermix",
					DeviceID:     "dev-docker",
					DeviceNo:     "devno-docker",
					DeviceTypeID: "dtype-docker",
				},
				SKU:         "DEMO-BATIDO-VAINILLA",
				AmountCents: 210000,
				State:       dispenser.LinePending,
			},
		},
	}

	if err := st.CreateOrder(ctx, order); err != nil {
		t.Fatalf("CreateOrder después del sembrado: %v", err)
	}
}

func TestSeedRollsBackWhenTheSchemaRejectsAMachine(t *testing.T) {
	ctx := context.Background()
	st, admin := seededStore(t)
	kiosks, combos := seededTopology()
	kiosks[0].Machines[0].DeviceTypeID = ""

	if err := st.Seed(ctx, kiosks, combos); err == nil {
		t.Fatal("Seed aceptó una máquina de batidos sin device_type_id")
	}

	var stored int
	if err := admin.QueryRow(`SELECT count(*) FROM kiosk`).Scan(&stored); err != nil {
		t.Fatalf("no se pudieron contar los muebles: %v", err)
	}
	if stored != 0 {
		t.Fatalf("quedaron %d muebles después de un sembrado rechazado, se esperaban 0", stored)
	}
}
