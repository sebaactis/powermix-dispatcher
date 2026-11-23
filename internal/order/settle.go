package order

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"compras-remotas/internal/linestate"
	"compras-remotas/internal/notify"
	"compras-remotas/internal/store"
)

const reasonReconcileExpired = "se agotó el plazo de reconciliación sin confirmar la entrega"

const maxSettleAttempts = 3

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
		e.markClosed(&loaded)

		saved, err := e.store.SaveOrder(ctx, loaded)
		if err == nil {
			return saved, nil
		}

		if !errors.Is(err, store.ErrStale) || attempt+1 >= maxSettleAttempts {
			return store.Order{}, err
		}
	}
}

func (e *Engine) notifyOrder(ctx context.Context, loaded store.Order) error {
	if e.notifier == nil {
		return nil
	}

	event := notify.Event{OrderID: loaded.ID, KioskID: loaded.KioskID}

	switch loaded.Status {
	case store.OrderInDoubt:
		event.Kind = notify.KindOrderInDoubt
		event.Detail = "el pedido quedó en duda: no se pudo confirmar la entrega y hace falta una persona"
	case store.OrderFailed:
		event.Kind = notify.KindOrderFailed
		event.Detail = "el pedido quedó fallido: el proveedor informó que no hubo entrega"
	case store.OrderPartial:
		event.Kind = notify.KindOrderPartial
		event.Detail = partialOrderDetail(loaded.Lines)
	default:
		return nil
	}

	return e.notifyOnce(ctx, event)
}

func partialOrderDetail(lines []store.Line) string {
	var delivered, failed, inDoubt, compensate []string

	for _, line := range lines {
		label := lineLabel(line)
		switch line.State {
		case linestate.Delivered:
			delivered = append(delivered, label)
		case linestate.Failed:
			failed = append(failed, label)
			compensate = append(compensate, fmt.Sprintf("%s (%s)", label, notify.Pesos(line.AmountCents)))
		case linestate.InDoubt:
			inDoubt = append(inDoubt, label)
		}
	}

	var parts []string
	if len(delivered) > 0 {
		parts = append(parts, joinVerb(len(delivered), "se entregó", "se entregaron")+" "+strings.Join(delivered, ", "))
	}
	if len(failed) > 0 {
		parts = append(parts, joinVerb(len(failed), "falló", "fallaron")+" "+strings.Join(failed, ", "))
	}
	if len(inDoubt) > 0 {
		parts = append(parts, joinVerb(len(inDoubt), "quedó en duda", "quedaron en duda")+" "+strings.Join(inDoubt, ", "))
	}

	summary := strings.Join(parts, ", ")
	if len(compensate) == 0 {
		return summary
	}

	return summary + ": compensar al cliente por " + strings.Join(compensate, ", ")
}

func lineLabel(line store.Line) string {
	if line.Name != "" {
		return line.Name
	}

	if line.SKU == "" {
		return string(line.Machine.Kind)
	}

	return string(line.Machine.Kind) + "/" + line.SKU
}

func joinVerb(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}

	return plural
}

func (e *Engine) notifyOnce(ctx context.Context, event notify.Event) error {
	if e.notifier == nil {
		return nil
	}

	sent, err := e.store.NotificationSent(ctx, event.OrderID, string(event.Kind))
	if err != nil {
		return err
	}
	if sent {
		return nil
	}

	if event.KioskID == "" {
		if loaded, err := e.store.LoadOrder(ctx, event.OrderID); err == nil {
			event.KioskID = loaded.KioskID
		}
	}

	if err := e.notifier.Notify(ctx, event); err != nil {
		return err
	}

	return e.store.RecordNotification(ctx, event.OrderID, string(event.Kind), e.now())
}
