package order

import (
	"context"
	"errors"
	"fmt"

	"compras-remotas/internal/notify"
	"compras-remotas/internal/store"
)

const reasonReconcileExpired = "se agotó el plazo de reconciliación sin confirmar la entrega"

func (e *Engine) settleOrder(ctx context.Context, orderID string) error {
	settled, err := e.refreshOrderStatus(ctx, orderID)
	if err != nil {
		return err
	}

	return e.notifyOrder(ctx, settled)
}

func (e *Engine) refreshOrderStatus(ctx context.Context, orderID string) (store.Order, error) {
	for attempt := 0; ; attempt++ {
		loaded, err := e.store.LoadOrder(ctx, orderID)
		if err != nil {
			return store.Order{}, err
		}

		next, err := AdvanceOrder(loaded.Status, loaded.Lines)
		if err != nil {
			return store.Order{}, fmt.Errorf("orden %s: %w", orderID, err)
		}

		if next == loaded.Status {
			return loaded, nil
		}

		loaded.Status = next

		saved, err := e.store.SaveOrder(ctx, loaded)
		if err == nil {
			return saved, nil
		}

		if !errors.Is(err, store.ErrStale) || attempt > 0 {
			return store.Order{}, err
		}
	}
}

func (e *Engine) notifyOrder(ctx context.Context, loaded store.Order) error {
	if e.notifier == nil {
		return nil
	}

	event := notify.Event{OrderID: loaded.ID}

	switch loaded.Status {
	case store.OrderInDoubt:
		event.Kind = notify.KindOrderInDoubt
		event.Detail = "el pedido quedó en duda: no se pudo confirmar la entrega y hace falta una persona"
	case store.OrderFailed:
		event.Kind = notify.KindOrderFailed
		event.Detail = "el pedido quedó fallido: el proveedor informó que no hubo entrega"
	default:
		return nil
	}

	return e.notifier.Notify(ctx, event)
}
