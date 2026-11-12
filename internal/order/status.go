package order

import (
	"errors"

	"compras-remotas/internal/linestate"
	"compras-remotas/internal/store"
)

var ErrInvalidTransition = errors.New("transición de estado inválida")

func DeriveOrderStatus(lines []store.Line) store.OrderStatus {
	if len(lines) == 0 {
		return store.OrderPendingPayment
	}

	var delivered, failed int

	for _, line := range lines {
		switch line.State {
		case linestate.Pending, linestate.Sent, linestate.Brewing:
			return store.OrderDispatching
		case linestate.Delivered:
			delivered++
		case linestate.Failed:
			failed++
		}
	}

	if delivered == len(lines) {
		return store.OrderDelivered
	}

	if delivered == 0 {
		if failed == len(lines) {
			return store.OrderFailed
		}

		return store.OrderInDoubt
	}

	if failed > 0 {
		return store.OrderPartial
	}

	return store.OrderInDoubt
}

func CanOrderTransition(from, to store.OrderStatus) bool {
	switch from {
	case store.OrderPendingPayment:
		return to == store.OrderPaid || to == store.OrderCancelled
	case store.OrderPaid:
		return to == store.OrderDispatching
	case store.OrderDispatching:
		return to == store.OrderDelivered ||
			to == store.OrderPartial ||
			to == store.OrderFailed ||
			to == store.OrderInDoubt
	case store.OrderInDoubt:
		return to == store.OrderDelivered ||
			to == store.OrderPartial ||
			to == store.OrderFailed ||
			to == store.OrderRefunded
	case store.OrderFailed, store.OrderPartial, store.OrderDelivered:
		return to == store.OrderRefunded
	default:
		return false
	}
}

func CanLineTransition(from, to linestate.State) bool {
	switch from {
	case linestate.Pending:
		return to == linestate.Sent
	case linestate.Sent:
		return to == linestate.Brewing ||
			to == linestate.Delivered ||
			to == linestate.Failed ||
			to == linestate.InDoubt
	case linestate.Brewing:
		return to == linestate.Delivered ||
			to == linestate.Failed ||
			to == linestate.InDoubt
	case linestate.InDoubt:
		return to == linestate.Delivered || to == linestate.Failed
	default:
		return false
	}
}

func AdvanceOrder(current store.OrderStatus, lines []store.Line) (store.OrderStatus, error) {
	next := DeriveOrderStatus(lines)
	if next == current {
		return current, nil
	}

	if !CanOrderTransition(current, next) {
		return current, ErrInvalidTransition
	}

	return next, nil
}

func AdvanceLine(current, next linestate.State) (linestate.State, error) {
	if next == current {
		return current, nil
	}

	if !CanLineTransition(current, next) {
		return current, ErrInvalidTransition
	}

	return next, nil
}
