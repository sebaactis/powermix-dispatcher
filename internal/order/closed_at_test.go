package order_test

import (
	"testing"
	"time"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/linestate"
	"compras-remotas/internal/store"
)

func TestOrderClosesWhenItSettles(t *testing.T) {
	cases := []struct {
		name    string
		settle  func(t *testing.T, fx *notifyFixture)
		wantEnd store.OrderStatus
	}{
		{
			name: "entregada",
			settle: func(t *testing.T, fx *notifyFixture) {
				mustNoErr(t, fx.deliverLine(t, "batidos", "batidos-1", "code-batidos"))
				mustNoErr(t, fx.deliverLine(t, "snacks", "snacks-1", "sub-snacks"))
			},
			wantEnd: store.OrderDelivered,
		},
		{
			name: "parcial",
			settle: func(t *testing.T, fx *notifyFixture) {
				mustNoErr(t, fx.deliverLine(t, "batidos", "batidos-1", "code-batidos"))
				mustNoErr(t, fx.failLine(t, "snacks", "snacks-1", "sub-snacks"))
			},
			wantEnd: store.OrderPartial,
		},
		{
			name: "fallida",
			settle: func(t *testing.T, fx *notifyFixture) {
				mustNoErr(t, fx.failLine(t, "batidos", "batidos-1", "code-batidos"))
				mustNoErr(t, fx.failLine(t, "snacks", "snacks-1", "sub-snacks"))
			},
			wantEnd: store.OrderFailed,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newNotifyFixture(t, &recordingNotifier{})

			if got := mustOrder(t, fx.orders, fx.orderID).ClosedAt; !got.IsZero() {
				t.Fatalf("ClosedAt=%v antes de cerrar, se esperaba vacío", got)
			}

			tc.settle(t, fx)

			loaded := mustOrder(t, fx.orders, fx.orderID)
			if loaded.Status != tc.wantEnd {
				t.Fatalf("Status=%q, se esperaba %q", loaded.Status, tc.wantEnd)
			}
			if !loaded.ClosedAt.Equal(engineNow) {
				t.Fatalf("ClosedAt=%v, se esperaba %v", loaded.ClosedAt, engineNow)
			}
		})
	}
}

func TestOrderInDoubtStaysOpenUntilItResolves(t *testing.T) {
	machine := acceptedDrinkMachine()
	machine.status = dispenser.Status{State: linestate.InDoubt, Reason: "el proveedor no informa el pedido"}

	setup := newReconcileFixture(t, machine)
	setup.clock.set(setup.line(t).ReconcileUntil)

	if err := setup.reconcileDue(t); err != nil {
		t.Fatalf("SyncOverdueLines: %v", err)
	}

	doubtful := setup.order(t)
	if doubtful.Status != store.OrderInDoubt {
		t.Fatalf("Status=%q, se esperaba %q", doubtful.Status, store.OrderInDoubt)
	}
	if !doubtful.ClosedAt.IsZero() {
		t.Fatalf("ClosedAt=%v: una orden en duda todavía no está cerrada", doubtful.ClosedAt)
	}

	resolvedAt := setup.line(t).ReconcileUntil.Add(10 * time.Minute)
	setup.clock.set(resolvedAt)

	fx := &notifyFixture{engine: setup.engine, orders: setup.store, orderID: setup.placed.ID}
	mustNoErr(t, fx.deliverLine(t, "batidos", "batidos-2", "exchange-code-1"))

	resolved := setup.order(t)
	if resolved.Status != store.OrderDelivered {
		t.Fatalf("Status=%q, se esperaba %q", resolved.Status, store.OrderDelivered)
	}
	if !resolved.ClosedAt.Equal(resolvedAt) {
		t.Fatalf("ClosedAt=%v, se esperaba la hora en que se resolvió la duda (%v)", resolved.ClosedAt, resolvedAt)
	}
}

func mustNoErr(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
}
