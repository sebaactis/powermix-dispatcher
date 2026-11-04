package order_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/order"
	"compras-remotas/internal/store"
)

func buildLines(states ...dispenser.LineState) []store.Line {
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
		{name: "una línea pendiente", lines: buildLines(dispenser.LinePending), want: store.OrderDispatching},
		{name: "una línea enviada", lines: buildLines(dispenser.LineSent), want: store.OrderDispatching},
		{name: "una línea preparándose", lines: buildLines(dispenser.LineBrewing), want: store.OrderDispatching},
		{name: "una línea entregada", lines: buildLines(dispenser.LineDelivered), want: store.OrderDelivered},
		{
			name:  "las dos líneas entregadas",
			lines: buildLines(dispenser.LineDelivered, dispenser.LineDelivered),
			want:  store.OrderDelivered,
		},
		{name: "una línea fallada", lines: buildLines(dispenser.LineFailed), want: store.OrderFailed},
		{name: "las dos líneas falladas", lines: buildLines(dispenser.LineFailed, dispenser.LineFailed), want: store.OrderFailed},
		{
			name:  "una entregada y la otra fallada",
			lines: buildLines(dispenser.LineDelivered, dispenser.LineFailed),
			want:  store.OrderPartial,
		},
		{
			name:  "una entregada y la otra en duda",
			lines: buildLines(dispenser.LineDelivered, dispenser.LineInDoubt),
			want:  store.OrderInDoubt,
		},
		{
			name:  "una en duda y la otra fallada",
			lines: buildLines(dispenser.LineInDoubt, dispenser.LineFailed),
			want:  store.OrderInDoubt,
		},
		{
			name:  "las dos en duda",
			lines: buildLines(dispenser.LineInDoubt, dispenser.LineInDoubt),
			want:  store.OrderInDoubt,
		},
		{
			name:  "una entregada mientras la otra sigue enviada",
			lines: buildLines(dispenser.LineDelivered, dispenser.LineSent),
			want:  store.OrderDispatching,
		},
		{
			name:  "una entregada mientras la otra sigue pendiente",
			lines: buildLines(dispenser.LineDelivered, dispenser.LinePending),
			want:  store.OrderDispatching,
		},
		{
			name:  "una entregada mientras la otra se prepara",
			lines: buildLines(dispenser.LineDelivered, dispenser.LineBrewing),
			want:  store.OrderDispatching,
		},
		{
			name:  "una en duda mientras la otra se prepara",
			lines: buildLines(dispenser.LineInDoubt, dispenser.LineBrewing),
			want:  store.OrderDispatching,
		},
		{
			name:  "una entregada, una fallada y una en duda",
			lines: buildLines(dispenser.LineDelivered, dispenser.LineFailed, dispenser.LineInDoubt),
			want:  store.OrderPartial,
		},
		{
			name:  "una línea con un estado desconocido",
			lines: buildLines(dispenser.LineState("refunded")),
			want:  store.OrderInDoubt,
		},
		{
			name:  "una entregada y la otra con un estado desconocido",
			lines: buildLines(dispenser.LineDelivered, dispenser.LineState("refunded")),
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
		buildLines(dispenser.LinePending),
		buildLines(dispenser.LineBrewing, dispenser.LineSent),
		buildLines(dispenser.LineDelivered, dispenser.LineDelivered),
		buildLines(dispenser.LineFailed, dispenser.LineFailed),
		buildLines(dispenser.LineDelivered, dispenser.LineFailed),
		buildLines(dispenser.LineDelivered, dispenser.LineInDoubt),
		buildLines(dispenser.LineInDoubt, dispenser.LineFailed),
		buildLines(dispenser.LineState("refunded")),
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
		from dispenser.LineState
		to   dispenser.LineState
	}{
		{from: dispenser.LinePending, to: dispenser.LineSent},
		{from: dispenser.LineSent, to: dispenser.LineBrewing},
		{from: dispenser.LineSent, to: dispenser.LineDelivered},
		{from: dispenser.LineSent, to: dispenser.LineFailed},
		{from: dispenser.LineSent, to: dispenser.LineInDoubt},
		{from: dispenser.LineBrewing, to: dispenser.LineDelivered},
		{from: dispenser.LineBrewing, to: dispenser.LineFailed},
		{from: dispenser.LineBrewing, to: dispenser.LineInDoubt},
		{from: dispenser.LineInDoubt, to: dispenser.LineDelivered},
		{from: dispenser.LineInDoubt, to: dispenser.LineFailed},
		{from: dispenser.LineFailed, to: dispenser.LineSent},
	}

	for _, edge := range edges {
		if !order.CanLineTransition(edge.from, edge.to) {
			t.Fatalf("CanLineTransition(%q, %q) = false, se esperaba true", edge.from, edge.to)
		}
	}
}

