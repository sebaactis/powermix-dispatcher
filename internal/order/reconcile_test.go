package order_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/linestate"
	"compras-remotas/internal/machine"
	"compras-remotas/internal/order"
	"compras-remotas/internal/store"
)

func TestEngineConfirmPaymentArmsTheReconcileWindow(t *testing.T) {
	cases := []struct {
		name     string
		opts     []order.Option
		deadline time.Duration
		until    time.Duration
	}{
		{
			name:     "valores por defecto: dos minutos de espera y cuatro de tope",
			deadline: 2 * time.Minute,
			until:    4 * time.Minute,
		},
		{
			name:     "ventana configurada",
			opts:     []order.Option{order.WithReconcileWindow(90 * time.Second)},
			deadline: 2 * time.Minute,
			until:    3*time.Minute + 30*time.Second,
		},
		{
			name:     "ventana no positiva conserva el valor por defecto",
			opts:     []order.Option{order.WithReconcileWindow(-time.Minute)},
			deadline: 2 * time.Minute,
			until:    4 * time.Minute,
		},
		{
			name:     "los dos plazos salen del mismo ahora",
			opts:     []order.Option{order.WithLineDeadline(time.Minute), order.WithReconcileWindow(2 * time.Minute)},
			deadline: time.Minute,
			until:    3 * time.Minute,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setup := newReconcileFixture(t, acceptedDrinkMachine(), tc.opts...)

			line := setup.line(t)
			if want := engineNow.Add(tc.deadline); !line.DeadlineAt.Equal(want) {
				t.Fatalf("DeadlineAt=%v, se esperaba %v", line.DeadlineAt, want)
			}
			if want := engineNow.Add(tc.until); !line.ReconcileUntil.Equal(want) {
				t.Fatalf("ReconcileUntil=%v, se esperaba %v", line.ReconcileUntil, want)
			}
		})
	}
}

func TestEngineReconcileDueMarksADeliveredLineFromTheProvider(t *testing.T) {
	machine := acceptedDrinkMachine()
	machine.status = dispenser.Status{State: linestate.Delivered, ProviderOrderNo: "order-no-1"}

	setup := newReconcileFixture(t, machine)
	setup.clock.set(engineNow.Add(2 * time.Minute))

	if err := setup.reconcileDue(t); err != nil {
		t.Fatalf("ReconcileDue: %v", err)
	}

	if machine.reconcileCount() != 1 {
		t.Fatalf("la máquina recibió %d consultas de reconciliación, se esperaba 1", machine.reconcileCount())
	}
	if machine.dispatchCount() != 1 {
		t.Fatalf("la reconciliación volvió a despachar: %d despachos, se esperaba 1", machine.dispatchCount())
	}

	req := machine.onlyReconcile(t)
	wantLine := setup.placed.Lines[0]

	if req.OrderID != setup.placed.ID || req.LineID != wantLine.ID {
		t.Fatalf("la consulta llevó order=%q line=%q, se esperaba order=%q line=%q", req.OrderID, req.LineID, setup.placed.ID, wantLine.ID)
	}
	if req.KnownRef != "exchange-code-1" {
		t.Fatalf("la consulta llevó Ref=%q, se esperaba el asa guardada en la línea", req.KnownRef)
	}
	if req.SKU != "sku-batido" || req.AmountCents != 1500 {
		t.Fatalf("la consulta llevó SKU=%q importe=%d", req.SKU, req.AmountCents)
	}
	if req.Machine.DeviceID != "device-1" {
		t.Fatalf("la consulta llevó la máquina %+v, se esperaba la de la línea", req.Machine)
	}

	line := setup.line(t)
	if line.State != linestate.Delivered {
		t.Fatalf("la línea quedó en %q, se esperaba %q", line.State, linestate.Delivered)
	}
	if !line.DeadlineAt.IsZero() {
		t.Fatalf("la línea entregada conserva el plazo %v", line.DeadlineAt)
	}
	if !line.ReconcileUntil.IsZero() {
		t.Fatalf("la línea entregada conserva el tope de reconciliación %v", line.ReconcileUntil)
	}
	if line.LastError != "" {
		t.Fatalf("la línea entregada conserva el error %q", line.LastError)
	}

	if got := setup.order(t); got.Status != store.OrderDelivered {
		t.Fatalf("Status=%q, se esperaba %q: la orden no puede quedarse en %q", got.Status, store.OrderDelivered, store.OrderDispatching)
	}
}

