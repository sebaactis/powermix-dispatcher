package order

import (
	"context"
	"fmt"

	"compras-remotas/internal/notify"
	"compras-remotas/internal/store"
)

const (
	kindOrderInDoubt = "order_in_doubt"
	kindOrderFailed  = "order_failed"
)

const reasonReconcileExpired = "se agotó el plazo de reconciliación sin confirmar la entrega"

func (e *Engine) settleOrder(ctx context.Context, orderID string) error {
	loaded, err := e.store.LoadOrder(ctx, orderID)
	if err != nil {
		return err
	}

	next, err := AdvanceOrder(loaded.Status, loaded.Lines)
	if err != nil {
		return fmt.Errorf("orden %s: %w", orderID, err)
	}

	if next != loaded.Status {
		loaded.Status = next

		if err := e.store.SaveOrder(ctx, loaded); err != nil {
			return err
		}
	}

	return e.notifyOrder(ctx, loaded)
}

func (e *Engine) notifyOrder(ctx context.Context, loaded store.Order) error {
	if e.notifier == nil {
		return nil
	}

	event := notify.Event{OrderID: loaded.ID}

	switch loaded.Status {
	case store.OrderInDoubt:
		event.Kind = kindOrderInDoubt
		event.Detail = "el pedido quedó en duda: no se pudo confirmar la entrega y hace falta una persona"
	case store.OrderFailed:
		event.Kind = kindOrderFailed
		event.Detail = "el pedido quedó fallido: el proveedor informó que no hubo entrega"
	default:
		return nil
	}

	return e.notifier.Notify(ctx, event)
}
