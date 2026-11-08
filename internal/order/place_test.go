package order_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/machine"
	"compras-remotas/internal/order"
	"compras-remotas/internal/store"
)

func TestEnginePlaceCreatesOneLinePerMachine(t *testing.T) {
	st := store.NewMemory()
	engine := order.New(
		st,
		map[machine.Kind]dispenser.Dispenser{"batidos": &stubMachine{}, "snacks": &stubMachine{}},
		order.WithClock(frozenClock()),
		order.WithIDGenerator(sequentialIDs()),
	)

	draft := comboDraft()
	placed := place(t, engine, draft)

	if placed.Status != store.OrderPendingPayment {
		t.Fatalf("Status=%q, se esperaba %q", placed.Status, store.OrderPendingPayment)
	}
	if !placed.CreatedAt.Equal(engineNow) {
		t.Fatalf("CreatedAt=%v, se esperaba %v", placed.CreatedAt, engineNow)
	}
	if !placed.PaidAt.IsZero() {
		t.Fatalf("PaidAt=%v, se esperaba vacío", placed.PaidAt)
	}
	if placed.KioskID != draft.KioskID || placed.ComboID != draft.ComboID {
		t.Fatalf("la orden no copió el mueble y el combo: %+v", placed)
	}
	if placed.AmountCents != draft.AmountCents || placed.Currency != draft.Currency {
		t.Fatalf("la orden no copió el importe y la moneda: %+v", placed)
	}
	if placed.ID == "" {
		t.Fatal("la orden quedó sin id")
	}

	if len(placed.Lines) != 2 {
		t.Fatalf("la orden tiene %d líneas, se esperaban 2", len(placed.Lines))
	}
	if placed.Lines[0].ID == "" || placed.Lines[0].ID == placed.Lines[1].ID {
		t.Fatalf("las líneas quedaron con ids vacíos o repetidos: %q, %q", placed.Lines[0].ID, placed.Lines[1].ID)
	}

	for i, want := range draft.Items {
		line := placed.Lines[i]

		if line.OrderID != placed.ID {
			t.Fatalf("la línea %d apunta a la orden %q, se esperaba %q", i+1, line.OrderID, placed.ID)
		}
		if line.Position != i+1 {
			t.Fatalf("la línea %d tiene Position=%d", i+1, line.Position)
		}
		if line.MachineType != want.Machine.Type {
			t.Fatalf("la línea %d tiene MachineType=%q, se esperaba %q", i+1, line.MachineType, want.Machine.Type)
		}
		if line.SKU != want.SKU {
			t.Fatalf("la línea %d tiene SKU=%q, se esperaba %q", i+1, line.SKU, want.SKU)
		}
		if line.AmountCents != want.AmountCents {
			t.Fatalf("la línea %d tiene AmountCents=%d, se esperaba %d", i+1, line.AmountCents, want.AmountCents)
		}
		if line.State != dispenser.LinePending {
			t.Fatalf("la línea %d nace en %q, se esperaba %q", i+1, line.State, dispenser.LinePending)
		}
		if line.Attempts != 0 {
			t.Fatalf("la línea %d nace con %d intentos", i+1, line.Attempts)
		}
		if line.Ref != "" || line.LastError != "" || !line.DeadlineAt.IsZero() {
			t.Fatalf("la línea %d nace con datos de despacho: %+v", i+1, line)
		}
	}

	stored := mustOrder(t, st, placed.ID)
	if !reflect.DeepEqual(stored, placed) {
		t.Fatalf("la orden guardada es %+v, se esperaba %+v", stored, placed)
	}
}

func TestEnginePlaceRejectsAnInvalidDraftAndCreatesNothing(t *testing.T) {
	cases := []struct {
		name         string
		mutate       func(*order.Draft)
		wantPosition string
	}{
		{
			name:   "sin ítems",
			mutate: func(draft *order.Draft) { draft.Items = nil },
		},
		{
			name:         "sin tipo de máquina",
			mutate:       func(draft *order.Draft) { draft.Items[0].Machine.Type = "" },
			wantPosition: "ítem 1",
		},
		{
			name:         "con un tipo de máquina desconocido",
			mutate:       func(draft *order.Draft) { draft.Items[1].Machine.Type = "cafetera" },
			wantPosition: "ítem 2",
		},
		{
			name:         "sin SKU",
			mutate:       func(draft *order.Draft) { draft.Items[1].SKU = "" },
			wantPosition: "ítem 2",
		},
		{
			name: "sin identificador de máquina",
			mutate: func(draft *order.Draft) {
				draft.Items[0].Machine = dispenser.MachineRef{KioskID: "kiosk-1", Type: "batidos"}
			},
			wantPosition: "ítem 1",
		},
		{
			name:   "con importe de orden negativo",
			mutate: func(draft *order.Draft) { draft.AmountCents = -1 },
		},
		{
			name:         "con importe de ítem negativo",
			mutate:       func(draft *order.Draft) { draft.Items[1].AmountCents = -1 },
			wantPosition: "ítem 2",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newRecordingStore()
			engine := order.New(
				st,
				map[machine.Kind]dispenser.Dispenser{"batidos": &stubMachine{}, "snacks": &stubMachine{}},
				order.WithClock(frozenClock()),
				order.WithIDGenerator(sequentialIDs()),
			)

			draft := comboDraft()
			tc.mutate(&draft)

			placed, err := engine.Place(context.Background(), draft)
			if err == nil {
				t.Fatalf("Place aceptó un borrador inválido y devolvió %+v", placed)
			}
			if !reflect.DeepEqual(placed, store.Order{}) {
				t.Fatalf("Place devolvió %+v junto al error", placed)
			}
			if tc.wantPosition != "" && !strings.Contains(err.Error(), tc.wantPosition) {
				t.Fatalf("el error %q no nombra %q", err.Error(), tc.wantPosition)
			}

			if created := st.createdIDs(); len(created) != 0 {
				t.Fatalf("Place creó %v con un borrador inválido", created)
			}
		})
	}
}

