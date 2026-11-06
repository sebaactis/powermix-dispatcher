package order

import (
	"context"
	"errors"
	"fmt"
	"time"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/store"
)

const defaultLineDeadline = 2 * time.Minute

const defaultReconcileWindow = 2 * time.Minute

const defaultReconcileInterval = 30 * time.Second

func (e *Engine) ReconcileDue(ctx context.Context, limit int) error {
	due, err := e.store.DueLines(ctx, e.now(), limit)
	if err != nil {
		return err
	}

	failures := make([]error, 0, len(due))
	touched := make([]string, 0, len(due))
	seen := make(map[string]struct{}, len(due))

	for _, line := range due {
		closed, lineErr := e.reconcileLine(ctx, line)
		if lineErr != nil {
			failures = append(failures, lineErr)
		}

		if !closed {
			continue
		}

		if _, ok := seen[line.OrderID]; ok {
			continue
		}

		seen[line.OrderID] = struct{}{}
		touched = append(touched, line.OrderID)
	}

	for _, orderID := range touched {
		if err := e.settleOrder(ctx, orderID); err != nil {
			failures = append(failures, err)
		}
	}

	return errors.Join(failures...)
}

func (e *Engine) reconcileLine(ctx context.Context, line store.Line) (bool, error) {
	machine, ok := e.machines[line.MachineType]
	if !ok {
		unconfigured := fmt.Errorf("no hay una máquina configurada para el tipo %q", line.MachineType)

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
	case dispenser.LineDelivered:
		return true, e.closeLine(ctx, line, dispenser.LineDelivered, "")
	case dispenser.LineFailed:
		return true, e.closeLine(ctx, line, dispenser.LineFailed, status.Reason)
	default:
		return e.extendReconcile(ctx, line, status.Reason)
	}
}

func (e *Engine) closeLine(ctx context.Context, line store.Line, target dispenser.LineState, detail string) error {
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

func (e *Engine) saveClosedLine(ctx context.Context, line store.Line, target dispenser.LineState, detail string) (store.Line, error) {
	state, stateErr := transitionLine(line.State, target)

	line.State = state
	line.DeadlineAt = time.Time{}
	line.ReconcileUntil = time.Time{}

	if target == dispenser.LineDelivered {
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
		state, stateErr := transitionLine(line.State, dispenser.LineInDoubt)

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

func transitionLine(current, next dispenser.LineState) (dispenser.LineState, error) {
	if current == dispenser.LinePending && next != dispenser.LineSent {
		if _, err := AdvanceLine(current, dispenser.LineSent); err != nil {
			return current, err
		}

		current = dispenser.LineSent
	}

	return AdvanceLine(current, next)
}
