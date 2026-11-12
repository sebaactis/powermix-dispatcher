package order_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"compras-remotas/internal/linestate"
	"compras-remotas/internal/order"
	"compras-remotas/internal/store"
)

func buildLines(states ...linestate.State) []store.Line {
	lines := make([]store.Line, 0, len(states))
	for index, state := range states {
		lines = append(lines, store.Line{
			ID:       fmt.Sprintf("line-%d", index),
			OrderID:  "order-1",
			Position: index,
			State:    state,
		})
	}

	return lines
}

func TestDeriveOrderStatus(t *testing.T) {
	cases := []struct {
		name  string
		lines []store.Line
		want  store.OrderStatus
	}{
		{name: "orden sin líneas", lines: nil, want: store.OrderPendingPayment},
		{name: "orden con la lista de líneas vacía", lines: buildLines(), want: store.OrderPendingPayment},
		{name: "una línea pendiente", lines: buildLines(linestate.Pending), want: store.OrderDispatching},
		{name: "una línea enviada", lines: buildLines(linestate.Sent), want: store.OrderDispatching},
		{name: "una línea preparándose", lines: buildLines(linestate.Brewing), want: store.OrderDispatching},
		{name: "una línea entregada", lines: buildLines(linestate.Delivered), want: store.OrderDelivered},
		{
			name:  "las dos líneas entregadas",
			lines: buildLines(linestate.Delivered, linestate.Delivered),
			want:  store.OrderDelivered,
		},
		{name: "una línea fallada", lines: buildLines(linestate.Failed), want: store.OrderFailed},
		{name: "las dos líneas falladas", lines: buildLines(linestate.Failed, linestate.Failed), want: store.OrderFailed},
		{
			name:  "una entregada y la otra fallada",
			lines: buildLines(linestate.Delivered, linestate.Failed),
			want:  store.OrderPartial,
		},
		{
			name:  "una entregada y la otra en duda",
			lines: buildLines(linestate.Delivered, linestate.InDoubt),
			want:  store.OrderInDoubt,
		},
		{
			name:  "una en duda y la otra fallada",
			lines: buildLines(linestate.InDoubt, linestate.Failed),
			want:  store.OrderInDoubt,
		},
		{
			name:  "las dos en duda",
			lines: buildLines(linestate.InDoubt, linestate.InDoubt),
			want:  store.OrderInDoubt,
		},
		{
			name:  "una entregada mientras la otra sigue enviada",
			lines: buildLines(linestate.Delivered, linestate.Sent),
			want:  store.OrderDispatching,
		},
		{
			name:  "una entregada mientras la otra sigue pendiente",
			lines: buildLines(linestate.Delivered, linestate.Pending),
			want:  store.OrderDispatching,
		},
		{
			name:  "una entregada mientras la otra se prepara",
			lines: buildLines(linestate.Delivered, linestate.Brewing),
			want:  store.OrderDispatching,
		},
		{
			name:  "una en duda mientras la otra se prepara",
			lines: buildLines(linestate.InDoubt, linestate.Brewing),
			want:  store.OrderDispatching,
		},
		{
			name:  "una entregada, una fallada y una en duda",
			lines: buildLines(linestate.Delivered, linestate.Failed, linestate.InDoubt),
			want:  store.OrderPartial,
		},
		{
			name:  "una línea con un estado desconocido",
			lines: buildLines(linestate.State("refunded")),
			want:  store.OrderInDoubt,
		},
		{
			name:  "una entregada y la otra con un estado desconocido",
			lines: buildLines(linestate.Delivered, linestate.State("refunded")),
			want:  store.OrderInDoubt,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := order.DeriveOrderStatus(testCase.lines)
			if got != testCase.want {
				t.Fatalf("DeriveOrderStatus = %q, se esperaba %q", got, testCase.want)
			}
		})
	}
}

func TestDeriveOrderStatusOnlyProducesLineDerivedStatuses(t *testing.T) {
	derivable := []store.OrderStatus{
		store.OrderPendingPayment,
		store.OrderDispatching,
		store.OrderDelivered,
		store.OrderPartial,
		store.OrderFailed,
		store.OrderInDoubt,
	}

	inputs := [][]store.Line{
		nil,
		buildLines(),
		buildLines(linestate.Pending),
		buildLines(linestate.Brewing, linestate.Sent),
		buildLines(linestate.Delivered, linestate.Delivered),
		buildLines(linestate.Failed, linestate.Failed),
		buildLines(linestate.Delivered, linestate.Failed),
		buildLines(linestate.Delivered, linestate.InDoubt),
		buildLines(linestate.InDoubt, linestate.Failed),
		buildLines(linestate.State("refunded")),
	}

	for _, lines := range inputs {
		got := order.DeriveOrderStatus(lines)
		if !slices.Contains(derivable, got) {
			t.Fatalf("DeriveOrderStatus derivó %q, que no sale de las líneas", got)
		}
		if got == store.OrderRefunded || got == store.OrderCancelled {
			t.Fatalf("DeriveOrderStatus derivó %q, que solo fija el motor", got)
		}
	}
}

