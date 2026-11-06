package order_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/order"
	"compras-remotas/internal/store"
)

type reconcileStub struct {
	status dispenser.Status
	err    error

	calls []dispenser.Request
}

func (s *reconcileStub) Check(ctx context.Context, machine dispenser.MachineRef) (dispenser.Availability, error) {
	return dispenser.Availability{Available: true}, nil
}

func (s *reconcileStub) Dispatch(ctx context.Context, req dispenser.Request) (dispenser.DispatchResult, error) {
	return dispenser.DispatchResult{Accepted: true, ProviderRef: req.SKU}, nil
}

func (s *reconcileStub) Reconcile(ctx context.Context, req dispenser.Request) (dispenser.Status, error) {
	s.calls = append(s.calls, req)

	return s.status, s.err
}

type dispatchedOrder struct {
	engine    *order.Engine
	orders    *store.Memory
	orderID   string
	batidos   *stubMachine
	snacks    *stubMachine
	reconcile map[string]*reconcileStub
}

func dispatched(t *testing.T) dispatchedOrder {
	t.Helper()

	return dispatchedWith(t, comboDraft(), "code-batidos", "sub-snacks")
}

func dispatchedWithoutProviderRefs(t *testing.T, draft order.Draft) dispatchedOrder {
	t.Helper()

	return dispatchedWith(t, draft, "", "")
}

func twoSnackLinesDraft() order.Draft {
	draft := comboDraft()
	draft.Items = []order.Item{
		draft.Items[1],
		{
			SKU:         "sku-snack-2",
			AmountCents: 1000,
			Machine: dispenser.MachineRef{
				KioskID: "kiosk-1",
				Type:    "snacks",
				Name:    "Snacks Powermix",
				MID:     "mid-1",
			},
		},
	}

	return draft
}

func dispatchedWith(t *testing.T, draft order.Draft, batidosRef, snacksRef string) dispatchedOrder {
	t.Helper()

	orders := store.NewMemory()

	batidos := &stubMachine{attempt: dispenser.DispatchResult{Accepted: true, ProviderRef: batidosRef}}
	snacks := &stubMachine{attempt: dispenser.DispatchResult{Accepted: true, ProviderRef: snacksRef}}

	batidosReconcile := &reconcileStub{}
	snacksReconcile := &reconcileStub{}

	engine := order.New(orders, map[string]dispenser.Dispenser{
		"batidos": reconcilingMachine{dispatch: batidos, reconcile: batidosReconcile},
		"snacks":  reconcilingMachine{dispatch: snacks, reconcile: snacksReconcile},
	}, order.WithClock(frozenClock()), order.WithIDGenerator(sequentialIDs()))

	placed := place(t, engine, draft)

	if err := engine.ConfirmPayment(context.Background(), placed.ID, engineNow); err != nil {
		t.Fatalf("la confirmación de pago no debía fallar: %v", err)
	}

	return dispatchedOrder{
		engine:    engine,
		orders:    orders,
		orderID:   placed.ID,
		batidos:   batidos,
		snacks:    snacks,
		reconcile: map[string]*reconcileStub{"batidos": batidosReconcile, "snacks": snacksReconcile},
	}
}

type reconcilingMachine struct {
	dispatch  dispenser.Dispenser
	reconcile dispenser.Dispenser
}

func (m reconcilingMachine) Check(ctx context.Context, machine dispenser.MachineRef) (dispenser.Availability, error) {
	return m.dispatch.Check(ctx, machine)
}

func (m reconcilingMachine) Dispatch(ctx context.Context, req dispenser.Request) (dispenser.DispatchResult, error) {
	return m.dispatch.Dispatch(ctx, req)
}

func (m reconcilingMachine) Reconcile(ctx context.Context, req dispenser.Request) (dispenser.Status, error) {
	return m.reconcile.Reconcile(ctx, req)
}

func TestCloseFromCallbackDeliversTheLine(t *testing.T) {
	fx := dispatched(t)

	duplicate, err := fx.engine.CloseFromCallback(context.Background(), order.Callback{
		Source:    "snacks",
		DedupeKey: "snacks-1",
		OrderID:   fx.orderID,
		Ref:       "sub-snacks",
		State:     order.CallbackDelivered,
	})
	if err != nil {
		t.Fatalf("el callback no debía fallar: %v", err)
	}

	if duplicate {
		t.Fatalf("el primer callback debe procesarse")
	}

	loaded := mustOrder(t, fx.orders, fx.orderID)
	snacks := lineFor(t, loaded, "snacks")

	if snacks.State != dispenser.LineDelivered {
		t.Fatalf("la línea de snacks debía quedar entregada y quedó %q", snacks.State)
	}

	if snacks.LastError != "" {
		t.Fatalf("una línea entregada no debe arrastrar error: %q", snacks.LastError)
	}

	batidos := lineFor(t, loaded, "batidos")
	if batidos.State == dispenser.LineDelivered {
		t.Fatalf("la línea de batidos no recibió callback y no debe cerrarse")
	}

	if loaded.Status != store.OrderDispatching {
		t.Fatalf("con una línea abierta la orden sigue en curso y quedó %q", loaded.Status)
	}
}

