package order_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/machine"
	"compras-remotas/internal/order"
	"compras-remotas/internal/store"
)

func TestEngineDispatchKeepsTheProviderRefOfAnAttemptThatEndsInDoubt(t *testing.T) {
	ctx := context.Background()
	st := newRecordingStore()
	clock := manualClockAt(engineNow)

	driver := &reconcileMachine{
		attempt:     dispenser.DispatchResult{ProviderRef: "code-1"},
		dispatchErr: errors.New("batidos: no se pudo enviar: EOF"),
	}

	engine := order.New(
		st,
		map[machine.Kind]dispenser.Dispenser{"batidos": driver},
		order.WithClock(clock.clock()),
		order.WithIDGenerator(sequentialIDs()),
	)

	placed := place(t, engine, drinkOnlyDraft())

	if err := engine.ConfirmPayment(ctx, placed.ID, engineNow); err != nil {
		t.Fatalf("ConfirmPayment: %v", err)
	}

	line := lineFor(t, mustOrder(t, st, placed.ID), "batidos")
	if line.State != dispenser.LineInDoubt {
		t.Fatalf("la línea quedó en %q, se esperaba %q", line.State, dispenser.LineInDoubt)
	}
	if line.Ref != "code-1" {
		t.Fatalf("el intento en duda perdió el código del proveedor y sólo la reconciliación puede usar ese asa: Ref=%q, se esperaba %q", line.Ref, "code-1")
	}

	clock.set(engineNow.Add(time.Minute))

	if err := engine.ReconcileDue(ctx, 10); err != nil {
		t.Fatalf("ReconcileDue: %v", err)
	}

	if ref := driver.onlyReconcile(t).KnownRef; ref != "code-1" {
		t.Fatalf("la reconciliación consultó el código %q, se esperaba el que dejó el despacho en duda: %q", ref, "code-1")
	}
}

func TestEngineDispatchDoesNotKeepTheProviderRefOfAnEarlierRejectedAttempt(t *testing.T) {
	ctx := context.Background()
	st := newRecordingStore()

	driver := &scriptedMachine{steps: []machineStep{
		{
			result: dispenser.DispatchResult{ProviderRef: "code-A"},
			err:    &dispenser.Rejection{Provider: "batidos", HTTPStatus: 500, Code: 50010, Message: "el canje no se pudo disparar"},
		},
		{result: dispenser.DispatchResult{Accepted: true}},
	}}

	engine := order.New(
		st,
		map[machine.Kind]dispenser.Dispenser{"batidos": driver},
		order.WithClock(frozenClock()),
		order.WithIDGenerator(sequentialIDs()),
	)

	placed := place(t, engine, drinkOnlyDraft())

	if err := engine.ConfirmPayment(ctx, placed.ID, engineNow); err != nil {
		t.Fatalf("ConfirmPayment: %v", err)
	}

	if calls := driver.callCount(); calls != 2 {
		t.Fatalf("la máquina recibió %d despachos, se esperaba el rechazo y su reintento", calls)
	}

	line := lineFor(t, mustOrder(t, st, placed.ID), "batidos")
	if line.State != dispenser.LineSent {
		t.Fatalf("la línea quedó en %q, se esperaba %q", line.State, dispenser.LineSent)
	}
	if line.Ref != "" {
		t.Fatalf("el intento aceptado sin código heredó el asa de una campaña rechazada: Ref=%q, se esperaba vacío", line.Ref)
	}
}

func TestEngineDispatchDoesNotRevertTheLineThatACallbackClosedWhileItWasInFlight(t *testing.T) {
	ctx := context.Background()
	st := newRecordingStore()

	driver := &closingOnDispatchMachine{
		attempt: dispenser.DispatchResult{Accepted: true, ProviderRef: "code-tardio"},
	}

	engine := order.New(
		st,
		map[machine.Kind]dispenser.Dispenser{"batidos": driver},
		order.WithClock(frozenClock()),
		order.WithIDGenerator(sequentialIDs()),
	)

	placed := place(t, engine, drinkOnlyDraft())

	driver.onDispatch = func() {
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

type machineStep struct {
	result dispenser.DispatchResult
	err    error
}

type scriptedMachine struct {
	mu    sync.Mutex
	steps []machineStep
	calls int
}

func (m *scriptedMachine) Check(ctx context.Context, machine dispenser.MachineRef) (dispenser.Availability, error) {
	return dispenser.Availability{Available: true}, nil
}

func (m *scriptedMachine) Dispatch(ctx context.Context, req dispenser.Request) (dispenser.DispatchResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	index := m.calls
	m.calls++

	if index >= len(m.steps) {
		index = len(m.steps) - 1
	}

	return m.steps[index].result, m.steps[index].err
}

func (m *scriptedMachine) Reconcile(ctx context.Context, req dispenser.Request) (dispenser.Status, error) {
	return dispenser.Status{}, nil
}

func (m *scriptedMachine) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.calls
}
