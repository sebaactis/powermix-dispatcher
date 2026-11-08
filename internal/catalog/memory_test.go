package catalog_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"compras-remotas/internal/catalog"
	"compras-remotas/internal/machine"
)

const (
	batidosSKU = "sku-batido-vainilla"
	snacksSKU  = "sku-snack-alfajor"
)

func drinkItem() catalog.Item {
	return catalog.Item{MachineType: "batidos", SKU: batidosSKU}
}

func snackItem() catalog.Item {
	return catalog.Item{MachineType: "snacks", SKU: snacksSKU}
}

func testCombo(id, name string, active bool, items ...catalog.Item) catalog.Combo {
	return catalog.Combo{
		ID:         id,
		Name:       name,
		PriceCents: 420000,
		Currency:   "ARS",
		Active:     active,
		Items:      items,
	}
}

func mustNewMemory(t *testing.T, combos []catalog.Combo) *catalog.Memory {
	t.Helper()

	memory, err := catalog.NewMemory(combos)
	if err != nil {
		t.Fatalf("NewMemory: %v", err)
	}

	return memory
}

func mustCombo(t *testing.T, memory *catalog.Memory, id string) catalog.Combo {
	t.Helper()

	combo, err := memory.Combo(context.Background(), id)
	if err != nil {
		t.Fatalf("Combo(%q): %v", id, err)
	}

	return combo
}

func comboIDs(combos []catalog.Combo) []string {
	ids := make([]string, 0, len(combos))
	for _, combo := range combos {
		ids = append(ids, combo.ID)
	}

	return ids
}

func TestCombosReturnsOnlyActiveOrderedByID(t *testing.T) {
	memory := mustNewMemory(t, []catalog.Combo{
		testCombo("combo-z", "Batido + Chocolate", true, drinkItem(), snackItem()),
		testCombo("combo-a", "Batido + Alfajor", true, drinkItem(), snackItem()),
		testCombo("combo-m", "Combo retirado", false, drinkItem(), snackItem()),
	})

	got, err := memory.Combos(context.Background())
	if err != nil {
		t.Fatalf("Combos: %v", err)
	}

	want := []string{"combo-a", "combo-z"}
	if ids := comboIDs(got); !reflect.DeepEqual(ids, want) {
		t.Fatalf("Combos() devolvió %v, se esperaba %v", ids, want)
	}
}

func TestComboReturnsTheComboWithItsItems(t *testing.T) {
	want := testCombo("combo-1", "Batido + Alfajor", true, drinkItem(), snackItem())
	memory := mustNewMemory(t, []catalog.Combo{want})

	got := mustCombo(t, memory, "combo-1")

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Combo(%q) devolvió %+v, se esperaba %+v", "combo-1", got, want)
	}
}

func TestComboReturnsNotFound(t *testing.T) {
	memory := mustNewMemory(t, []catalog.Combo{
		testCombo("combo-1", "Batido + Alfajor", true, drinkItem(), snackItem()),
		testCombo("combo-inactivo", "Combo retirado", false, drinkItem(), snackItem()),
	})

	cases := []struct {
		name string
		id   string
	}{
		{name: "id desconocido", id: "combo-inexistente"},
		{name: "combo inactivo", id: "combo-inactivo"},
		{name: "id vacío", id: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			combo, err := memory.Combo(context.Background(), tc.id)
			if !errors.Is(err, catalog.ErrNotFound) {
				t.Fatalf("Combo(%q) devolvió error %v, se esperaba ErrNotFound", tc.id, err)
			}
			if combo.ID != "" {
				t.Fatalf("Combo(%q) devolvió %+v, se esperaba el combo vacío", tc.id, combo)
			}
		})
	}
}

func TestNewMemoryRejectsInvalidCombos(t *testing.T) {
	valid := testCombo("combo-1", "Batido + Alfajor", true, drinkItem(), snackItem())

	cases := []struct {
		name      string
		combos    []catalog.Combo
		wantInMsg string
	}{
		{
			name:      "sin ítems",
			combos:    []catalog.Combo{testCombo("combo-sin-items", "Batido solo", true)},
			wantInMsg: "combo-sin-items",
		},
		{
			name: "id vacío",
			combos: []catalog.Combo{
				valid,
				{Name: "Sin id", PriceCents: 100, Items: []catalog.Item{drinkItem()}},
			},
			wantInMsg: "posición 2",
		},
		{
			name: "nombre vacío",
			combos: []catalog.Combo{
				testCombo("combo-sin-nombre", "", true, drinkItem(), snackItem()),
			},
			wantInMsg: "combo-sin-nombre",
		},
		{
			name: "precio negativo",
			combos: []catalog.Combo{
				{
					ID:         "combo-precio-negativo",
					Name:       "Batido + Alfajor",
					PriceCents: -1,
					Currency:   "ARS",
					Active:     true,
					Items:      []catalog.Item{drinkItem(), snackItem()},
				},
			},
			wantInMsg: "combo-precio-negativo",
		},
		{
			name: "ítem sin tipo de máquina",
			combos: []catalog.Combo{
				testCombo("combo-sin-maquina", "Batido + Alfajor", true,
					catalog.Item{MachineType: "", SKU: batidosSKU}, snackItem()),
			},
			wantInMsg: "combo-sin-maquina",
		},
		{
			name: "ítem sin sku",
			combos: []catalog.Combo{
				testCombo("combo-sin-sku", "Batido + Alfajor", true,
					drinkItem(), catalog.Item{MachineType: "snacks", SKU: ""}),
			},
			wantInMsg: "combo-sin-sku",
		},
		{
			name: "ids duplicados",
			combos: []catalog.Combo{
				testCombo("combo-1", "Batido + Alfajor", true, drinkItem(), snackItem()),
				testCombo("combo-1", "Batido + Chocolate", true, drinkItem(), snackItem()),
			},
			wantInMsg: "combo-1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory, err := catalog.NewMemory(tc.combos)
			if err == nil {
				t.Fatalf("NewMemory aceptó un catálogo inválido y devolvió %+v", memory)
			}
			if !strings.Contains(err.Error(), tc.wantInMsg) {
				t.Fatalf("el error %q no nombra el combo ofensor (%q)", err, tc.wantInMsg)
			}
		})
	}
}