func TestCloseFromCallbackClosesTheOrderWhenBothLinesArrive(t *testing.T) {
	fx := dispatched(t)

	for _, event := range []order.Callback{
		{Source: "batidos", DedupeKey: "batidos-1", OrderID: fx.orderID, Ref: "code-batidos", State: order.CallbackDelivered},
		{Source: "snacks", DedupeKey: "snacks-1", OrderID: fx.orderID, Ref: "sub-snacks", State: order.CallbackDelivered},
	} {
		if _, err := fx.engine.CloseFromCallback(context.Background(), event); err != nil {
			t.Fatalf("el callback de %s no debía fallar: %v", event.Source, err)
		}
	}

	loaded := mustOrder(t, fx.orders, fx.orderID)
	if loaded.Status != store.OrderDelivered {
		t.Fatalf("con las dos líneas entregadas la orden queda entregada y quedó %q", loaded.Status)
	}

	for _, line := range loaded.Lines {
		if !line.DeadlineAt.IsZero() || !line.ReconcileUntil.IsZero() {
			t.Fatalf("una línea cerrada no debe conservar plazos: %+v", line)
		}
	}
}

func TestCloseFromCallbackMarksTheLineFailedWithTheProviderReason(t *testing.T) {
	fx := dispatched(t)

	if _, err := fx.engine.CloseFromCallback(context.Background(), order.Callback{
		Source:    "snacks",
		DedupeKey: "snacks-1",
		OrderID:   fx.orderID,
		Ref:       "sub-snacks",
		State:     order.CallbackFailed,
		Reason:    "el canal no entregó el producto",
	}); err != nil {
		t.Fatalf("el callback no debía fallar: %v", err)
	}

	loaded := mustOrder(t, fx.orders, fx.orderID)
	snacks := lineFor(t, loaded, "snacks")

	if snacks.State != dispenser.LineFailed {
		t.Fatalf("la línea debía quedar fallida y quedó %q", snacks.State)
	}

	if snacks.LastError != "el canal no entregó el producto" {
		t.Fatalf("el motivo del proveedor debe quedar registrado y quedó %q", snacks.LastError)
	}
}

func TestCloseFromCallbackIsIdempotentBySourceAndKey(t *testing.T) {
	fx := dispatched(t)

	event := order.Callback{
		Source:    "snacks",
		DedupeKey: "snacks-1",
		OrderID:   fx.orderID,
		Ref:       "sub-snacks",
		State:     order.CallbackDelivered,
	}

	if _, err := fx.engine.CloseFromCallback(context.Background(), event); err != nil {
		t.Fatalf("el primer callback no debía fallar: %v", err)
	}

	repeated := event
	repeated.State = order.CallbackFailed
	repeated.Reason = "llego tarde"

	duplicate, err := fx.engine.CloseFromCallback(context.Background(), repeated)
	if err != nil {
		t.Fatalf("un reintento del mismo callback no debe devolver error: %v", err)
	}

	if !duplicate {
		t.Fatalf("un reintento del mismo callback debe informarse como duplicado y no procesarse de nuevo")
	}

	snacks := lineFor(t, mustOrder(t, fx.orders, fx.orderID), "snacks")
	if snacks.State != dispenser.LineDelivered {
		t.Fatalf("el reintento no debe cambiar el estado ya resuelto y quedó %q", snacks.State)
	}

	if snacks.LastError != "" {
		t.Fatalf("el reintento no debe escribir un motivo sobre una línea entregada: %q", snacks.LastError)
	}
}

func TestCloseFromCallbackRejectsAReferenceThatIsNotInTheOrder(t *testing.T) {
	fx := dispatched(t)

	duplicate, err := fx.engine.CloseFromCallback(context.Background(), order.Callback{
		Source:    "snacks",
		DedupeKey: "snacks-1",
		OrderID:   fx.orderID,
		Ref:       "sub-de-otra-orden",
		State:     order.CallbackDelivered,
	})

	if !errors.Is(err, order.ErrCallbackUnknownLine) {
		t.Fatalf("se esperaba ErrCallbackUnknownLine y llegó %v", err)
	}

	if duplicate {
		t.Fatalf("el callback se registró como visto aunque no hubiera línea que cerrar, y un aviso nuevo no es un duplicado")
	}

	snacks := lineFor(t, mustOrder(t, fx.orders, fx.orderID), "snacks")
	if snacks.State == dispenser.LineDelivered {
		t.Fatalf("una referencia desconocida no debe cerrar ninguna línea")
	}
}

func TestCloseFromCallbackRejectsAnIncompleteEvent(t *testing.T) {
	fx := dispatched(t)

	cases := []struct {
		name  string
		event order.Callback
	}{
		{name: "sin origen", event: order.Callback{DedupeKey: "k", OrderID: fx.orderID, Ref: "sub-snacks", State: order.CallbackDelivered}},
		{name: "sin clave", event: order.Callback{Source: "snacks", OrderID: fx.orderID, Ref: "sub-snacks", State: order.CallbackDelivered}},
		{name: "sin orden", event: order.Callback{Source: "snacks", DedupeKey: "k", Ref: "sub-snacks", State: order.CallbackDelivered}},
		{name: "sin estado", event: order.Callback{Source: "snacks", DedupeKey: "k", OrderID: fx.orderID, Ref: "sub-snacks"}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			duplicate, err := fx.engine.CloseFromCallback(context.Background(), testCase.event)
			if !errors.Is(err, order.ErrCallbackIncomplete) {
				t.Fatalf("se esperaba ErrCallbackIncomplete y llegó %v", err)
			}

			if duplicate {
				t.Fatalf("un callback incompleto no debe registrarse como visto ni como duplicado")
			}
		})
	}
}

