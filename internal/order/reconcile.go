package order

import (
	"context"
	"errors"
	"fmt"
	"time"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/linestate"
	"compras-remotas/internal/store"
)

const defaultLineDeadline = 2 * time.Minute

const defaultReconcileWindow = 2 * time.Minute

const defaultReconcileInterval = 30 * time.Second

func (e *Engine) ReconcileDue(ctx context.Context, limit int) error {
	now := e.now()

	due, err := e.store.DueLines(ctx, now, limit)
	if err != nil {
		return err
	}

	failures := make([]error, 0, len(due))
	touched := make([]string, 0, len(due))
	seen := make(map[string]struct{}, len(due))

	touch := func(orderID string) {
		if _, ok := seen[orderID]; ok {
			return
		}

		seen[orderID] = struct{}{}
		touched = append(touched, orderID)
	}

	for _, line := range due {
		closed, lineErr := e.reconcileLine(ctx, line)
		if lineErr != nil {
			failures = append(failures, lineErr)
		}

		if !closed {
			continue
		}

		touch(line.OrderID)
	}

	stalled, err := e.store.StalledLines(ctx, now.Add(-e.deadline), limit)
	if err != nil {
		failures = append(failures, err)
	} else {
		for _, line := range stalled {
			if lineErr := e.resumeStalledLine(ctx, line); lineErr != nil {
				failures = append(failures, lineErr)
			}

			touch(line.OrderID)
		}
	}

	for _, orderID := range touched {
		if err := e.settleOrder(ctx, orderID); err != nil {
			failures = append(failures, err)
		}
	}

	return errors.Join(failures...)
}

func (e *Engine) resumeStalledLine(ctx context.Context, line store.Line) error {
	if _, err := e.refreshOrderStatus(ctx, line.OrderID); err != nil {
		return err
	}

	fresh, err := e.reloadLine(ctx, line)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	if fresh.State != linestate.Pending || !fresh.DeadlineAt.IsZero() {
		return nil
	}

	return e.dispatchWithinDeadline(ctx, line.OrderID, fresh)
}

func (e *Engine) reconcileLine(ctx context.Context, line store.Line) (bool, error) {
	machine, ok := e.machines[line.Machine.Kind]
	if !ok {
		unconfigured := fmt.Errorf("no hay una máquina configurada para el tipo %q", line.Machine.Kind)

		closed, scheduleErr := e.extendReconcile(ctx, line, unconfigured.Error())

		return closed, errors.Join(unconfigured, scheduleErr)
	}

	status, reconcileErr := machine.Reconcile(ctx, dispenser.Request{
		OrderID:     line.OrderID,
		LineID:      line.ID,
		Machine:     line.Machine,
		SKU:         line.SKU,
		AmountCents: line.AmountCents,
		KnownRef:    line.Ref,
	})

	if reconcileErr != nil {
		closed, scheduleErr := e.extendReconcile(ctx, line, reconcileErr.Error())

		return closed, errors.Join(reconcileErr, scheduleErr)
	}

	switch status.State {
	case linestate.Delivered:
		return true, e.closeLine(ctx, line, linestate.Delivered, "")
	case linestate.Failed:
		return true, e.closeLine(ctx, line, linestate.Failed, status.Reason)
	default:
		return e.extendReconcile(ctx, line, status.Reason)
	}
}

func (e *Engine) closeLine(ctx context.Context, line store.Line, target linestate.State, detail string) error {
	_, err := e.saveClosedLine(ctx, line, target, detail)
	if !errors.Is(err, store.ErrStale) {
		return err
	}

	fresh, reloadErr := e.reloadLine(ctx, line)
	if reloadErr != nil {
		return reloadErr
	}

	if lineClosed(fresh) {
		return nil
	}

	_, retryErr := e.saveClosedLine(ctx, fresh, target, detail)
	if errors.Is(retryErr, store.ErrStale) {
		return nil
	}

	return retryErr
}

func (e *Engine) saveClosedLine(ctx context.Context, line store.Line, target linestate.State, detail string) (store.Line, error) {
	state, stateErr := transitionLine(line.State, target)

	line.State = state
	line.DeadlineAt = time.Time{}
	line.ReconcileUntil = time.Time{}

	if target == linestate.Delivered {
		line.LastError = ""
	} else if detail != "" {
		line.LastError = detail
	}

	saved, err := e.store.SaveLine(ctx, line)

	return saved, errors.Join(stateErr, err)
}

func (e *Engine) reloadLine(ctx context.Context, line store.Line) (store.Line, error) {
	loaded, err := e.store.LoadOrder(ctx, line.OrderID)
	if err != nil {
		return store.Line{}, err
	}

	for _, candidate := range loaded.Lines {
		if candidate.ID == line.ID {
			return candidate, nil
		}
	}

	return store.Line{}, store.ErrNotFound
}

func (e *Engine) extendReconcile(ctx context.Context, line store.Line, detail string) (bool, error) {
	now := e.now()

	if detail != "" {
		line.LastError = detail
	}

	if line.ReconcileUntil.IsZero() || !now.Before(line.ReconcileUntil) {
		state, stateErr := transitionLine(line.State, linestate.InDoubt)

		line.State = state
		line.DeadlineAt = time.Time{}
		line.ReconcileUntil = time.Time{}

		if line.LastError == "" {
			line.LastError = reasonReconcileExpired
		}

		if _, err := e.store.SaveLine(ctx, line); err != nil {
			if errors.Is(err, store.ErrStale) {
				return false, nil
			}

			return true, errors.Join(stateErr, err)
		}

		return true, stateErr
	}

	next := now.Add(e.reconcileInterval)
	if next.After(line.ReconcileUntil) {
		next = line.ReconcileUntil
	}

	line.DeadlineAt = next

	if _, err := e.store.SaveLine(ctx, line); err != nil {
		if errors.Is(err, store.ErrStale) {
			return false, nil
		}

		return false, err
	}

	return false, nil
}

func transitionLine(current, next linestate.State) (linestate.State, error) {
	if current == linestate.Pending && next != linestate.Sent {
		if _, err := AdvanceLine(current, linestate.Sent); err != nil {
			return current, err
		}

		current = linestate.Sent
	}

	return AdvanceLine(current, next)
}