func TestCanOrderTransitionAllowsTheLegalEdges(t *testing.T) {
	edges := []struct {
		from store.OrderStatus
		to   store.OrderStatus
	}{
		{from: store.OrderPendingPayment, to: store.OrderPaid},
		{from: store.OrderPendingPayment, to: store.OrderCancelled},
		{from: store.OrderPaid, to: store.OrderDispatching},
		{from: store.OrderDispatching, to: store.OrderDelivered},
		{from: store.OrderDispatching, to: store.OrderPartial},
		{from: store.OrderDispatching, to: store.OrderFailed},
		{from: store.OrderDispatching, to: store.OrderInDoubt},
		{from: store.OrderInDoubt, to: store.OrderDelivered},
		{from: store.OrderInDoubt, to: store.OrderPartial},
		{from: store.OrderInDoubt, to: store.OrderFailed},
		{from: store.OrderInDoubt, to: store.OrderRefunded},
		{from: store.OrderFailed, to: store.OrderRefunded},
		{from: store.OrderPartial, to: store.OrderRefunded},
		{from: store.OrderDelivered, to: store.OrderRefunded},
	}

	for _, edge := range edges {
		if !order.CanOrderTransition(edge.from, edge.to) {
			t.Fatalf("CanOrderTransition(%q, %q) = false, se esperaba true", edge.from, edge.to)
		}
	}
}

func TestCanOrderTransitionRejectsIllegalEdges(t *testing.T) {
	edges := []struct {
		from store.OrderStatus
		to   store.OrderStatus
	}{
		{from: store.OrderPendingPayment, to: store.OrderPendingPayment},
		{from: store.OrderPendingPayment, to: store.OrderDispatching},
		{from: store.OrderPendingPayment, to: store.OrderDelivered},
		{from: store.OrderPendingPayment, to: store.OrderFailed},
		{from: store.OrderPendingPayment, to: store.OrderRefunded},
		{from: store.OrderPaid, to: store.OrderPaid},
		{from: store.OrderPaid, to: store.OrderCancelled},
		{from: store.OrderPaid, to: store.OrderDelivered},
		{from: store.OrderPaid, to: store.OrderFailed},
		{from: store.OrderDispatching, to: store.OrderPaid},
		{from: store.OrderDispatching, to: store.OrderDispatching},
		{from: store.OrderDispatching, to: store.OrderCancelled},
		{from: store.OrderDispatching, to: store.OrderRefunded},
		{from: store.OrderInDoubt, to: store.OrderDispatching},
		{from: store.OrderInDoubt, to: store.OrderInDoubt},
		{from: store.OrderInDoubt, to: store.OrderCancelled},
		{from: store.OrderFailed, to: store.OrderFailed},
		{from: store.OrderFailed, to: store.OrderDispatching},
		{from: store.OrderFailed, to: store.OrderPartial},
		{from: store.OrderFailed, to: store.OrderCancelled},
		{from: store.OrderPartial, to: store.OrderPartial},
		{from: store.OrderPartial, to: store.OrderDelivered},
		{from: store.OrderPartial, to: store.OrderFailed},
		{from: store.OrderDelivered, to: store.OrderDelivered},
		{from: store.OrderDelivered, to: store.OrderPartial},
		{from: store.OrderDelivered, to: store.OrderDispatching},
		{from: store.OrderDelivered, to: store.OrderCancelled},
	}

	for _, edge := range edges {
		if order.CanOrderTransition(edge.from, edge.to) {
			t.Fatalf("CanOrderTransition(%q, %q) = true, se esperaba false", edge.from, edge.to)
		}
	}
}

func TestCanOrderTransitionTreatsRefundedAndCancelledAsTerminal(t *testing.T) {
	all := []store.OrderStatus{
		store.OrderPendingPayment,
		store.OrderPaid,
		store.OrderDispatching,
		store.OrderDelivered,
		store.OrderPartial,
		store.OrderFailed,
		store.OrderInDoubt,
		store.OrderRefunded,
		store.OrderCancelled,
	}

	for _, terminal := range []store.OrderStatus{store.OrderRefunded, store.OrderCancelled} {
		for _, to := range all {
			if order.CanOrderTransition(terminal, to) {
				t.Fatalf("CanOrderTransition(%q, %q) = true, %q es terminal", terminal, to, terminal)
			}
		}
	}
}

