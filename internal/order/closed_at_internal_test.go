package order

import (
	"context"
	"testing"
	"time"

	"compras-remotas/internal/payments"
	"compras-remotas/internal/store"
)

func TestCancelledOrderClosesAtTheClock(t *testing.T) {
	rig := newChargeRig(t)
	placed := rig.place(t, "a")
	intent := rig.qr(t, placed.ID)
	rig.gateway.set(intent.ProviderID, payments.ChargeExpired, 0)

	if err := rig.engine.SyncPayment(context.Background(), intent.ProviderID); err != nil {
		t.Fatalf("SyncPayment: %v", err)
	}

	loaded := rig.order(t, placed.ID)
	if loaded.Status != store.OrderCancelled {
		t.Fatalf("Status=%q, se esperaba %q", loaded.Status, store.OrderCancelled)
	}
	if !loaded.ClosedAt.Equal(rig.now) {
		t.Fatalf("ClosedAt=%v, se esperaba %v", loaded.ClosedAt, rig.now)
	}
}

func TestMarkClosed(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	earlier := now.Add(-time.Hour)
	engine := New(store.NewMemory(), nil, WithClock(func() time.Time { return now }))

	cases := []struct {
		name   string
		status store.OrderStatus
		closed time.Time
		want   time.Time
	}{
		{"entregada", store.OrderDelivered, time.Time{}, now},
		{"parcial", store.OrderPartial, time.Time{}, now},
		{"fallida", store.OrderFailed, time.Time{}, now},
		{"cancelada", store.OrderCancelled, time.Time{}, now},
		{"ya cerrada no se pisa", store.OrderFailed, earlier, earlier},
		{"devuelta conserva el cierre", store.OrderRefunded, earlier, earlier},
		{"en duda sigue abierta", store.OrderInDoubt, time.Time{}, time.Time{}},
		{"despachando sigue abierta", store.OrderDispatching, time.Time{}, time.Time{}},
		{"pagada sigue abierta", store.OrderPaid, time.Time{}, time.Time{}},
		{"esperando pago sigue abierta", store.OrderPendingPayment, time.Time{}, time.Time{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loaded := store.Order{Status: tc.status, ClosedAt: tc.closed}
			engine.markClosed(&loaded)
			if !loaded.ClosedAt.Equal(tc.want) {
				t.Fatalf("ClosedAt=%v, se esperaba %v", loaded.ClosedAt, tc.want)
			}
		})
	}
}
