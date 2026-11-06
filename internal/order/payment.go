package order

import (
	"context"
	"errors"
	"sync"
	"time"

	"compras-remotas/internal/store"
)

func (e *Engine) ConfirmPayment(ctx context.Context, orderID string, at time.Time) error {
	loaded, err := e.store.LoadOrder(ctx, orderID)
	if err != nil {
		return err
	}

	if loaded.Status != store.OrderPendingPayment {
		return nil
	}

	loaded.Status = store.OrderPaid
	loaded.PaidAt = at

	if err := e.store.SaveOrder(ctx, loaded); err != nil {
		return err
	}

	dispatching, err := AdvanceOrder(loaded.Status, loaded.Lines)
	if err != nil {
		return err
	}

	loaded.Status = dispatching

	if err := e.store.SaveOrder(ctx, loaded); err != nil {
		return err
	}

	lines := loaded.Lines
	failures := make([]error, len(lines))

	var wg sync.WaitGroup
	wg.Add(len(lines))

	for i := range lines {
		go func(i int) {
			defer wg.Done()

			failures[i] = e.dispatchLine(ctx, orderID, lines[i])
		}(i)
	}

	wg.Wait()

	joined := errors.Join(failures...)

	reloaded, err := e.store.LoadOrder(ctx, orderID)
	if err != nil {
		return errors.Join(joined, err)
	}

	next, err := AdvanceOrder(reloaded.Status, reloaded.Lines)
	if err != nil {
		return errors.Join(joined, err)
	}

	reloaded.Status = next

	if err := e.store.SaveOrder(ctx, reloaded); err != nil {
		return errors.Join(joined, err)
	}

	return joined
}
