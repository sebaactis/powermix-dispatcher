package order

import (
	"errors"

	"compras-remotas/internal/dispenser"
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
		case dispenser.LinePending, dispenser.LineSent, dispenser.LineBrewing:
			return store.OrderDispatching
		case dispenser.LineDelivered:
			delivered++
		case dispenser.LineFailed:
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

func CanLineTransition(from, to dispenser.LineState) bool {
	switch from {
	case dispenser.LinePending:
		return to == dispenser.LineSent
	case dispenser.LineSent:
		return to == dispenser.LineBrewing ||
			to == dispenser.LineDelivered ||
			to == dispenser.LineFailed ||
			to == dispenser.LineInDoubt
	case dispenser.LineBrewing:
		return to == dispenser.LineDelivered ||
			to == dispenser.LineFailed ||
			to == dispenser.LineInDoubt
	case dispenser.LineInDoubt:
		return to == dispenser.LineDelivered || to == dispenser.LineFailed
	case dispenser.LineFailed:
		return to == dispenser.LineSent
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

func AdvanceLine(current, next dispenser.LineState) (dispenser.LineState, error) {
	if next == current {
		return current, nil
	}

	if !CanLineTransition(current, next) {
		return current, ErrInvalidTransition
	}

	return next, nil
}
