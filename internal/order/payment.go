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

	paid := loaded
	paid.Status = store.OrderPaid
	paid.PaidAt = at

	paid, err = e.store.SaveOrder(ctx, paid)
	if err != nil {
		if errors.Is(err, store.ErrStale) {
			return nil
		}

		return err
	}

	dispatching, err := AdvanceOrder(paid.Status, loaded.Lines)
	if err != nil {
		return err
	}

	paid.Status = dispatching

	if _, err := e.store.SaveOrder(ctx, paid); err != nil {
		return err
	}

	lines := loaded.Lines
	detached := context.WithoutCancel(ctx)
	failures := make([]error, len(lines))

	var wg sync.WaitGroup
	wg.Add(len(lines))

	for i := range lines {
		go func(i int) {
			defer wg.Done()

			failures[i] = e.dispatchWithinDeadline(detached, orderID, lines[i])
		}(i)
	}

	wg.Wait()

	joined := errors.Join(failures...)

	if _, err := e.refreshOrderStatus(detached, orderID); err != nil {
		return errors.Join(joined, err)
	}

	return joined
}
