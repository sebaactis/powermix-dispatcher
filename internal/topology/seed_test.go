package topology_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"compras-remotas/internal/catalog"
	"compras-remotas/internal/config"
	"compras-remotas/internal/topology"
)

type call struct {
	query string
	args  []any
}

type recorder struct {
	calls []call
	err   error
}

func (r *recorder) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	if r.err != nil {
		return nil, r.err
	}

	r.calls = append(r.calls, call{query: query, args: args})

	return result(0), nil
}

type result int64

func (r result) LastInsertId() (int64, error) { return 0, nil }
func (r result) RowsAffected() (int64, error) { return int64(r), nil }

func kiosks() []config.Kiosk {
	return []config.Kiosk{
		{
			ID:   "kiosk-uno",
			Name: "Mueble uno",
			Machines: []config.Machine{
				{
					Type:         config.MachineBatidos,
					Name:         "Batidos Powermix",
					DeviceID:     "dev-1",
					DeviceNo:     "devno-1",
					DeviceTypeID: "dtype-1",
				},
				{
					Type: config.MachineSnacks,
					Name: "Snacks Powermix",
					MID:  "mid-1",
				},
			},
		},
	}
}

func combos() []catalog.Combo {
	return []catalog.Combo{
		{
			ID:         "combo-uno",
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

func TestSeedUpsertsTheWholeTopology(t *testing.T) {
	db := &recorder{}

	if err := topology.Seed(context.Background(), db, kiosks(), combos()); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	if len(db.calls) != 6 {
		t.Fatalf("sentencias = %d, se esperaban 6 (mueble, 2 máquinas, combo, 2 ítems)", len(db.calls))
	}

	for _, index := range []int{0, 1, 2, 3, 4, 5} {
		if !strings.Contains(db.calls[index].query, "ON CONFLICT") {
			t.Fatalf("la sentencia %d no es un upsert: %s", index, db.calls[index].query)
		}
	}
}

func TestSeedPassesTheKioskColumnsTheSchemaDeclares(t *testing.T) {
	db := &recorder{}

	if err := topology.Seed(context.Background(), db, kiosks(), combos()); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	kiosk := db.calls[0]
	if !strings.Contains(kiosk.query, "INSERT INTO kiosk (id, name)") {
		t.Fatalf("sentencia del mueble = %q", kiosk.query)
	}
	if len(kiosk.args) != 2 || kiosk.args[0] != "kiosk-uno" || kiosk.args[1] != "Mueble uno" {
		t.Fatalf("argumentos del mueble = %+v", kiosk.args)
	}
}

func TestSeedSplitsTheMachineIdentifiersByType(t *testing.T) {
	db := &recorder{}

	if err := topology.Seed(context.Background(), db, kiosks(), combos()); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	batidos := db.calls[1]
	if !strings.Contains(batidos.query, "INSERT INTO machine (kiosk_id, type, name") {
		t.Fatalf("sentencia de la máquina = %q", batidos.query)
	}
	if len(batidos.args) != 7 {
		t.Fatalf("argumentos de la máquina = %+v, se esperaban 7 columnas", batidos.args)
	}
	if batidos.args[1] != "batidos" || batidos.args[3] != "dev-1" || batidos.args[6] != "" {
		t.Fatalf("la máquina de batidos quedó con %+v", batidos.args)
	}

	snacks := db.calls[2]
	if snacks.args[1] != "snacks" || snacks.args[3] != "" || snacks.args[6] != "mid-1" {
		t.Fatalf("la máquina de snacks quedó con %+v", snacks.args)
	}
}

func TestSeedPassesEveryComboItemWithItsPosition(t *testing.T) {
	db := &recorder{}

	if err := topology.Seed(context.Background(), db, kiosks(), combos()); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	combo := db.calls[3]
	if len(combo.args) != 5 || combo.args[0] != "combo-uno" || combo.args[2] != int64(420000) {
		t.Fatalf("argumentos del combo = %+v", combo.args)
	}

	first := db.calls[4]
	if len(first.args) != 4 || first.args[0] != "combo-uno" || first.args[1] != 0 {
		t.Fatalf("argumentos del primer ítem = %+v", first.args)
	}
	if first.args[2] != "batidos" || first.args[3] != "DEMO-BATIDO-VAINILLA" {
		t.Fatalf("el primer ítem quedó con %+v", first.args)
	}
}

func TestSeedWritesEveryKioskAndCombo(t *testing.T) {
	db := &recorder{}
	extra := append(kiosks(), config.Kiosk{
		ID:   "kiosk-dos",
		Name: "Mueble dos",
		Machines: []config.Machine{
			{Type: config.MachineBatidos, Name: "Batidos", DeviceID: "d", DeviceNo: "n", DeviceTypeID: "t"},
			{Type: config.MachineSnacks, Name: "Snacks", MID: "m"},
		},
	})

	if err := topology.Seed(context.Background(), db, extra, combos()); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	if len(db.calls) != 9 {
		t.Fatalf("sentencias = %d, se esperaban 9", len(db.calls))
	}
	if db.calls[3].args[0] != "kiosk-dos" {
		t.Fatalf("el segundo mueble quedó con %+v", db.calls[3].args)
	}
}

func TestSeedReportsWhichWriteFailed(t *testing.T) {
	failure := errors.New("la base no responde")
	db := &recorder{err: failure}

	err := topology.Seed(context.Background(), db, kiosks(), combos())
	if err == nil {
		t.Fatal("Seed no reportó el fallo de escritura")
	}
	if !errors.Is(err, failure) {
		t.Fatalf("error = %v, se esperaba que envuelva el fallo original", err)
	}
	if !strings.Contains(err.Error(), "kiosk-uno") {
		t.Fatalf("error = %q, se esperaba que nombre el mueble que falló", err)
	}
}

func TestSeedWithoutTopologyWritesNothing(t *testing.T) {
	db := &recorder{}

	if err := topology.Seed(context.Background(), db, nil, nil); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if len(db.calls) != 0 {
		t.Fatalf("sentencias = %d, se esperaban cero", len(db.calls))
	}
}
