package order_test

import (
	"context"
	"testing"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/order"
	"compras-remotas/internal/store"
)

func TestEngineDispatchDoesNotRevertTheLineThatACallbackClosedWhileItWasInFlight(t *testing.T) {
	ctx := context.Background()
	st := newRecordingStore()

	machine := &closingOnDispatchMachine{
		attempt: dispenser.DispatchResult{Accepted: true, ProviderRef: "code-tardio"},
	}

	engine := order.New(
		st,
		map[string]dispenser.Dispenser{"batidos": machine},
		order.WithClock(frozenClock()),
		order.WithIDGenerator(sequentialIDs()),
	)

	placed := place(t, engine, drinkOnlyDraft())

	machine.onDispatch = func() {
		duplicate, err := engine.CloseFromCallback(ctx, order.Callback{
			Source:      "batidos",
			DedupeKey:   "batidos-1",
			OrderID:     placed.ID,
			MachineType: "batidos",
			State:       order.CallbackDelivered,
		})
		if err != nil {
			t.Errorf("el callback no debía fallar: %v", err)
		}
		if duplicate {
			t.Errorf("el primer callback no es un duplicado")
		}
	}

	if err := engine.ConfirmPayment(ctx, placed.ID, engineNow); err != nil {
		t.Fatalf("una línea ya resuelta por el callback no debe volver al despacho: %v", err)
	}

	line := lineFor(t, mustOrder(t, st, placed.ID), "batidos")
	if line.State != dispenser.LineDelivered {
		t.Fatalf("el despacho tardío revirtió la línea: State=%q, se esperaba %q", line.State, dispenser.LineDelivered)
	}
	if line.Ref != "" {
		t.Fatalf("el despacho tardío adoptó una referencia sobre una línea ya cerrada: Ref=%q", line.Ref)
	}
	if !line.DeadlineAt.IsZero() || !line.ReconcileUntil.IsZero() {
		t.Fatalf("el despacho tardío rearmó los plazos de una línea cerrada: deadline=%v until=%v", line.DeadlineAt, line.ReconcileUntil)
	}

	if got := mustOrder(t, st, placed.ID); got.Status != store.OrderDelivered {
		t.Fatalf("Status=%q, se esperaba %q", got.Status, store.OrderDelivered)
	}
}