func TestCloseFromCallbackRejectsAnUnknownOrder(t *testing.T) {
	fx := dispatched(t)

	_, err := fx.engine.CloseFromCallback(context.Background(), order.Callback{
		Source:    "snacks",
		DedupeKey: "snacks-1",
		OrderID:   "orden-que-no-existe",
		Ref:       "sub-snacks",
		State:     order.CallbackDelivered,
	})

	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("se esperaba store.ErrNotFound y llegó %v", err)
	}
}

func TestConfirmFromCallbackAsksTheProviderInsteadOfTrustingTheCallback(t *testing.T) {
	fx := dispatched(t)

	fx.reconcile["batidos"].status = dispenser.Status{State: dispenser.LineDelivered}

	duplicate, err := fx.engine.ConfirmFromCallback(context.Background(), order.Callback{
		Source:    "batidos",
		DedupeKey: "batidos-1",
		OrderID:   fx.orderID,
		Ref:       "code-batidos",
		State:     order.CallbackFailed,
		Reason:    "el callback dice fallo",
	})
	if err != nil {
		t.Fatalf("la confirmación no debía fallar: %v", err)
	}

	if duplicate {
		t.Fatalf("el primer callback debe procesarse")
	}

	if len(fx.reconcile["batidos"].calls) != 1 {
		t.Fatalf("se debía consultar al proveedor una vez y se consultó %d", len(fx.reconcile["batidos"].calls))
	}

	asked := fx.reconcile["batidos"].calls[0]
	if asked.KnownRef != "code-batidos" || asked.OrderID != fx.orderID {
		t.Fatalf("la consulta debe llevar el código y la orden: %+v", asked)
	}

	batidos := lineFor(t, mustOrder(t, fx.orders, fx.orderID), "batidos")
	if batidos.State != dispenser.LineDelivered {
		t.Fatalf("el resultado lo decide el proveedor: debía quedar entregada y quedó %q", batidos.State)
	}

	if batidos.LastError != "" {
		t.Fatalf("no se debe guardar el motivo del callback cuando el proveedor confirma la entrega: %q", batidos.LastError)
	}
}

func TestConfirmFromCallbackKeepsWaitingWhenTheProviderIsStillWorking(t *testing.T) {
	fx := dispatched(t)

	fx.reconcile["batidos"].status = dispenser.Status{State: dispenser.LineBrewing, Reason: "todavía preparando"}

	duplicate, err := fx.engine.ConfirmFromCallback(context.Background(), order.Callback{
		Source:    "batidos",
		DedupeKey: "batidos-1",
		OrderID:   fx.orderID,
		Ref:       "code-batidos",
		State:     order.CallbackProgress,
	})
	if err != nil {
		t.Fatalf("la confirmación no debía fallar: %v", err)
	}

	if duplicate {
		t.Fatalf("el primer callback debe procesarse")
	}

	loaded := mustOrder(t, fx.orders, fx.orderID)
	batidos := lineFor(t, loaded, "batidos")

	if batidos.State != dispenser.LineBrewing {
		t.Fatalf("mientras el proveedor prepara la línea queda en elaboración y quedó %q", batidos.State)
	}

	if batidos.DeadlineAt.IsZero() {
		t.Fatalf("una línea en elaboración debe conservar un plazo para volver a preguntar")
	}

	if loaded.Status != store.OrderDispatching {
		t.Fatalf("la orden sigue en curso y quedó %q", loaded.Status)
	}
}

func TestCloseFromCallbackDoesNotTouchAClosedLine(t *testing.T) {
	fx := dispatched(t)

	if _, err := fx.engine.CloseFromCallback(context.Background(), order.Callback{
		Source:    "snacks",
		DedupeKey: "snacks-1",
		OrderID:   fx.orderID,
		Ref:       "sub-snacks",
		State:     order.CallbackDelivered,
	}); err != nil {
		t.Fatalf("el primer callback no debía fallar: %v", err)
	}

	if _, err := fx.engine.CloseFromCallback(context.Background(), order.Callback{
		Source:    "snacks",
		DedupeKey: "snacks-2",
		OrderID:   fx.orderID,
		Ref:       "sub-snacks",
		State:     order.CallbackFailed,
		Reason:    "el canal no entregó el producto",
	}); err != nil {
		t.Fatalf("un callback sobre una línea ya cerrada no debe devolver error: %v", err)
	}

	snacks := lineFor(t, mustOrder(t, fx.orders, fx.orderID), "snacks")

	if snacks.State != dispenser.LineDelivered {
		t.Fatalf("una línea cerrada no se reabre ni se degrada y quedó %q", snacks.State)
	}

	if snacks.LastError != "" {
		t.Fatalf("una línea entregada no debe arrastrar el motivo de un callback tardío: %q", snacks.LastError)
	}
}

