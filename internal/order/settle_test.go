package order_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/linestate"
	"compras-remotas/internal/machine"
	"compras-remotas/internal/order"
	"compras-remotas/internal/store"
)

type notifyFixture struct {
	engine  *order.Engine
	orders  *store.Memory
	orderID string
}

func newNotifyFixture(t *testing.T, notifier *recordingNotifier) *notifyFixture {
	t.Helper()

	orders := store.NewMemory()
	engine := order.New(orders, map[machine.Kind]dispenser.Dispenser{
		"batidos": &stubMachine{attempt: dispenser.DispatchResult{Accepted: true, ProviderRef: "code-batidos"}},
		"snacks":  &stubMachine{attempt: dispenser.DispatchResult{Accepted: true, ProviderRef: "sub-snacks"}},
	}, order.WithClock(frozenClock()), order.WithIDGenerator(sequentialIDs()), order.WithNotifier(notifier))

	placed := place(t, engine, comboDraft())

	if err := engine.ConfirmPayment(context.Background(), placed.ID, engineNow); err != nil {
		t.Fatalf("ConfirmPayment: %v", err)
	}

	return &notifyFixture{engine: engine, orders: orders, orderID: placed.ID}
}

func (f *notifyFixture) failLine(t *testing.T, source, key, ref string) error {
	t.Helper()

	_, err := f.engine.CloseFromCallback(context.Background(), order.Callback{
		Source:    source,
		DedupeKey: key,
		OrderID:   f.orderID,
		Ref:       ref,
		State:     order.CallbackFailed,
		Reason:    "el canal no entregó el producto",
	})

	return err
}

func (f *notifyFixture) progressLine(t *testing.T, key, ref string) error {
	t.Helper()

	_, err := f.engine.CloseFromCallback(context.Background(), order.Callback{
		Source:    "batidos",
		DedupeKey: key,
		OrderID:   f.orderID,
		Ref:       ref,
		State:     order.CallbackProgress,
	})

	return err
}

func TestEngineNotifiesAnOrderExceptionOnlyOnce(t *testing.T) {
	notifier := &recordingNotifier{}
	fx := newNotifyFixture(t, notifier)

	if err := fx.failLine(t, "batidos", "batidos-1", "code-batidos"); err != nil {
		t.Fatalf("el callback de una línea no debía fallar: %v", err)
	}

	if err := fx.failLine(t, "snacks", "snacks-1", "sub-snacks"); err != nil {
		t.Fatalf("el callback que cierra la orden no debía fallar: %v", err)
	}

	if got := mustOrder(t, fx.orders, fx.orderID).Status; got != store.OrderFailed {
		t.Fatalf("Status=%q, se esperaba %q", got, store.OrderFailed)
	}

	event := notifier.only(t)
	if event.Kind != "order_failed" {
		t.Fatalf("Kind=%q, se esperaba %q", event.Kind, "order_failed")
	}
	if event.OrderID != fx.orderID {
		t.Fatalf("el evento apunta a la orden %q, se esperaba %q", event.OrderID, fx.orderID)
	}

	for _, key := range []string{"batidos-2", "batidos-3"} {
		if err := fx.failLine(t, "batidos", key, "code-batidos"); err != nil {
			t.Fatalf("el aviso repetido %q no debía devolver error: %v", key, err)
		}
	}

	if got := notifier.count(); got != 1 {
		t.Fatalf("el notificador recibió %d avisos, se esperaba 1: la misma excepción se avisó de nuevo", got)
	}
}

func TestEngineRetriesTheNoticeThatTheNotifierCouldNotDeliver(t *testing.T) {
	notifier := &recordingNotifier{}
	fx := newNotifyFixture(t, notifier)

	notifier.failWith(errors.New("el servicio de avisos no respondió"))

	if err := fx.failLine(t, "batidos", "batidos-1", "code-batidos"); err != nil {
		t.Fatalf("un callback sin aviso no debía fallar: %v", err)
	}

	if err := fx.failLine(t, "snacks", "snacks-1", "sub-snacks"); err == nil {
		t.Fatal("el callback devolvió nil con el aviso fallido")
	}

	if got := mustOrder(t, fx.orders, fx.orderID).Status; got != store.OrderFailed {
		t.Fatalf("Status=%q, se esperaba %q: el estado se guarda antes del aviso", got, store.OrderFailed)
	}

	sent, err := fx.orders.NotificationSent(context.Background(), fx.orderID, "order_failed")
	if err != nil {
		t.Fatalf("NotificationSent: %v", err)
	}
	if sent {
		t.Fatal("se registró un aviso que el notificador no entregó: la próxima pasada no lo reintentaría")
	}

	notifier.deliver()

	if err := fx.failLine(t, "snacks", "snacks-2", "sub-snacks"); err != nil {
		t.Fatalf("la pasada siguiente no debía fallar: %v", err)
	}

	event := notifier.only(t)
	if event.Kind != "order_failed" {
		t.Fatalf("Kind=%q, se esperaba %q", event.Kind, "order_failed")
	}

	sent, err = fx.orders.NotificationSent(context.Background(), fx.orderID, "order_failed")
	if err != nil {
		t.Fatalf("NotificationSent: %v", err)
	}
	if !sent {
		t.Fatal("el aviso entregado quedó sin registrar: la pasada siguiente lo repetiría")
	}

	if err := fx.failLine(t, "snacks", "snacks-3", "sub-snacks"); err != nil {
		t.Fatalf("la tercera pasada no debía fallar: %v", err)
	}

	if got := notifier.count(); got != 1 {
		t.Fatalf("el notificador recibió %d avisos, se esperaba 1", got)
	}
}