func TestCanLineTransitionRejectsIllegalEdges(t *testing.T) {
	edges := []struct {
		from dispenser.LineState
		to   dispenser.LineState
	}{
		{from: dispenser.LinePending, to: dispenser.LinePending},
		{from: dispenser.LinePending, to: dispenser.LineBrewing},
		{from: dispenser.LinePending, to: dispenser.LineDelivered},
		{from: dispenser.LinePending, to: dispenser.LineFailed},
		{from: dispenser.LinePending, to: dispenser.LineInDoubt},
		{from: dispenser.LineSent, to: dispenser.LinePending},
		{from: dispenser.LineSent, to: dispenser.LineSent},
		{from: dispenser.LineBrewing, to: dispenser.LinePending},
		{from: dispenser.LineBrewing, to: dispenser.LineSent},
		{from: dispenser.LineBrewing, to: dispenser.LineBrewing},
		{from: dispenser.LineDelivered, to: dispenser.LinePending},
		{from: dispenser.LineDelivered, to: dispenser.LineSent},
		{from: dispenser.LineDelivered, to: dispenser.LineBrewing},
		{from: dispenser.LineDelivered, to: dispenser.LineFailed},
		{from: dispenser.LineDelivered, to: dispenser.LineInDoubt},
		{from: dispenser.LineDelivered, to: dispenser.LineDelivered},
		{from: dispenser.LineFailed, to: dispenser.LinePending},
		{from: dispenser.LineFailed, to: dispenser.LineBrewing},
		{from: dispenser.LineFailed, to: dispenser.LineDelivered},
		{from: dispenser.LineFailed, to: dispenser.LineInDoubt},
		{from: dispenser.LineFailed, to: dispenser.LineFailed},
		{from: dispenser.LineInDoubt, to: dispenser.LinePending},
		{from: dispenser.LineInDoubt, to: dispenser.LineSent},
		{from: dispenser.LineInDoubt, to: dispenser.LineBrewing},
		{from: dispenser.LineInDoubt, to: dispenser.LineInDoubt},
		{from: dispenser.LineState("refunded"), to: dispenser.LineDelivered},
		{from: dispenser.LineDelivered, to: dispenser.LineState("refunded")},
	}

	for _, edge := range edges {
		if order.CanLineTransition(edge.from, edge.to) {
			t.Fatalf("CanLineTransition(%q, %q) = true, se esperaba false", edge.from, edge.to)
		}
	}
}