func TestConfirmFromCallbackDoesNotReopenAClosedLine(t *testing.T) {
	fx := dispatched(t)

	if _, err := fx.engine.CloseFromCallback(context.Background(), order.Callback{
		Source:    "batidos",
		DedupeKey: "batidos-1",
		OrderID:   fx.orderID,
		Ref:       "code-batidos",
		State:     order.CallbackDelivered,
	}); err != nil {
		t.Fatalf("el cierre no debía fallar: %v", err)
	}

	if _, err := fx.engine.ConfirmFromCallback(context.Background(), order.Callback{
		Source:    "batidos",
		DedupeKey: "batidos-2",
		OrderID:   fx.orderID,
		Ref:       "code-batidos",
		State:     order.CallbackProgress,
	}); err != nil {
		t.Fatalf("un aviso tardío sobre una línea cerrada no debe devolver error: %v", err)
	}

	batidos := lineFor(t, mustOrder(t, fx.orders, fx.orderID), "batidos")
	if batidos.State != dispenser.LineDelivered {
		t.Fatalf("una línea entregada no debe volver a elaboración y quedó %q", batidos.State)
	}

	if len(fx.reconcile["batidos"].calls) != 0 {
		t.Fatalf("una línea cerrada no se reconcilia y hubo %d consultas", len(fx.reconcile["batidos"].calls))
	}
}

type staleDueStore struct {
	*store.Memory

	mu  sync.Mutex
	due []store.Line
}

func newStaleDueStore() *staleDueStore {
	return &staleDueStore{Memory: store.NewMemory()}
}

func (s *staleDueStore) DueLines(ctx context.Context, now time.Time, limit int) ([]store.Line, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.due != nil {
		return append([]store.Line(nil), s.due...), nil
	}

	return s.Memory.DueLines(ctx, now, limit)
}

func (s *staleDueStore) copyDueLines(t *testing.T, now time.Time) []store.Line {
	t.Helper()

	due, err := s.Memory.DueLines(context.Background(), now, 10)
	if err != nil {
		t.Fatalf("DueLines: %v", err)
	}
	if len(due) == 0 {
		t.Fatalf("el worker necesita una línea vencida para copiar y no hubo ninguna")
	}

	s.mu.Lock()
	s.due = due
	s.mu.Unlock()

	return due
}

func TestReconcileDueDoesNotOverwriteTheCloseThatACallbackAlreadyMade(t *testing.T) {
	cases := []struct {
		name   string
		status dispenser.Status
	}{
		{
			name:   "el worker traía un cierre fallido",
			status: dispenser.Status{State: dispenser.LineFailed, Reason: "la máquina informó que no entregó"},
		},
		{
			name:   "el worker traía una entrega",
			status: dispenser.Status{State: dispenser.LineDelivered, ProviderOrderNo: "order-no-1"},
		},
		{
			name:   "el worker traía un aplazo de la consulta",
			status: dispenser.Status{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			orders := newStaleDueStore()
			clock := manualClockAt(engineNow)

			machine := &reconcileMachine{attempt: dispenser.DispatchResult{Accepted: true}}
			machine.status = tc.status

			engine := order.New(
				orders,
				map[string]dispenser.Dispenser{"batidos": machine},
				order.WithClock(clock.clock()),
				order.WithIDGenerator(sequentialIDs()),
			)

			placed := place(t, engine, drinkOnlyDraft())
			if err := engine.ConfirmPayment(ctx, placed.ID, engineNow); err != nil {
				t.Fatalf("ConfirmPayment: %v", err)
			}

			clock.set(engineNow.Add(2 * time.Minute))
			copied := orders.copyDueLines(t, engineNow.Add(2*time.Minute))

			if copied[0].State != dispenser.LineSent {
				t.Fatalf("el worker debía copiar una línea abierta y copió %q", copied[0].State)
			}

			duplicate, err := engine.CloseFromCallback(ctx, order.Callback{
				Source:      "batidos",
				DedupeKey:   "batidos-1",
				OrderID:     placed.ID,
				Ref:         "code-adoptado",
				MachineType: "batidos",
				State:       order.CallbackDelivered,
			})
			if err != nil {
				t.Fatalf("el callback debía cerrar la línea y devolvió: %v", err)
			}
			if duplicate {
				t.Fatalf("el primer callback no es un duplicado")
			}

			closed := lineFor(t, mustOrder(t, orders, placed.ID), "batidos")
			if closed.State != dispenser.LineDelivered || closed.Ref != "code-adoptado" {
				t.Fatalf("el callback debía dejar la línea entregada con su referencia y quedó %+v", closed)
			}

			if err := engine.ReconcileDue(ctx, 10); err != nil {
				t.Fatalf("el barrido con una copia vieja no debe devolver error: %v", err)
			}

			if machine.reconcileCount() != 1 {
				t.Fatalf("el worker debía consultar al proveedor una vez y consultó %d", machine.reconcileCount())
			}

			stored := mustOrder(t, orders, placed.ID)
			line := lineFor(t, stored, "batidos")

			if line.State != dispenser.LineDelivered {
				t.Fatalf("el worker revirtió una línea cerrada por el callback: State=%q", line.State)
			}
			if line.Ref != "code-adoptado" {
				t.Fatalf("el worker borró la referencia adoptada por el callback: Ref=%q, se esperaba %q", line.Ref, "code-adoptado")
			}
			if !line.DeadlineAt.IsZero() || !line.ReconcileUntil.IsZero() {
				t.Fatalf("el worker rearmó los plazos de una línea cerrada: deadline=%v until=%v", line.DeadlineAt, line.ReconcileUntil)
			}
			if line.LastError != "" {
				t.Fatalf("el worker escribió un error en una línea entregada: %q", line.LastError)
			}
			if stored.Status != store.OrderDelivered {
				t.Fatalf("Status=%q, se esperaba %q", stored.Status, store.OrderDelivered)
			}
		})
	}
}

