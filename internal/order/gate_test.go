package order_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/machine"
	"compras-remotas/internal/order"
	"compras-remotas/internal/store"
)

type stubGate struct {
	mu    sync.Mutex
	calls []order.Draft
	err   error
}

func (g *stubGate) Check(ctx context.Context, draft order.Draft) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.calls = append(g.calls, draft)

	return g.err
}

func (g *stubGate) callCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()

	return len(g.calls)
}

func (g *stubGate) onlyCall(t *testing.T) order.Draft {
	t.Helper()

	g.mu.Lock()
	defer g.mu.Unlock()

	if len(g.calls) != 1 {
		t.Fatalf("la puerta recibió %d consultas, se esperaba 1", len(g.calls))
	}

	return g.calls[0]
}

func gatedEngine(st store.Store, g order.Gate) *order.Engine {
	return order.New(
		st,
		map[machine.Kind]dispenser.Dispenser{"batidos": &stubMachine{}, "snacks": &stubMachine{}},
		order.WithClock(frozenClock()),
		order.WithIDGenerator(sequentialIDs()),
		order.WithGate(g),
	)
}

func TestEnginePlaceAsksTheGateForANewOrder(t *testing.T) {
	st := newRecordingStore()
	gate := &stubGate{}
	engine := gatedEngine(st, gate)

	draft := comboDraft()
	place(t, engine, draft)

	checked := gate.onlyCall(t)

	if checked.KioskID != draft.KioskID || checked.ComboID != draft.ComboID {
		t.Fatalf("la puerta recibió %+v, se esperaba %+v", checked, draft)
	}
	if len(checked.Items) != len(draft.Items) {
		t.Fatalf("la puerta recibió %d ítems, se esperaban %d", len(checked.Items), len(draft.Items))
	}
}

func TestEnginePlaceDoesNotAskTheGateForAnExistingClientKey(t *testing.T) {
	st := newRecordingStore()
	gate := &stubGate{}
	engine := gatedEngine(st, gate)

	draft := comboDraft()
	draft.ClientKey = "tap-1"

	first := place(t, engine, draft)
	second := place(t, engine, draft)

	if first.ID != second.ID {
		t.Fatalf("la misma clave devolvió dos órdenes: %q y %q", first.ID, second.ID)
	}
	if gate.callCount() != 1 {
		t.Fatalf("la puerta se consultó %d veces, se esperaba 1: un reintento idempotente no se vuelve a chequear", gate.callCount())
	}
}

func TestEnginePlaceCreatesNothingWhenTheGateRejects(t *testing.T) {
	st := newRecordingStore()
	gate := &stubGate{err: &order.NotSellableError{Reasons: []string{"batidos_device_offline"}}}
	engine := gatedEngine(st, gate)

	_, err := engine.Place(context.Background(), comboDraft())
	if err == nil {
		t.Fatal("se esperaba un error de la puerta y llegó nil")
	}

	if !errors.Is(err, order.ErrNotSellable) {
		t.Fatalf("errors.Is(err, ErrNotSellable)=false para %v", err)
	}
	if created := st.createdIDs(); len(created) != 0 {
		t.Fatalf("la puerta bloqueó y se crearon %d órdenes: %v", len(created), created)
	}
}

func TestEnginePlacePropagatesAGateErrorWithoutCreatingAnOrder(t *testing.T) {
	st := newRecordingStore()
	caido := errors.New("el proveedor de snacks no responde")
	engine := gatedEngine(st, &stubGate{err: caido})

	_, err := engine.Place(context.Background(), comboDraft())
	if !errors.Is(err, caido) {
		t.Fatalf("se esperaba el error del proveedor y llegó %v", err)
	}
	if errors.Is(err, order.ErrNotSellable) {
		t.Fatalf("un fallo del proveedor no es un combo no vendible: %v", err)
	}
	if created := st.createdIDs(); len(created) != 0 {
		t.Fatalf("la puerta falló y se crearon %d órdenes: %v", len(created), created)
	}
}

func TestEnginePlaceWithANilGatePlacesTheOrder(t *testing.T) {
	st := newRecordingStore()
	engine := gatedEngine(st, nil)

	placed := place(t, engine, comboDraft())

	if len(placed.Lines) != 2 {
		t.Fatalf("la orden tiene %d líneas, se esperaban 2", len(placed.Lines))
	}
	if created := st.createdIDs(); len(created) != 1 {
		t.Fatalf("se crearon %d órdenes, se esperaba 1", len(created))
	}
}

func TestNotSellableErrorIsErrNotSellable(t *testing.T) {
	err := error(&order.NotSellableError{Reasons: []string{"batidos_device_offline", "snacks_device_out_of_stock"}})

	if !errors.Is(err, order.ErrNotSellable) {
		t.Fatalf("errors.Is(err, ErrNotSellable)=false para %v", err)
	}

	message := err.Error()
	if !strings.Contains(message, "el combo no se puede vender ahora") {
		t.Fatalf("el mensaje no explica el bloqueo: %q", message)
	}
	for _, reason := range err.(*order.NotSellableError).Reasons {
		if !strings.Contains(message, reason) {
			t.Fatalf("el mensaje no incluye la razón %q: %q", reason, message)
		}
	}
}

func TestNotSellableErrorWithoutReasonsStillExplainsTheBlock(t *testing.T) {
	err := error(&order.NotSellableError{})

	if !errors.Is(err, order.ErrNotSellable) {
		t.Fatalf("errors.Is(err, ErrNotSellable)=false para %v", err)
	}
	if err.Error() != "el combo no se puede vender ahora" {
		t.Fatalf("Error()=%q", err.Error())
	}
}

func TestEnginePlaceDoesNotAskTheGateForAnInvalidDraft(t *testing.T) {
	st := newRecordingStore()
	gate := &stubGate{}
	engine := gatedEngine(st, gate)

	invalid := comboDraft()
	invalid.Items = nil

	if _, err := engine.Place(context.Background(), invalid); err == nil {
		t.Fatal("se esperaba el rechazo del draft y llegó nil")
	}

	if gate.callCount() != 0 {
		t.Fatalf("la puerta se consultó %d veces con un draft inválido", gate.callCount())
	}
}

func TestEnginePlaceDoesNotConsumeIDsWhenTheGateRejects(t *testing.T) {
	st := newRecordingStore()

	generated := 0
	engine := order.New(
		st,
		map[machine.Kind]dispenser.Dispenser{"batidos": &stubMachine{}, "snacks": &stubMachine{}},
		order.WithClock(frozenClock()),
		order.WithIDGenerator(func() string {
			generated++
			return fmt.Sprintf("id-%d", generated)
		}),
		order.WithGate(&stubGate{err: &order.NotSellableError{Reasons: []string{"snacks_device_offline"}}}),
	)

	if _, err := engine.Place(context.Background(), comboDraft()); !errors.Is(err, order.ErrNotSellable) {
		t.Fatalf("se esperaba ErrNotSellable y llegó %v", err)
	}

	if generated != 0 {
		t.Fatalf("la puerta bloqueó y se generaron %d identificadores", generated)
	}
	if created := st.createdIDs(); len(created) != 0 {
		t.Fatalf("la puerta bloqueó y se crearon %v", created)
	}
}