func TestMutatingAReadComboDoesNotChangeTheCatalog(t *testing.T) {
	memory := mustNewMemory(t, []catalog.Combo{
		testCombo("combo-1", "Batido + Alfajor", true, drinkItem(), snackItem()),
	})

	first := mustCombo(t, memory, "combo-1")
	first.Items[0].SKU = "sku-mutado"
	first.Name = "nombre mutado"

	list, err := memory.Combos(context.Background())
	if err != nil {
		t.Fatalf("Combos: %v", err)
	}
	list[0].Items[1].SKU = "sku-mutado-desde-la-lista"

	second := mustCombo(t, memory, "combo-1")

	if second.Name != "Batido + Alfajor" {
		t.Fatalf("el nombre del combo cambió a %q", second.Name)
	}
	if second.Items[0].SKU != batidosSKU {
		t.Fatalf("el sku del primer ítem cambió a %q", second.Items[0].SKU)
	}
	if second.Items[1].SKU != snacksSKU {
		t.Fatalf("el sku del segundo ítem cambió a %q", second.Items[1].SKU)
	}
}

func TestMutatingTheInputSliceDoesNotChangeTheCatalog(t *testing.T) {
	combos := []catalog.Combo{
		testCombo("combo-1", "Batido + Alfajor", true, drinkItem(), snackItem()),
	}

	memory := mustNewMemory(t, combos)

	combos[0].Name = "nombre mutado"
	combos[0].Items[0].SKU = "sku-mutado"
	combos[0].Items = append(combos[0].Items, catalog.Item{MachineType: "snacks", SKU: "sku-agregado"})

	got := mustCombo(t, memory, "combo-1")

	if got.Name != "Batido + Alfajor" {
		t.Fatalf("el nombre del combo cambió a %q", got.Name)
	}
	if len(got.Items) != 2 {
		t.Fatalf("el combo tiene %d ítems, se esperaban 2", len(got.Items))
	}
	if got.Items[0].SKU != batidosSKU {
		t.Fatalf("el sku del primer ítem cambió a %q", got.Items[0].SKU)
	}
}

func TestLoadFileReadsValidCombos(t *testing.T) {
	path := filepath.Join(t.TempDir(), "combos.json")
	raw := `[
		{
			"id": "combo-1",
			"name": "Batido + Alfajor",
			"price_cents": 420000,
			"currency": "ARS",
			"active": true,
			"items": [
				{"machine_type": "batidos", "sku": "sku-batido-vainilla"},
				{"machine_type": "snacks", "sku": "sku-snack-alfajor"}
			]
		}
	]`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("no se pudo escribir el archivo de prueba: %v", err)
	}

	got, err := catalog.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	want := []catalog.Combo{
		testCombo("combo-1", "Batido + Alfajor", true, drinkItem(), snackItem()),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadFile devolvió %+v, se esperaba %+v", got, want)
	}
}

func TestLoadFileRejectsInvalidCombos(t *testing.T) {
	path := filepath.Join(t.TempDir(), "combos.json")
	raw := `[
		{
			"id": "combo-sin-items",
			"name": "Batido solo",
			"price_cents": 420000,
			"currency": "ARS",
			"active": true,
			"items": []
		}
	]`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("no se pudo escribir el archivo de prueba: %v", err)
	}

	combos, err := catalog.LoadFile(path)
	if err == nil {
		t.Fatalf("LoadFile aceptó un catálogo inválido: %+v", combos)
	}
	if combos != nil {
		t.Fatalf("LoadFile devolvió %+v junto con el error", combos)
	}
	if !strings.Contains(err.Error(), "combo-sin-items") {
		t.Fatalf("el error %q no nombra el combo ofensor", err)
	}
}

func TestExampleFileLoads(t *testing.T) {
	path := filepath.Join("..", "..", "combos.example.json")

	got, err := catalog.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile(%q): %v", path, err)
	}
	if len(got) < 2 {
		t.Fatalf("%s tiene %d combos, se esperaban al menos 2", path, len(got))
	}

	for _, combo := range got {
		machineTypes := make(map[machine.Kind]bool, len(combo.Items))
		for _, item := range combo.Items {
			machineTypes[item.MachineType] = true
		}
		if !machineTypes["batidos"] || !machineTypes["snacks"] {
			t.Fatalf("el combo %q no tiene un ítem de batidos y uno de snacks", combo.ID)
		}
	}
}