func TestEngineNotifiesTheDoubtAndLaterTheFailureOfTheSameOrder(t *testing.T) {
	machine := acceptedDrinkMachine()
	machine.status = dispenser.Status{State: linestate.InDoubt, Reason: "el proveedor no informa el pedido"}

	notifier := &recordingNotifier{}
	setup := newReconcileFixture(t, machine, order.WithNotifier(notifier))

	setup.clock.set(setup.line(t).ReconcileUntil)

	if err := setup.reconcileDue(t); err != nil {
		t.Fatalf("ReconcileDue: %v", err)
	}

	if got := setup.order(t).Status; got != store.OrderInDoubt {
		t.Fatalf("Status=%q, se esperaba %q", got, store.OrderInDoubt)
	}

	notifier.only(t)

	fx := &notifyFixture{engine: setup.engine, orders: setup.store, orderID: setup.placed.ID}
	if err := fx.progressLine(t, "batidos-2", "exchange-code-1"); err != nil {
		t.Fatalf("el callback de progreso no debía fallar: %v", err)
	}

	if got := notifier.count(); got != 1 {
		t.Fatalf("el notificador recibió %d avisos, se esperaba 1: la duda ya se había avisado", got)
	}

	if err := fx.failLine(t, "batidos", "batidos-3", "exchange-code-1"); err != nil {
		t.Fatalf("el callback de fallo no debía fallar: %v", err)
	}

	if got := setup.order(t).Status; got != store.OrderFailed {
		t.Fatalf("Status=%q, se esperaba %q", got, store.OrderFailed)
	}

	if err := fx.failLine(t, "batidos", "batidos-4", "exchange-code-1"); err != nil {
		t.Fatalf("el aviso repetido del fallo no debía devolver error: %v", err)
	}

	want := []string{"order_in_doubt", "order_failed"}
	if got := notifier.kinds(); !slices.Equal(got, want) {
		t.Fatalf("el notificador recibió %v, se esperaba %v: la duda y el fallo son dos avisos distintos y ninguno se repite", got, want)
	}
}

type failingNoticeStore struct {
	*store.Memory

	failure error
}

func (s *failingNoticeStore) NotificationSent(ctx context.Context, orderID, kind string) (bool, error) {
	if s.failure != nil {
		return false, s.failure
	}

	return s.Memory.NotificationSent(ctx, orderID, kind)
}

func TestEngineDoesNotNotifyWhenItCannotTellWhetherTheNoticeWentOut(t *testing.T) {
	orders := store.NewMemory()
	broken := &failingNoticeStore{Memory: orders}
	notifier := &recordingNotifier{}

	engine := order.New(broken, map[machine.Kind]dispenser.Dispenser{
		"batidos": &stubMachine{attempt: dispenser.DispatchResult{Accepted: true, ProviderRef: "code-batidos"}},
		"snacks":  &stubMachine{attempt: dispenser.DispatchResult{Accepted: true, ProviderRef: "sub-snacks"}},
	}, order.WithClock(frozenClock()), order.WithIDGenerator(sequentialIDs()), order.WithNotifier(notifier))

	placed := place(t, engine, comboDraft())

	if err := engine.ConfirmPayment(context.Background(), placed.ID, engineNow); err != nil {
		t.Fatalf("ConfirmPayment: %v", err)
	}

	broken.failure = errors.New("la base no respondió")

	fail := func(source, key, ref string) error {
		_, err := engine.CloseFromCallback(context.Background(), order.Callback{
			Source:    source,
			DedupeKey: key,
			OrderID:   placed.ID,
			Ref:       ref,
			State:     order.CallbackFailed,
			Reason:    "el canal no entregó el producto",
		})

		return err
	}

	if err := fail("batidos", "batidos-1", "code-batidos"); err != nil {
		t.Fatalf("el callback de una línea no debía fallar: %v", err)
	}

	if err := fail("snacks", "snacks-1", "sub-snacks"); err == nil {
		t.Fatal("el callback devolvió nil sin poder saber si el aviso ya había salido")
	}

	if got := notifier.count(); got != 0 {
		t.Fatalf("el notificador recibió %d avisos, se esperaba 0: sin respuesta del registro no se puede descartar un aviso repetido", got)
	}
}