type competingWriteStore struct {
	*store.Memory

	mu        sync.Mutex
	remaining int
	calls     int
}

func (s *competingWriteStore) SaveLine(ctx context.Context, line store.Line) (store.Line, error) {
	s.mu.Lock()
	s.calls++
	compete := s.remaining > 0
	if compete {
		s.remaining--
	}
	s.mu.Unlock()

	if compete {
		loaded, err := s.Memory.LoadOrder(ctx, line.OrderID)
		if err != nil {
			return store.Line{}, err
		}

		for _, candidate := range loaded.Lines {
			if candidate.ID != line.ID {
				continue
			}

			candidate.LastError = "otro escritor tocó la línea"

			if _, err := s.Memory.SaveLine(ctx, candidate); err != nil {
				return store.Line{}, err
			}

			break
		}
	}

	return s.Memory.SaveLine(ctx, line)
}

func (s *competingWriteStore) arm(times int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.remaining = times
	s.calls = 0
}

func (s *competingWriteStore) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.calls
}

func TestCloseFromCallbackRetriesOnceWhenAnotherWriterTookTheLine(t *testing.T) {
	cases := []struct {
		name      string
		competing int
		wantState dispenser.LineState
		wantCalls int
	}{
		{
			name:      "un solo escritor adelantado: se reintenta una vez y el cierre entra",
			competing: 1,
			wantState: dispenser.LineDelivered,
			wantCalls: 2,
		},
		{
			name:      "dos escritores adelantados: no se reintenta dos veces",
			competing: 2,
			wantState: dispenser.LineSent,
			wantCalls: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			orders := &competingWriteStore{Memory: store.NewMemory()}

			batidos := &stubMachine{attempt: dispenser.DispatchResult{Accepted: true, ProviderRef: "code-batidos"}}
			snacks := &stubMachine{attempt: dispenser.DispatchResult{Accepted: true, ProviderRef: "sub-snacks"}}

			engine := order.New(orders, map[string]dispenser.Dispenser{
				"batidos": reconcilingMachine{dispatch: batidos, reconcile: &reconcileStub{}},
				"snacks":  reconcilingMachine{dispatch: snacks, reconcile: &reconcileStub{}},
			}, order.WithClock(frozenClock()), order.WithIDGenerator(sequentialIDs()))

			placed := place(t, engine, comboDraft())
			if err := engine.ConfirmPayment(ctx, placed.ID, engineNow); err != nil {
				t.Fatalf("ConfirmPayment: %v", err)
			}

			orders.arm(tc.competing)

			duplicate, err := engine.CloseFromCallback(ctx, order.Callback{
				Source:      "snacks",
				DedupeKey:   "snacks-1",
				OrderID:     placed.ID,
				Ref:         "sub-snacks",
				MachineType: "snacks",
				State:       order.CallbackDelivered,
			})
			if err != nil {
				t.Fatalf("un conflicto de escritura no debe devolver error: %v", err)
			}
			if duplicate {
				t.Fatalf("el primer callback no es un duplicado")
			}

			if calls := orders.callCount(); calls != tc.wantCalls {
				t.Fatalf("el cierre intentó guardar %d veces, se esperaban %d", calls, tc.wantCalls)
			}

			line := lineFor(t, mustOrder(t, orders, placed.ID), "snacks")
			if line.State != tc.wantState {
				t.Fatalf("la línea quedó en %q, se esperaba %q", line.State, tc.wantState)
			}
		})
	}
}

type failingSaveLineStore struct {
	*store.Memory

	mu        sync.Mutex
	remaining int
}

func (s *failingSaveLineStore) SaveLine(ctx context.Context, line store.Line) (store.Line, error) {
	s.mu.Lock()
	if s.remaining > 0 {
		s.remaining--
		s.mu.Unlock()

		return store.Line{}, errors.New("el store no pudo guardar la línea")
	}
	s.mu.Unlock()

	return s.Memory.SaveLine(ctx, line)
}

func (s *failingSaveLineStore) failNextSaveLine() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.remaining++
}

func TestCallbackDoesNotBurnItsKeyWhenTheApplyFails(t *testing.T) {
	orders := store.NewMemory()
	failing := &failingSaveLineStore{Memory: orders}

	batidos := &stubMachine{attempt: dispenser.DispatchResult{Accepted: true, ProviderRef: "code-batidos"}}
	snacks := &stubMachine{attempt: dispenser.DispatchResult{Accepted: true, ProviderRef: "sub-snacks"}}

	engine := order.New(failing, map[string]dispenser.Dispenser{
		"batidos": reconcilingMachine{dispatch: batidos, reconcile: &reconcileStub{}},
		"snacks":  reconcilingMachine{dispatch: snacks, reconcile: &reconcileStub{}},
	}, order.WithClock(frozenClock()), order.WithIDGenerator(sequentialIDs()))

	placed := place(t, engine, comboDraft())

	if err := engine.ConfirmPayment(context.Background(), placed.ID, engineNow); err != nil {
		t.Fatalf("la confirmación de pago no debía fallar: %v", err)
	}

	event := order.Callback{
		Source:    "snacks",
		DedupeKey: "snacks-1",
		OrderID:   placed.ID,
		Ref:       "sub-snacks",
		State:     order.CallbackDelivered,
	}

	failing.failNextSaveLine()

	duplicate, err := engine.CloseFromCallback(context.Background(), event)
	if err == nil {
		t.Fatalf("si el efecto no se pudo guardar el callback debe devolver el error")
	}

	if duplicate {
		t.Fatalf("un callback que no se pudo aplicar no debe quedar registrado como visto ni como duplicado")
	}

	duplicate, err = engine.CloseFromCallback(context.Background(), event)
	if err != nil {
		t.Fatalf("el reintento del proveedor con la misma clave debía procesarse: %v", err)
	}

	if duplicate {
		t.Fatalf("el reintento debía procesarse como un evento nuevo")
	}

	snacksLine := lineFor(t, mustOrder(t, orders, placed.ID), "snacks")
	if snacksLine.State != dispenser.LineDelivered {
		t.Fatalf("la línea debía quedar entregada en el reintento y quedó %q", snacksLine.State)
	}
}