func TestCanLineTransitionAllowsTheLegalEdges(t *testing.T) {
	edges := []struct {
		from linestate.State
		to   linestate.State
	}{
		{from: linestate.Pending, to: linestate.Sent},
		{from: linestate.Sent, to: linestate.Brewing},
		{from: linestate.Sent, to: linestate.Delivered},
		{from: linestate.Sent, to: linestate.Failed},
		{from: linestate.Sent, to: linestate.InDoubt},
		{from: linestate.Brewing, to: linestate.Delivered},
		{from: linestate.Brewing, to: linestate.Failed},
		{from: linestate.Brewing, to: linestate.InDoubt},
		{from: linestate.InDoubt, to: linestate.Delivered},
		{from: linestate.InDoubt, to: linestate.Failed},
	}

	for _, edge := range edges {
		if !order.CanLineTransition(edge.from, edge.to) {
			t.Fatalf("CanLineTransition(%q, %q) = false, se esperaba true", edge.from, edge.to)
		}
	}
}

func TestCanLineTransitionRejectsIllegalEdges(t *testing.T) {
	edges := []struct {
		from linestate.State
		to   linestate.State
	}{
		{from: linestate.Pending, to: linestate.Pending},
		{from: linestate.Pending, to: linestate.Brewing},
		{from: linestate.Pending, to: linestate.Delivered},
		{from: linestate.Pending, to: linestate.Failed},
		{from: linestate.Pending, to: linestate.InDoubt},
		{from: linestate.Sent, to: linestate.Pending},
		{from: linestate.Sent, to: linestate.Sent},
		{from: linestate.Brewing, to: linestate.Pending},
		{from: linestate.Brewing, to: linestate.Sent},
		{from: linestate.Brewing, to: linestate.Brewing},
		{from: linestate.Delivered, to: linestate.Pending},
		{from: linestate.Delivered, to: linestate.Sent},
		{from: linestate.Delivered, to: linestate.Brewing},
		{from: linestate.Delivered, to: linestate.Failed},
		{from: linestate.Delivered, to: linestate.InDoubt},
		{from: linestate.Delivered, to: linestate.Delivered},
		{from: linestate.Failed, to: linestate.Pending},
		{from: linestate.Failed, to: linestate.Sent},
		{from: linestate.Failed, to: linestate.Brewing},
		{from: linestate.Failed, to: linestate.Delivered},
		{from: linestate.Failed, to: linestate.InDoubt},
		{from: linestate.Failed, to: linestate.Failed},
		{from: linestate.InDoubt, to: linestate.Pending},
		{from: linestate.InDoubt, to: linestate.Sent},
		{from: linestate.InDoubt, to: linestate.Brewing},
		{from: linestate.InDoubt, to: linestate.InDoubt},
		{from: linestate.State("refunded"), to: linestate.Delivered},
		{from: linestate.Delivered, to: linestate.State("refunded")},
	}

	for _, edge := range edges {
		if order.CanLineTransition(edge.from, edge.to) {
			t.Fatalf("CanLineTransition(%q, %q) = true, se esperaba false", edge.from, edge.to)
		}
	}
}

func TestCanLineTransitionTreatsDeliveredAndFailedAsTerminal(t *testing.T) {
	all := []linestate.State{
		linestate.Pending,
		linestate.Sent,
		linestate.Brewing,
		linestate.Delivered,
		linestate.Failed,
		linestate.InDoubt,
	}

	for _, terminal := range []linestate.State{linestate.Delivered, linestate.Failed} {
		for _, to := range all {
			if order.CanLineTransition(terminal, to) {
				t.Fatalf("CanLineTransition(%q, %q) = true, %q es terminal", terminal, to, terminal)
			}
		}
	}
}

