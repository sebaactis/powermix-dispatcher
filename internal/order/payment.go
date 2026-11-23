package order

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"compras-remotas/internal/linestate"
	"compras-remotas/internal/notify"
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
	panicked := make([]bool, len(lines))

	var wg sync.WaitGroup
	wg.Add(len(lines))

	for i := range lines {
		go func(i int) {
			defer wg.Done()

			panicked[i], failures[i] = e.dispatchRecovering(ctx, orderID, lines[i])
		}(i)
	}

	wg.Wait()

	joined := errors.Join(failures...)

	settlePanicked, settleErr := e.guardPanicked(ctx, "panic al cerrar la orden", func() error {
		return e.settleOrder(ctx, orderID)
	}, "order_id", orderID)
	joined = errors.Join(joined, settleErr)

	if hit := panickedLines(lines, panicked); len(hit) > 0 {
		joined = errors.Join(joined, e.notifyDispatchPanic(ctx, orderID, hit))
	}

	if settlePanicked {
		joined = errors.Join(joined, e.recoverOrder(ctx, orderID))
	}

	return joined
}

type attemptProgress struct {
	number  int
	attempt store.Attempt
	open    bool
}

func (e *Engine) dispatchRecovering(ctx context.Context, orderID string, line store.Line) (panicked bool, err error) {
	var progress attemptProgress

	defer func() {
		if r := recover(); r != nil {
			panicked = true
			err = e.handleDispatchPanic(ctx, orderID, line, &progress, r, debug.Stack())
		}
	}()

	return false, e.dispatchWithinDeadline(ctx, orderID, line, &progress)
}

func (e *Engine) notifyDispatchPanic(ctx context.Context, orderID string, hit []store.Line) error {
	return e.guard(ctx, "panic al avisar el panic de un despacho", func() error {
		labels := make([]string, len(hit))
		for i, line := range hit {
			labels[i] = lineLabel(line)
		}
		// Dedicated kind preserves the order_in_doubt dedup key for settleOrder.
		return e.notifyOnce(ctx, notify.Event{
			Kind:    notify.KindDispatchPanic,
			OrderID: orderID,
			LineID:  hit[0].ID,
			Detail:  fmt.Sprintf("el despacho de %s sufrió un panic y %s en duda: verificar si salió el producto", strings.Join(labels, ", "), joinVerb(len(hit), "la línea quedó", "las líneas quedaron")),
		})
	}, "order_id", orderID)
}

func panickedLines(lines []store.Line, panicked []bool) []store.Line {
	var hit []store.Line
	for i, p := range panicked {
		if p {
			hit = append(hit, lines[i])
		}
	}

	return hit
}

func (e *Engine) handleDispatchPanic(ctx context.Context, orderID string, line store.Line, progress *attemptProgress, panicked any, stack []byte) error {
	panicErr := e.recoverPanic(ctx, "panic during line dispatch", panicked, stack,
		"order_id", orderID,
		"line_id", line.ID,
	)

	recordErr := e.guard(ctx, "panic al registrar el panic de un despacho", func() error {
		attemptErr := e.closePanickedAttempt(ctx, progress, panicErr.Error())
		markErr := e.markPanickedLineInDoubt(ctx, line, progress.number, panicErr.Error())

		return errors.Join(attemptErr, markErr)
	}, "order_id", orderID, "line_id", line.ID)

	return errors.Join(panicErr, recordErr)
}

func (e *Engine) closePanickedAttempt(ctx context.Context, progress *attemptProgress, detail string) error {
	if !progress.open {
		return nil
	}

	attempt := progress.attempt
	attempt.Outcome = store.OutcomeError
	attempt.Detail = detail
	attempt.FinishedAt = e.now()

	if err := e.store.SaveAttempt(ctx, attempt); err != nil {
		return err
	}

	progress.open = false

	return nil
}

func (e *Engine) markPanickedLineInDoubt(ctx context.Context, line store.Line, attempts int, detail string) error {
	for attempt := 0; attempt < 2; attempt++ {
		fresh, err := e.reloadLine(ctx, line)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}

		if lineClosed(fresh) {
			return nil
		}

		state, stateErr := transitionLine(fresh.State, linestate.InDoubt)
		doubtfulAt := e.now()

		fresh.State = state
		fresh.LastError = detail
		if attempts > fresh.Attempts {
			fresh.Attempts = attempts
		}
		fresh.DeadlineAt = doubtfulAt.Add(e.reconcileInterval)
		fresh.ReconcileUntil = doubtfulAt.Add(e.reconcileWindow)

		if _, saveErr := e.store.SaveLine(ctx, fresh); saveErr != nil {
			if errors.Is(saveErr, store.ErrStale) && attempt == 0 {
				continue
			}

			return errors.Join(stateErr, saveErr)
		}

		return stateErr
	}

	return nil
}