func TestEngineReconcileDueMarksAFailedLineAndSettlesTheOrder(t *testing.T) {
	machine := acceptedDrinkMachine()
	machine.status = dispenser.Status{State: linestate.Failed, Reason: "la máquina informó que no entregó"}

	setup := newReconcileFixture(t, machine)
	setup.clock.set(engineNow.Add(2 * time.Minute))

	if err := setup.reconcileDue(t); err != nil {
		t.Fatalf("ReconcileDue: %v", err)
	}

	if machine.dispatchCount() != 1 {
		t.Fatalf("la máquina recibió %d despachos, se esperaba 1: una línea resuelta por reconciliación se informa, no se redespacha", machine.dispatchCount())
	}

	line := setup.line(t)
	if line.State != linestate.Failed {
		t.Fatalf("la línea quedó en %q, se esperaba %q", line.State, linestate.Failed)
	}
	if !line.DeadlineAt.IsZero() || !line.ReconcileUntil.IsZero() {
		t.Fatalf("la línea fallida conserva plazos: deadline=%v until=%v", line.DeadlineAt, line.ReconcileUntil)
	}
	if !strings.Contains(line.LastError, "no entregó") {
		t.Fatalf("LastError=%q, se esperaba el motivo del proveedor", line.LastError)
	}

	if got := setup.order(t); got.Status != store.OrderFailed {
		t.Fatalf("Status=%q, se esperaba %q: la orden no puede quedarse en %q", got.Status, store.OrderFailed, store.OrderDispatching)
	}
}

func TestEngineReconcileDueKeepsAskingWhileTheWindowIsOpen(t *testing.T) {
	cases := []struct {
		name     string
		opts     []order.Option
		interval time.Duration
	}{
		{name: "intervalo por defecto", interval: 30 * time.Second},
		{name: "intervalo configurado", opts: []order.Option{order.WithReconcileInterval(time.Minute)}, interval: time.Minute},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			machine := acceptedDrinkMachine()
			machine.status = dispenser.Status{State: linestate.InDoubt, Reason: "el proveedor no informa el pedido"}

			setup := newReconcileFixture(t, machine, tc.opts...)
			until := setup.line(t).ReconcileUntil

			setup.clock.set(engineNow.Add(2 * time.Minute))

			if err := setup.reconcileDue(t); err != nil {
				t.Fatalf("ReconcileDue: %v", err)
			}

			line := setup.line(t)
			if want := engineNow.Add(2*time.Minute + tc.interval); !line.DeadlineAt.Equal(want) {
				t.Fatalf("DeadlineAt=%v, se esperaba %v: la duda empuja el plazo un intervalo", line.DeadlineAt, want)
			}
			if !line.ReconcileUntil.Equal(until) {
				t.Fatalf("ReconcileUntil=%v, se esperaba %v sin tocar: el tope no se mueve", line.ReconcileUntil, until)
			}
			if line.State != linestate.Sent {
				t.Fatalf("la línea quedó en %q, se esperaba %q: mientras la ventana está abierta la línea sigue en vuelo para que el worker la vuelva a ver", line.State, linestate.Sent)
			}
			if !strings.Contains(line.LastError, "no informa el pedido") {
				t.Fatalf("LastError=%q, se esperaba el motivo informado por el proveedor", line.LastError)
			}
		})
	}
}