func TestConfirmFromCallbackIsIdempotentBySourceAndKey(t *testing.T) {
	fx := dispatched(t)

	fx.reconcile["batidos"].status = dispenser.Status{State: dispenser.LineDelivered}

	event := order.Callback{
		Source:    "batidos",
		DedupeKey: "batidos-1",
		OrderID:   fx.orderID,
		Ref:       "code-batidos",
		State:     order.CallbackDelivered,
	}

	if _, err := fx.engine.ConfirmFromCallback(context.Background(), event); err != nil {
		t.Fatalf("el primer callback no debía fallar: %v", err)
	}

	duplicate, err := fx.engine.ConfirmFromCallback(context.Background(), event)
	if err != nil {
		t.Fatalf("un reintento del mismo callback no debe devolver error: %v", err)
	}

	if !duplicate {
		t.Fatalf("un reintento del mismo callback debe informarse como duplicado y no procesarse de nuevo")
	}

	if len(fx.reconcile["batidos"].calls) != 1 {
		t.Fatalf("un reintento no debe volver a consultar al proveedor: %d consultas", len(fx.reconcile["batidos"].calls))
	}
}

func TestCloseFromCallbackResolvesTheLineByThirdOrderNo(t *testing.T) {
	fx := dispatchedWithoutProviderRefs(t, comboDraft())

	snacksBefore := lineFor(t, mustOrder(t, fx.orders, fx.orderID), "snacks")
	if snacksBefore.Ref != "" {
		t.Fatalf("el escenario necesita una línea sin referencia y tiene %q", snacksBefore.Ref)
	}

	duplicate, err := fx.engine.CloseFromCallback(context.Background(), order.Callback{
		Source:       "snacks",
		DedupeKey:    "snacks-1",
		OrderID:      fx.orderID,
		Ref:          "sub-que-llego-por-el-callback",
		ThirdOrderNo: snacksBefore.ID,
		MachineType:  "snacks",
		State:        order.CallbackDelivered,
	})
	if err != nil {
		t.Fatalf("el callback no debía fallar: %v", err)
	}

	if duplicate {
		t.Fatalf("el primer callback debe procesarse")
	}

	snacks := lineFor(t, mustOrder(t, fx.orders, fx.orderID), "snacks")
	if snacks.State != dispenser.LineDelivered {
		t.Fatalf("el ThirdOrderNo identifica la línea y debía quedar entregada, pero quedó %q", snacks.State)
	}

	if snacks.Ref != "sub-que-llego-por-el-callback" {
		t.Fatalf("la línea debía adoptar la referencia del callback y quedó %q", snacks.Ref)
	}

	if batidos := lineFor(t, mustOrder(t, fx.orders, fx.orderID), "batidos"); batidos.State == dispenser.LineDelivered {
		t.Fatalf("el callback de snacks no debe cerrar la línea de batidos")
	}
}

func TestCloseFromCallbackAdoptedRefWinsOnTheNextEvent(t *testing.T) {
	fx := dispatchedWithoutProviderRefs(t, comboDraft())

	snacksBefore := lineFor(t, mustOrder(t, fx.orders, fx.orderID), "snacks")

	if _, err := fx.engine.CloseFromCallback(context.Background(), order.Callback{
		Source:       "snacks",
		DedupeKey:    "snacks-1",
		OrderID:      fx.orderID,
		Ref:          "sub-adoptado",
		ThirdOrderNo: snacksBefore.ID,
		MachineType:  "snacks",
		State:        order.CallbackDelivered,
	}); err != nil {
		t.Fatalf("el primer callback no debía fallar: %v", err)
	}

	if _, err := fx.engine.CloseFromCallback(context.Background(), order.Callback{
		Source:    "snacks",
		DedupeKey: "snacks-2",
		OrderID:   fx.orderID,
		Ref:       "sub-adoptado",
		State:     order.CallbackFailed,
		Reason:    "llego tarde",
	}); err != nil {
		t.Fatalf("el segundo callback debía entrar por la referencia adoptada: %v", err)
	}

	snacks := lineFor(t, mustOrder(t, fx.orders, fx.orderID), "snacks")
	if snacks.State != dispenser.LineDelivered {
		t.Fatalf("una línea cerrada no se reabre y quedó %q", snacks.State)
	}

	if snacks.LastError != "" {
		t.Fatalf("el evento tardío no debe escribir un motivo sobre una línea entregada: %q", snacks.LastError)
	}
}