func TestCanLineTransitionTreatsOnlyDeliveredAsTerminal(t *testing.T) {
	all := []dispenser.LineState{
		dispenser.LinePending,
		dispenser.LineSent,
		dispenser.LineBrewing,
		dispenser.LineDelivered,
		dispenser.LineFailed,
		dispenser.LineInDoubt,
	}

	terminal := dispenser.LineDelivered

	for _, to := range all {
		if order.CanLineTransition(terminal, to) {
			t.Fatalf("CanLineTransition(%q, %q) = true, %q es terminal", terminal, to, terminal)
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
			lines:   buildLines(dispenser.LinePending, dispenser.LineSent),
			want:    store.OrderDispatching,
		},
		{
			name:    "despachando y todavía con líneas en vuelo no cambia nada",
			current: store.OrderDispatching,
			lines:   buildLines(dispenser.LineSent, dispenser.LineBrewing),
			want:    store.OrderDispatching,
		},
		{
			name:    "despachando con las dos líneas entregadas cierra la orden",
			current: store.OrderDispatching,
			lines:   buildLines(dispenser.LineDelivered, dispenser.LineDelivered),
			want:    store.OrderDelivered,
		},
		{
			name:    "en duda que se resuelve en parcial",
			current: store.OrderInDoubt,
			lines:   buildLines(dispenser.LineDelivered, dispenser.LineFailed),
			want:    store.OrderPartial,
		},
		{
			name:    "en duda que se resuelve en fallada",
			current: store.OrderInDoubt,
			lines:   buildLines(dispenser.LineFailed, dispenser.LineFailed),
			want:    store.OrderFailed,
		},
		{
			name:    "entregada que se vuelve a derivar entregada",
			current: store.OrderDelivered,
			lines:   buildLines(dispenser.LineDelivered, dispenser.LineDelivered),
			want:    store.OrderDelivered,
		},
		{
			name:    "pago pendiente no puede saltear a entregada",
			current: store.OrderPendingPayment,
			lines:   buildLines(dispenser.LineDelivered, dispenser.LineDelivered),
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
			lines:   buildLines(dispenser.LineDelivered, dispenser.LineDelivered),
			want:    store.OrderRefunded,
			wantErr: order.ErrInvalidTransition,
		},
		{
			name:    "cancelada es terminal",
			current: store.OrderCancelled,
			lines:   buildLines(dispenser.LinePending, dispenser.LinePending),
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
	got, err := order.AdvanceOrder(store.OrderPendingPayment, buildLines(dispenser.LineDelivered))
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
		current dispenser.LineState
		next    dispenser.LineState
		want    dispenser.LineState
		wantErr error
	}{
		{
			name:    "pendiente a enviada",
			current: dispenser.LinePending,
			next:    dispenser.LineSent,
			want:    dispenser.LineSent,
		},
		{
			name:    "enviada a preparándose",
			current: dispenser.LineSent,
			next:    dispenser.LineBrewing,
			want:    dispenser.LineBrewing,
		},
		{
			name:    "enviada a entregada",
			current: dispenser.LineSent,
			next:    dispenser.LineDelivered,
			want:    dispenser.LineDelivered,
		},
		{
			name:    "enviada a en duda",
			current: dispenser.LineSent,
			next:    dispenser.LineInDoubt,
			want:    dispenser.LineInDoubt,
		},
		{
			name:    "en duda a entregada",
			current: dispenser.LineInDoubt,
			next:    dispenser.LineDelivered,
			want:    dispenser.LineDelivered,
		},
		{
			name:    "en duda a fallada",
			current: dispenser.LineInDoubt,
			next:    dispenser.LineFailed,
			want:    dispenser.LineFailed,
		},
		{
			name:    "enviada a enviada no cambia nada",
			current: dispenser.LineSent,
			next:    dispenser.LineSent,
			want:    dispenser.LineSent,
		},
		{
			name:    "entregada no vuelve a en duda",
			current: dispenser.LineDelivered,
			next:    dispenser.LineInDoubt,
			want:    dispenser.LineDelivered,
			wantErr: order.ErrInvalidTransition,
		},
		{
			name:    "fallada vuelve a enviada para reintentar",
			current: dispenser.LineFailed,
			next:    dispenser.LineSent,
			want:    dispenser.LineSent,
		},
		{
			name:    "en duda no vuelve a pendiente",
			current: dispenser.LineInDoubt,
			next:    dispenser.LinePending,
			want:    dispenser.LineInDoubt,
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