func TestEngineReconcileDueClampsTheLastScanToTheHardStop(t *testing.T) {
	machine := acceptedDrinkMachine()
	machine.status = dispenser.Status{State: linestate.InDoubt, Reason: "el proveedor no informa el pedido"}

	setup := newReconcileFixture(t, machine)
	until := setup.line(t).ReconcileUntil

	setup.clock.set(until.Add(-15 * time.Second))

	if err := setup.reconcileDue(t); err != nil {
		t.Fatalf("ReconcileDue: %v", err)
	}

	line := setup.line(t)
	if !line.DeadlineAt.Equal(until) {
		t.Fatalf("DeadlineAt=%v, se esperaba %v: el último escaneo cae exactamente en el tope, nunca después", line.DeadlineAt, until)
	}
	if !line.ReconcileUntil.Equal(until) {
		t.Fatalf("ReconcileUntil=%v, se esperaba %v sin tocar", line.ReconcileUntil, until)
	}
}

func TestEngineReconcileDueClosesTheLineInDoubtAtTheHardStop(t *testing.T) {
	machine := acceptedDrinkMachine()
	machine.status = dispenser.Status{State: linestate.InDoubt, Reason: "el proveedor no informa el pedido"}
	notifier := &recordingNotifier{}

	setup := newReconcileFixture(t, machine, order.WithNotifier(notifier))
	until := setup.line(t).ReconcileUntil

	setup.clock.set(until)

	if err := setup.reconcileDue(t); err != nil {
		t.Fatalf("ReconcileDue: %v", err)
	}

	line := setup.line(t)
	if line.State != linestate.InDoubt {
		t.Fatalf("la línea quedó en %q, se esperaba %q: sin dato no se afirma nada", line.State, linestate.InDoubt)
	}
	if !line.DeadlineAt.IsZero() {
		t.Fatalf("la línea en duda conserva el plazo %v: se escanearía para siempre", line.DeadlineAt)
	}
	if !line.ReconcileUntil.IsZero() {
		t.Fatalf("la línea en duda conserva el tope %v", line.ReconcileUntil)
	}

	if got := setup.order(t); got.Status != store.OrderInDoubt {
		t.Fatalf("Status=%q, se esperaba %q", got.Status, store.OrderInDoubt)
	}

	event := notifier.only(t)
	if event.OrderID != setup.placed.ID {
		t.Fatalf("el evento apunta a la orden %q, se esperaba %q", event.OrderID, setup.placed.ID)
	}
	if event.Kind != "order_in_doubt" {
		t.Fatalf("Kind=%q, se esperaba %q", event.Kind, "order_in_doubt")
	}
	if event.Detail == "" {
		t.Fatal("el evento quedó sin detalle para la persona que tiene que actuar")
	}

	if err := setup.reconcileDue(t); err != nil {
		t.Fatalf("ReconcileDue sobre una línea ya cerrada: %v", err)
	}
	if machine.reconcileCount() != 1 {
		t.Fatalf("la línea se volvió a consultar: %d consultas, se esperaba 1", machine.reconcileCount())
	}
	if notifier.count() != 1 {
		t.Fatalf("el notificador recibió %d eventos, se esperaba exactamente 1", notifier.count())
	}
}