func TestEnginePlaceRejectsAnItemWithoutAConfiguredMachineType(t *testing.T) {
	cases := []struct {
		name         string
		machineType  machine.Kind
		wantPosition string
	}{
		{name: "con el tipo de máquina vacío", machineType: "", wantPosition: "ítem 1"},
		{name: "con un tipo de máquina no configurado", machineType: "cafetera", wantPosition: "ítem 1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newRecordingStore()
			engine := order.New(
				st,
				map[machine.Kind]dispenser.Dispenser{"batidos": &stubMachine{}, "snacks": &stubMachine{}},
				order.WithClock(frozenClock()),
				order.WithIDGenerator(sequentialIDs()),
			)

			draft := comboDraft()
			draft.Items[0].Machine.Type = tc.machineType

			placed, err := engine.Place(context.Background(), draft)
			if err == nil {
				t.Fatalf("Place aceptó un ítem con Machine.Type=%q y devolvió %+v", tc.machineType, placed)
			}
			if !reflect.DeepEqual(placed, store.Order{}) {
				t.Fatalf("Place devolvió %+v junto al error", placed)
			}
			if !strings.Contains(err.Error(), tc.wantPosition) {
				t.Fatalf("el error %q no nombra %q", err.Error(), tc.wantPosition)
			}

			if created := st.createdIDs(); len(created) != 0 {
				t.Fatalf("Place creó %v con un ítem sin tipo de máquina válido", created)
			}
		})
	}
}

func TestEnginePlaceTakesTheLineMachineTypeFromTheMachineRef(t *testing.T) {
	st := newRecordingStore()
	machines := map[machine.Kind]dispenser.Dispenser{"batidos": &stubMachine{}, "snacks": &stubMachine{}}
	engine := order.New(
		st,
		machines,
		order.WithClock(frozenClock()),
		order.WithIDGenerator(sequentialIDs()),
	)

	draft := comboDraft()
	placed := place(t, engine, draft)

	stored := mustOrder(t, st, placed.ID)
	if len(stored.Lines) != len(draft.Items) {
		t.Fatalf("la orden guardada tiene %d líneas, se esperaban %d", len(stored.Lines), len(draft.Items))
	}

	for i, item := range draft.Items {
		line := stored.Lines[i]

		if line.MachineType != item.Machine.Type {
			t.Fatalf("la línea %d se guardó con MachineType=%q, se esperaba el tipo de la máquina %q", i+1, line.MachineType, item.Machine.Type)
		}
		if !reflect.DeepEqual(line.Machine, item.Machine) {
			t.Fatalf("la línea %d se guardó con la máquina %+v, se esperaba %+v", i+1, line.Machine, item.Machine)
		}
		if _, ok := machines[line.MachineType]; !ok {
			t.Fatalf("la línea %d se guardó con un tipo sin driver: %q", i+1, line.MachineType)
		}
	}
}

func TestEnginePlaceIsIdempotentByClientKey(t *testing.T) {
	st := newRecordingStore()
	engine := order.New(
		st,
		map[machine.Kind]dispenser.Dispenser{
			"batidos": &stubMachine{attempt: dispenser.DispatchResult{Accepted: true}},
			"snacks":  &stubMachine{attempt: dispenser.DispatchResult{Accepted: true}},
		},
		order.WithClock(frozenClock()),
		order.WithIDGenerator(sequentialIDs()),
	)

	draft := comboDraft()
	draft.ClientKey = "tap-1"

	first := place(t, engine, draft)
	second := place(t, engine, draft)

	if first.ID != second.ID {
		t.Fatalf("la misma clave creó dos órdenes: %q y %q", first.ID, second.ID)
	}

	other := comboDraft()
	other.ClientKey = "tap-2"
	third := place(t, engine, other)

	if third.ID == first.ID {
		t.Fatalf("dos claves distintas devolvieron la misma orden %q", third.ID)
	}
}