func TestCloseFromCallbackClosesTheOnlyLineWithoutRef(t *testing.T) {
	fx := dispatchedWithoutProviderRefs(t, comboDraft())

	duplicate, err := fx.engine.CloseFromCallback(context.Background(), order.Callback{
		Source:      "snacks",
		DedupeKey:   "snacks-1",
		OrderID:     fx.orderID,
		Ref:         "sub-sin-coincidencia",
		MachineType: "snacks",
		State:       order.CallbackDelivered,
	})
	if err != nil {
		t.Fatalf("con una sola línea candidata el callback no debía fallar: %v", err)
	}

	if duplicate {
		t.Fatalf("el primer callback debe procesarse")
	}

	snacks := lineFor(t, mustOrder(t, fx.orders, fx.orderID), "snacks")
	if snacks.State != dispenser.LineDelivered {
		t.Fatalf("la única línea de esa máquina sin referencia debía cerrarse y quedó %q", snacks.State)
	}

	if snacks.Ref != "sub-sin-coincidencia" {
		t.Fatalf("la línea debía adoptar la referencia del callback y quedó %q", snacks.Ref)
	}
}

func TestCloseFromCallbackRefusesToGuessBetweenTwoLines(t *testing.T) {
	fx := dispatchedWithoutProviderRefs(t, twoSnackLinesDraft())

	duplicate, err := fx.engine.CloseFromCallback(context.Background(), order.Callback{
		Source:      "snacks",
		DedupeKey:   "snacks-1",
		OrderID:     fx.orderID,
		MachineType: "snacks",
		State:       order.CallbackDelivered,
	})

	if !errors.Is(err, order.ErrCallbackAmbiguous) {
		t.Fatalf("con dos candidatas se esperaba ErrCallbackAmbiguous y llegó %v", err)
	}

	if duplicate {
		t.Fatalf("un evento ambiguo no es un duplicado")
	}

	for _, line := range mustOrder(t, fx.orders, fx.orderID).Lines {
		if line.State == dispenser.LineDelivered || line.State == dispenser.LineFailed {
			t.Fatalf("un callback ambiguo no debe cerrar ninguna línea: %+v", line)
		}
	}
}

func TestCloseFromCallbackDoesNotAdoptTheOtherMachineLineWithoutRef(t *testing.T) {
	fx := dispatchedWithoutProviderRefs(t, comboDraft())

	duplicate, err := fx.engine.CloseFromCallback(context.Background(), order.Callback{
		Source:      "snacks",
		DedupeKey:   "snacks-1",
		OrderID:     fx.orderID,
		MachineType: "snacks",
		State:       order.CallbackDelivered,
	})
	if err != nil {
		t.Fatalf("el callback no debía fallar: %v", err)
	}

	if duplicate {
		t.Fatalf("el primer callback debe procesarse")
	}

	loaded := mustOrder(t, fx.orders, fx.orderID)

	snacks := lineFor(t, loaded, "snacks")
	if snacks.State != dispenser.LineDelivered {
		t.Fatalf("la línea de snacks debía cerrarse y quedó %q", snacks.State)
	}

	batidos := lineFor(t, loaded, "batidos")
	if batidos.State == dispenser.LineDelivered || batidos.State == dispenser.LineFailed {
		t.Fatalf("una línea de la otra máquina no debe cerrarse: %+v", batidos)
	}

	if batidos.Ref != "" {
		t.Fatalf("una línea de la otra máquina no debe salir con referencia adoptada: %q", batidos.Ref)
	}
}

func TestCloseFromCallbackDoesNotGuessWithoutTheMachineType(t *testing.T) {
	fx := dispatchedWithoutProviderRefs(t, comboDraft())

	duplicate, err := fx.engine.CloseFromCallback(context.Background(), order.Callback{
		Source:    "snacks",
		DedupeKey: "snacks-1",
		OrderID:   fx.orderID,
		Ref:       "sub-sin-coincidencia",
		State:     order.CallbackDelivered,
	})

	if !errors.Is(err, order.ErrCallbackUnknownLine) {
		t.Fatalf("sin tipo de máquina la tercera regla no aplica y se esperaba ErrCallbackUnknownLine, llegó %v", err)
	}

	if duplicate {
		t.Fatalf("un aviso nuevo no es un duplicado")
	}

	for _, line := range mustOrder(t, fx.orders, fx.orderID).Lines {
		if line.State == dispenser.LineDelivered || line.State == dispenser.LineFailed {
			t.Fatalf("sin tipo de máquina no se debe cerrar ninguna línea: %+v", line)
		}
	}
}

func TestCloseFromCallbackWithoutAnyReferenceIsAnUnknownLine(t *testing.T) {
	fx := dispatched(t)

	for _, machineType := range []string{"", "snacks"} {
		t.Run("maquina="+machineType, func(t *testing.T) {
			_, err := fx.engine.CloseFromCallback(context.Background(), order.Callback{
				Source:      "snacks",
				DedupeKey:   "snacks-" + machineType,
				OrderID:     fx.orderID,
				MachineType: machineType,
				State:       order.CallbackDelivered,
			})

			if !errors.Is(err, order.ErrCallbackUnknownLine) {
				t.Fatalf("con todas las líneas referenciadas no hay candidata y se esperaba ErrCallbackUnknownLine, llegó %v", err)
			}
		})
	}
}