func TestEngineReconcileDueKeepsTheLineWhenTheProviderAnswersWithAnError(t *testing.T) {
	machine := acceptedDrinkMachine()
	machine.reconcileErr = errors.New("dial tcp 10.0.0.1:443: connect: connection refused")

	setup := newReconcileFixture(t, machine)
	setup.clock.set(engineNow.Add(2 * time.Minute))

	err := setup.reconcileDue(t)
	if err == nil {
		t.Fatal("ReconcileDue devolvió nil con un error del proveedor")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("el error devuelto %q, se esperaba el del proveedor", err.Error())
	}

	line := setup.line(t)
	if line.State != linestate.Sent {
		t.Fatalf("la línea quedó en %q, se esperaba %q: un error de consulta no resuelve la línea ni la pierde", line.State, linestate.Sent)
	}
	if !strings.Contains(line.LastError, "connection refused") {
		t.Fatalf("LastError=%q, se esperaba el mensaje del error", line.LastError)
	}
	if want := engineNow.Add(2*time.Minute + 30*time.Second); !line.DeadlineAt.Equal(want) {
		t.Fatalf("DeadlineAt=%v, se esperaba %v: la duda empuja el plazo un intervalo", line.DeadlineAt, want)
	}
	if want := engineNow.Add(4 * time.Minute); !line.ReconcileUntil.Equal(want) {
		t.Fatalf("ReconcileUntil=%v, se esperaba %v sin tocar", line.ReconcileUntil, want)
	}

	if got := setup.order(t); got.Status != store.OrderDispatching {
		t.Fatalf("Status=%q, se esperaba %q: la línea sigue en vuelo", got.Status, store.OrderDispatching)
	}
}

func TestEngineReconcileDueIgnoresALineThatIsNotDue(t *testing.T) {
	machine := acceptedDrinkMachine()
	machine.status = dispenser.Status{State: linestate.Delivered}

	setup := newReconcileFixture(t, machine)
	before := setup.line(t)

	if err := setup.reconcileDue(t); err != nil {
		t.Fatalf("ReconcileDue: %v", err)
	}

	if machine.reconcileCount() != 0 {
		t.Fatalf("la máquina recibió %d consultas con el plazo en el futuro, se esperaban 0", machine.reconcileCount())
	}

	after := setup.line(t)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("la línea cambió sin estar vencida: %+v, se esperaba %+v", after, before)
	}
}

func storedOrder(orderID string, status store.OrderStatus, paidAt time.Time, lines ...store.Line) store.Order {
	return store.Order{
		ID:          orderID,
		KioskID:     "kiosk-1",
		ComboID:     "combo-1",
		AmountCents: 2500,
		Currency:    "ARS",
		Status:      status,
		CreatedAt:   engineNow,
		PaidAt:      paidAt,
		Lines:       lines,
	}
}

func pendingLine(lineID, orderID string, position int, kind string, amountCents int64, sku string) store.Line {
	return store.Line{
		ID:          lineID,
		OrderID:     orderID,
		Position:    position,
		Machine:     machine.Ref{KioskID: "kiosk-1", Kind: machine.Kind(kind), Name: kind + " Powermix", DeviceID: "device-1", DeviceNo: "1", DeviceTypeID: "2"},
		SKU:         sku,
		AmountCents: amountCents,
		State:       linestate.Pending,
	}
}

func newEngineFor(t *testing.T, st store.Store, drivers map[machine.Kind]dispenser.Dispenser, opts ...order.Option) *order.Engine {
	t.Helper()

	engineOpts := []order.Option{order.WithClock(manualClockAt(engineNow).clock()), order.WithIDGenerator(sequentialIDs())}
	engineOpts = append(engineOpts, opts...)

	return order.New(st, drivers, engineOpts...)
}

func mustCreateStored(t *testing.T, st store.Store, stored store.Order) {
	t.Helper()

	if err := st.CreateOrder(context.Background(), stored); err != nil {
		t.Fatalf("CreateOrder(%q): %v", stored.ID, err)
	}
}