func TestAdvanceOrder(t *testing.T) {
	cases := []struct {
		name    string
		current store.OrderStatus
		lines   []store.Line
		want    store.OrderStatus
		wantErr error
	}{
		{
			name:    "pago confirmado con las dos líneas en vuelo",
			current: store.OrderPaid,
			lines:   buildLines(linestate.Pending, linestate.Sent),
			want:    store.OrderDispatching,
		},
		{
			name:    "despachando y todavía con líneas en vuelo no cambia nada",
			current: store.OrderDispatching,
			lines:   buildLines(linestate.Sent, linestate.Brewing),
			want:    store.OrderDispatching,
		},
		{
			name:    "despachando con las dos líneas entregadas cierra la orden",
			current: store.OrderDispatching,
			lines:   buildLines(linestate.Delivered, linestate.Delivered),
			want:    store.OrderDelivered,
		},
		{
			name:    "en duda que se resuelve en parcial",
			current: store.OrderInDoubt,
			lines:   buildLines(linestate.Delivered, linestate.Failed),
			want:    store.OrderPartial,
		},
		{
			name:    "en duda que se resuelve en fallada",
			current: store.OrderInDoubt,
			lines:   buildLines(linestate.Failed, linestate.Failed),
			want:    store.OrderFailed,
		},
		{
			name:    "entregada que se vuelve a derivar entregada",
			current: store.OrderDelivered,
			lines:   buildLines(linestate.Delivered, linestate.Delivered),
			want:    store.OrderDelivered,
		},
		{
			name:    "pago pendiente no puede saltear a entregada",
			current: store.OrderPendingPayment,
			lines:   buildLines(linestate.Delivered, linestate.Delivered),
			want:    store.OrderPendingPayment,
			wantErr: order.ErrInvalidTransition,
		},
		{
			name:    "fallada sin líneas no vuelve a pago pendiente",
			current: store.OrderFailed,
			lines:   nil,
			want:    store.OrderFailed,
			wantErr: order.ErrInvalidTransition,
		},
		{
			name:    "reembolsada es terminal",
			current: store.OrderRefunded,
			lines:   buildLines(linestate.Delivered, linestate.Delivered),
			want:    store.OrderRefunded,
			wantErr: order.ErrInvalidTransition,
		},
		{
			name:    "cancelada es terminal",
			current: store.OrderCancelled,
			lines:   buildLines(linestate.Pending, linestate.Pending),
			want:    store.OrderCancelled,
			wantErr: order.ErrInvalidTransition,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := order.AdvanceOrder(testCase.current, testCase.lines)
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("AdvanceOrder error = %v, se esperaba %v", err, testCase.wantErr)
			}
			if got != testCase.want {
				t.Fatalf("AdvanceOrder = %q, se esperaba %q", got, testCase.want)
			}
		})
	}
}

func TestAdvanceOrderKeepsTheCurrentStatusWhenItRejectsTheMove(t *testing.T) {
	got, err := order.AdvanceOrder(store.OrderPendingPayment, buildLines(linestate.Delivered))
	if !errors.Is(err, order.ErrInvalidTransition) {
		t.Fatalf("AdvanceOrder error = %v, se esperaba %v", err, order.ErrInvalidTransition)
	}
	if got != store.OrderPendingPayment {
		t.Fatalf("AdvanceOrder = %q, se esperaba el estado intacto %q", got, store.OrderPendingPayment)
	}
}

func TestAdvanceLine(t *testing.T) {
	cases := []struct {
		name    string
		current linestate.State
		next    linestate.State
		want    linestate.State
		wantErr error
	}{
		{
			name:    "pendiente a enviada",
			current: linestate.Pending,
			next:    linestate.Sent,
			want:    linestate.Sent,
		},
		{
			name:    "enviada a preparándose",
			current: linestate.Sent,
			next:    linestate.Brewing,
			want:    linestate.Brewing,
		},
		{
			name:    "enviada a entregada",
			current: linestate.Sent,
			next:    linestate.Delivered,
			want:    linestate.Delivered,
		},
		{
			name:    "enviada a en duda",
			current: linestate.Sent,
			next:    linestate.InDoubt,
			want:    linestate.InDoubt,
		},
		{
			name:    "en duda a entregada",
			current: linestate.InDoubt,
			next:    linestate.Delivered,
			want:    linestate.Delivered,
		},
		{
			name:    "en duda a fallada",
			current: linestate.InDoubt,
			next:    linestate.Failed,
			want:    linestate.Failed,
		},
		{
			name:    "enviada a enviada no cambia nada",
			current: linestate.Sent,
			next:    linestate.Sent,
			want:    linestate.Sent,
		},
		{
			name:    "entregada no vuelve a en duda",
			current: linestate.Delivered,
			next:    linestate.InDoubt,
			want:    linestate.Delivered,
			wantErr: order.ErrInvalidTransition,
		},
		{
			name:    "fallada no vuelve a enviada",
			current: linestate.Failed,
			next:    linestate.Sent,
			want:    linestate.Failed,
			wantErr: order.ErrInvalidTransition,
		},
		{
			name:    "en duda no vuelve a pendiente",
			current: linestate.InDoubt,
			next:    linestate.Pending,
			want:    linestate.InDoubt,
			wantErr: order.ErrInvalidTransition,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := order.AdvanceLine(testCase.current, testCase.next)
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("AdvanceLine error = %v, se esperaba %v", err, testCase.wantErr)
			}
			if got != testCase.want {
				t.Fatalf("AdvanceLine = %q, se esperaba %q", got, testCase.want)
			}
		})
	}
}