func giveLineRef(t *testing.T, orders *store.Memory, orderID string, position int, ref string) store.Line {
	t.Helper()

	line := mustOrder(t, orders, orderID).Lines[position]
	line.Ref = ref

	if _, err := orders.SaveLine(context.Background(), line); err != nil {
		t.Fatalf("no se pudo guardar la referencia de la línea: %v", err)
	}

	return line
}

func TestCloseFromCallbackDoesNotCloseAnotherSnackLineWhenTheRefDoesNotMatch(t *testing.T) {
	fx := dispatchedWithoutProviderRefs(t, twoSnackLinesDraft())

	giveLineRef(t, fx.orders, fx.orderID, 0, "sub-guardado")

	duplicate, err := fx.engine.CloseFromCallback(context.Background(), order.Callback{
		Source:      "snacks",
		DedupeKey:   "snacks-1",
		OrderID:     fx.orderID,
		Ref:         "sub-rotado",
		MachineType: "snacks",
		State:       order.CallbackDelivered,
	})

	if !errors.Is(err, order.ErrCallbackAmbiguous) {
		t.Fatalf("con dos líneas de snacks la referencia que no coincide no elige ninguna y se esperaba ErrCallbackAmbiguous, llegó %v", err)
	}

	if duplicate {
		t.Fatalf("un evento ambiguo no es un duplicado")
	}

	for _, line := range mustOrder(t, fx.orders, fx.orderID).Lines {
		if line.State == dispenser.LineDelivered || line.State == dispenser.LineFailed {
			t.Fatalf("una referencia que no coincide no debe cerrar ninguna línea de snacks: %+v", line)
		}

		if line.Ref != "" && line.Ref != "sub-guardado" {
			t.Fatalf("ninguna línea debe adoptar una referencia que no le corresponde: %+v", line)
		}
	}
}

func TestCloseFromCallbackWithARefMismatchAndNoMachineTypeIsAnUnknownLine(t *testing.T) {
	fx := dispatchedWithoutProviderRefs(t, twoSnackLinesDraft())

	giveLineRef(t, fx.orders, fx.orderID, 0, "sub-guardado")

	duplicate, err := fx.engine.CloseFromCallback(context.Background(), order.Callback{
		Source:    "snacks",
		DedupeKey: "snacks-1",
		OrderID:   fx.orderID,
		Ref:       "sub-rotado",
		State:     order.CallbackDelivered,
	})

	if !errors.Is(err, order.ErrCallbackUnknownLine) {
		t.Fatalf("sin tipo de máquina la referencia que no coincide se esperaba ErrCallbackUnknownLine y llegó %v", err)
	}

	if duplicate {
		t.Fatalf("un aviso nuevo no es un duplicado")
	}

	for _, line := range mustOrder(t, fx.orders, fx.orderID).Lines {
		if line.State == dispenser.LineDelivered || line.State == dispenser.LineFailed {
			t.Fatalf("sin tipo de máquina no se debe cerrar ninguna línea: %+v", line)
		}
	}
}

func TestCloseFromCallbackDoesNotCloseTheOtherMachineLineByThirdOrderNo(t *testing.T) {
	fx := dispatched(t)

	batidosBefore := lineFor(t, mustOrder(t, fx.orders, fx.orderID), "batidos")

	_, err := fx.engine.CloseFromCallback(context.Background(), order.Callback{
		Source:       "snacks",
		DedupeKey:    "snacks-1",
		OrderID:      fx.orderID,
		ThirdOrderNo: batidosBefore.ID,
		MachineType:  "snacks",
		State:        order.CallbackDelivered,
	})

	if !errors.Is(err, order.ErrCallbackUnknownLine) {
		t.Fatalf("un ThirdOrderNo de la línea de batidos no identifica ninguna línea de snacks y se esperaba ErrCallbackUnknownLine, llegó %v", err)
	}

	batidos := lineFor(t, mustOrder(t, fx.orders, fx.orderID), "batidos")
	if batidos.State == dispenser.LineDelivered || batidos.State == dispenser.LineFailed {
		t.Fatalf("un callback de snacks no debe cerrar la línea de batidos: %+v", batidos)
	}
}

func TestCloseFromCallbackDoesNotMatchAThirdOrderNoWithoutTheMachineType(t *testing.T) {
	fx := dispatchedWithoutProviderRefs(t, comboDraft())

	batidosBefore := lineFor(t, mustOrder(t, fx.orders, fx.orderID), "batidos")
	if batidosBefore.Ref != "" {
		t.Fatalf("el escenario necesita una línea de batidos sin referencia y tiene %q", batidosBefore.Ref)
	}

	_, err := fx.engine.CloseFromCallback(context.Background(), order.Callback{
		Source:       "snacks",
		DedupeKey:    "snacks-1",
		OrderID:      fx.orderID,
		ThirdOrderNo: batidosBefore.ID,
		State:        order.CallbackDelivered,
	})

	if !errors.Is(err, order.ErrCallbackUnknownLine) {
		t.Fatalf("sin tipo de máquina el ThirdOrderNo de la otra máquina se esperaba ErrCallbackUnknownLine y llegó %v", err)
	}

	batidos := lineFor(t, mustOrder(t, fx.orders, fx.orderID), "batidos")
	if batidos.State == dispenser.LineDelivered || batidos.State == dispenser.LineFailed {
		t.Fatalf("un callback de snacks no debe cerrar la línea de batidos: %+v", batidos)
	}
}