func TestEngineReconcileDueReconcilesAClaimedPendingLineInsteadOfRedispatching(t *testing.T) {
	cases := []struct {
		name      string
		until     time.Time
		status    dispenser.Status
		wantState linestate.State
		wantOrder store.OrderStatus
	}{
		{
			name:      "el proveedor informa la entrega",
			until:     engineNow.Add(time.Minute),
			status:    dispenser.Status{State: linestate.Delivered, ProviderOrderNo: "order-no-1"},
			wantState: linestate.Delivered,
			wantOrder: store.OrderDelivered,
		},
		{
			name:      "sin dato y con la ventana agotada",
			until:     engineNow.Add(-time.Second),
			status:    dispenser.Status{State: linestate.InDoubt, Reason: "el proveedor no informa el pedido"},
			wantState: linestate.InDoubt,
			wantOrder: store.OrderInDoubt,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			driver := acceptedDrinkMachine()
			driver.status = tc.status

			line := pendingLine("line-batido", "order-1", 1, "batidos", 1500, "sku-batido")
			line.Attempts = 1
			line.DeadlineAt = engineNow.Add(-time.Minute)
			line.ReconcileUntil = tc.until

			st := store.NewMemory()
			mustCreateStored(t, st, storedOrder("order-1", store.OrderDispatching, engineNow.Add(-3*time.Minute), line))

			engine := newEngineFor(t, st, map[machine.Kind]dispenser.Dispenser{"batidos": driver})

			if err := engine.ReconcileDue(context.Background(), 10); err != nil {
				t.Fatalf("ReconcileDue: %v", err)
			}

			if driver.reconcileCount() != 1 {
				t.Fatalf("la máquina recibió %d consultas de reconciliación, se esperaba 1: una línea reclamada se reconcilia", driver.reconcileCount())
			}
			if driver.dispatchCount() != 0 {
				t.Fatalf("una línea reclamada se volvió a despachar: %d despachos, se esperaban 0", driver.dispatchCount())
			}

			got := mustOrder(t, st, "order-1")
			if got.Lines[0].State != tc.wantState {
				t.Fatalf("la línea quedó en %q, se esperaba %q", got.Lines[0].State, tc.wantState)
			}
			if !got.Lines[0].DeadlineAt.IsZero() || !got.Lines[0].ReconcileUntil.IsZero() {
				t.Fatalf("la línea cerrada conserva plazos: deadline=%v until=%v", got.Lines[0].DeadlineAt, got.Lines[0].ReconcileUntil)
			}
			if got.Status != tc.wantOrder {
				t.Fatalf("Status=%q, se esperaba %q", got.Status, tc.wantOrder)
			}
		})
	}
}

func TestEngineReconcileDueResumesAStalledPendingLineWithTheNextAttempt(t *testing.T) {
	ctx := context.Background()
	st := newRecordingStore()

	driver := acceptedDrinkMachine()

	line := pendingLine("line-batido", "order-1", 1, "batidos", 1500, "sku-batido")
	line.Attempts = 1

	mustCreateStored(t, st, storedOrder("order-1", store.OrderDispatching, engineNow.Add(-3*time.Minute), line))

	engine := newEngineFor(t, st, map[machine.Kind]dispenser.Dispenser{"batidos": driver})

	if err := engine.ReconcileDue(ctx, 10); err != nil {
		t.Fatalf("ReconcileDue: %v", err)
	}

	if driver.dispatchCount() != 1 {
		t.Fatalf("la línea trabada se despachó %d veces, se esperaba exactamente 1", driver.dispatchCount())
	}
	if driver.reconcileCount() != 0 {
		t.Fatalf("una línea sin plazo se reconcilió %d veces en vez de despacharse", driver.reconcileCount())
	}

	attempts := st.attemptsFor(t, line.ID)
	if len(attempts) != 1 {
		t.Fatalf("se registraron %d intentos, se esperaba 1", len(attempts))
	}
	if attempts[0].Number != 2 {
		t.Fatalf("el intento quedó con el número %d, se esperaba 2: el primero se registró antes de la caída", attempts[0].Number)
	}
	if !strings.HasSuffix(attempts[0].IdempotencyKey, ":2") {
		t.Fatalf("la clave del intento es %q, se esperaba la del intento 2", attempts[0].IdempotencyKey)
	}

	got := mustOrder(t, st, "order-1")
	if got.Lines[0].State != linestate.Sent {
		t.Fatalf("la línea quedó en %q, se esperaba %q", got.Lines[0].State, linestate.Sent)
	}
	if got.Lines[0].Attempts != 2 {
		t.Fatalf("la línea acumuló %d intentos, se esperaban 2", got.Lines[0].Attempts)
	}
}

