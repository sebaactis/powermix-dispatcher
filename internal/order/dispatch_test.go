package order_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/linestate"
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
	if line.State != linestate.InDoubt {
		t.Fatalf("la línea quedó en %q, se esperaba %q", line.State, linestate.InDoubt)
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
	if line.State != linestate.Sent {
		t.Fatalf("la línea quedó en %q, se esperaba %q", line.State, linestate.Sent)
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
	if line.State != linestate.Delivered {
		t.Fatalf("el despacho tardío revirtió la línea: State=%q, se esperaba %q", line.State, linestate.Delivered)
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

func TestEngineDispatchClaimsTheLineBeforeCallingTheMachine(t *testing.T) {
	ctx := context.Background()
	st := newRecordingStore()

	driver := &closingOnDispatchMachine{attempt: dispenser.DispatchResult{Accepted: true}}
	driver.onDispatch = func() { st.record("dispatch") }

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

	events := st.recordedEvents()
	if len(events) != 4 {
		t.Fatalf("el despacho dejó %d eventos, se esperaban 4 (reclamo, intento, despacho, resultado): %+v", len(events), events)
	}

	claim := events[0]
	if claim.Kind != "saveLine" {
		t.Fatalf("el primer evento fue %q, se esperaba el reclamo de la línea antes de registrar el intento", claim.Kind)
	}
	if claim.State != linestate.Pending {
		t.Fatalf("el reclamo guardó la línea en %q, se esperaba %q: el despacho todavía no ocurrió", claim.State, linestate.Pending)
	}
	if want := engineNow.Add(2 * time.Minute); !claim.Deadline.Equal(want) {
		t.Fatalf("el reclamo guardó DeadlineAt=%v, se esperaba %v", claim.Deadline, want)
	}
	if want := engineNow.Add(4 * time.Minute); !claim.Until.Equal(want) {
		t.Fatalf("el reclamo guardó ReconcileUntil=%v, se esperaba %v", claim.Until, want)
	}

	if events[1].Kind != "appendAttempt" {
		t.Fatalf("el segundo evento fue %q, se esperaba el intento registrado después del reclamo", events[1].Kind)
	}
	if events[2].Kind != "dispatch" {
		t.Fatalf("el tercer evento fue %q, se esperaba la llamada a la máquina después del intento", events[2].Kind)
	}
	if events[3].Kind != "saveLine" || events[3].State != linestate.Sent {
		t.Fatalf("el último evento fue %q en el estado %q, se esperaba el resultado %q", events[3].Kind, events[3].State, linestate.Sent)
	}
}

func TestEngineDispatchClearsTheClaimDeadlinesOnAFinalRejection(t *testing.T) {
	ctx := context.Background()
	st := newRecordingStore()

	driver := &scriptedMachine{steps: []machineStep{
		{err: &dispenser.Rejection{Provider: "batidos", HTTPStatus: 400, Code: 40005, Message: "sin stock"}},
	}}

	engine := order.New(
		st,
		map[machine.Kind]dispenser.Dispenser{"batidos": driver},
		order.WithClock(frozenClock()),
		order.WithIDGenerator(sequentialIDs()),
		order.WithMaxAttempts(1),
	)

	placed := place(t, engine, drinkOnlyDraft())

	if err := engine.ConfirmPayment(ctx, placed.ID, engineNow); err != nil {
		t.Fatalf("ConfirmPayment: %v", err)
	}

	line := lineFor(t, mustOrder(t, st, placed.ID), "batidos")
	if line.State != linestate.Failed {
		t.Fatalf("la línea quedó en %q, se esperaba %q: sin intentos restantes el rechazo es final", line.State, linestate.Failed)
	}
	if !line.DeadlineAt.IsZero() || !line.ReconcileUntil.IsZero() {
		t.Fatalf("el rechazo final conservó los plazos del reclamo: deadline=%v until=%v, se esperaban los dos en cero", line.DeadlineAt, line.ReconcileUntil)
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

func (m *scriptedMachine) Check(ctx context.Context, machine machine.Ref) (dispenser.Availability, error) {
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
