package order

import (
	"context"
	"errors"
	"sync"
	"time"

	"compras-remotas/internal/store"
)

func (e *Engine) MarkPaid(ctx context.Context, orderID string, at time.Time) error {
	loaded, started, err := e.claimPaid(ctx, orderID, at)
	if err != nil || !started {
		return err
	}

	return e.dispatchLines(context.WithoutCancel(ctx), orderID, loaded.Lines)
}

func (e *Engine) claimPaid(ctx context.Context, orderID string, at time.Time) (store.Order, bool, error) {
	loaded, err := e.store.LoadOrder(ctx, orderID)
	if err != nil {
		return store.Order{}, false, err
	}

	if loaded.Status != store.OrderPendingPayment {
		return loaded, false, nil
	}

	paid := loaded
	paid.Status = store.OrderPaid
	paid.PaidAt = at

	paid, err = e.store.SaveOrder(ctx, paid)
	if err != nil {
		if errors.Is(err, store.ErrStale) {
			return loaded, false, nil
		}

		return store.Order{}, false, err
	}

	dispatching, err := AdvanceOrder(paid.Status, loaded.Lines)
	if err != nil {
		return store.Order{}, false, err
	}

	paid.Status = dispatching

	if _, err := e.store.SaveOrder(ctx, paid); err != nil {
		return store.Order{}, false, err
	}

	return loaded, true, nil
}

func (e *Engine) dispatchLines(ctx context.Context, orderID string, lines []store.Line) error {
	failures := make([]error, len(lines))

	var wg sync.WaitGroup
	wg.Add(len(lines))

	for i := range lines {
		go func(i int) {
			defer wg.Done()

			failures[i] = e.dispatchWithinDeadline(ctx, orderID, lines[i])
		}(i)
	}

	wg.Wait()

	joined := errors.Join(failures...)

	if err := e.settleOrder(ctx, orderID); err != nil {
		return errors.Join(joined, err)
	}

	return joined
}