func TestEngineReconcileDueMovesAPaidOrderToDispatchingAndDispatchesItsStalledLines(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()

	batido := acceptedDrinkMachine()
	snack := acceptedDrinkMachine()

	drink := pendingLine("line-batido", "order-1", 1, "batidos", 1500, "sku-batido")
	snacksLine := pendingLine("line-snack", "order-1", 2, "snacks", 1000, "sku-snack")

	mustCreateStored(t, st, storedOrder("order-1", store.OrderPaid, engineNow.Add(-3*time.Minute), drink, snacksLine))

	engine := newEngineFor(t, st, map[machine.Kind]dispenser.Dispenser{"batidos": batido, "snacks": snack})

	if err := engine.ReconcileDue(ctx, 10); err != nil {
		t.Fatalf("ReconcileDue: %v", err)
	}

	if batido.dispatchCount() != 1 || snack.dispatchCount() != 1 {
		t.Fatalf("despachos: batidos=%d snacks=%d, se esperaba uno de cada uno", batido.dispatchCount(), snack.dispatchCount())
	}

	got := mustOrder(t, st, "order-1")
	if got.Status != store.OrderDispatching {
		t.Fatalf("Status=%q, se esperaba %q: el barrido toma la orden paga que quedó trabada", got.Status, store.OrderDispatching)
	}
	for _, line := range got.Lines {
		if line.State != linestate.Sent {
			t.Fatalf("la línea %q quedó en %q, se esperaba %q", line.ID, line.State, linestate.Sent)
		}
		if line.Attempts != 1 {
			t.Fatalf("la línea %q acumuló %d intentos, se esperaba 1", line.ID, line.Attempts)
		}
	}
}

func TestEngineReconcileDueLeavesThePendingLinesThatWereNeverAbandoned(t *testing.T) {
	cases := []struct {
		name   string
		status store.OrderStatus
		paidAt time.Time
		tweak  func(line *store.Line)
	}{
		{
			name:   "reclamada con el plazo todavía abierto",
			status: store.OrderDispatching,
			paidAt: engineNow.Add(-time.Hour),
			tweak:  func(line *store.Line) { line.DeadlineAt = engineNow.Add(time.Minute) },
		},
		{
			name:   "sin plazo de una orden pagada hace menos del plazo",
			status: store.OrderPaid,
			paidAt: engineNow.Add(-time.Minute),
		},
		{
			name:   "sin plazo de una orden que todavía no se pagó",
			status: store.OrderPendingPayment,
			paidAt: engineNow.Add(-time.Hour),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			driver := acceptedDrinkMachine()

			line := pendingLine("line-batido", "order-1", 1, "batidos", 1500, "sku-batido")
			line.Attempts = 1
			if tc.tweak != nil {
				tc.tweak(&line)
			}

			st := store.NewMemory()
			mustCreateStored(t, st, storedOrder("order-1", tc.status, tc.paidAt, line))

			engine := newEngineFor(t, st, map[machine.Kind]dispenser.Dispenser{"batidos": driver})

			before := mustOrder(t, st, "order-1").Lines[0]

			if err := engine.ReconcileDue(context.Background(), 10); err != nil {
				t.Fatalf("ReconcileDue: %v", err)
			}

			if driver.dispatchCount() != 0 || driver.reconcileCount() != 0 {
				t.Fatalf("la línea se tocó: despachos=%d consultas=%d, se esperaban 0 y 0", driver.dispatchCount(), driver.reconcileCount())
			}

			after := mustOrder(t, st, "order-1")
			if !reflect.DeepEqual(after.Lines[0], before) {
				t.Fatalf("la línea cambió sin estar abandonada: %+v, se esperaba %+v", after.Lines[0], before)
			}
			if after.Status != tc.status {
				t.Fatalf("Status=%q, se esperaba %q sin tocar", after.Status, tc.status)
			}
		})
	}
}
