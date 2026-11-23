package order

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/linestate"
	"compras-remotas/internal/store"
)

const defaultMaxAttempts = 2

type dispatchVerdict struct {
	state   linestate.State
	outcome store.AttemptOutcome
	detail  string
}

func (e *Engine) dispatchWithinDeadline(ctx context.Context, orderID string, line store.Line, progress *attemptProgress) error {
	lineCtx, cancel := context.WithTimeout(ctx, e.deadline)
	defer cancel()

	return e.dispatchLine(lineCtx, orderID, line, progress)
}

func (e *Engine) dispatchLine(ctx context.Context, orderID string, line store.Line, progress *attemptProgress) error {
	for line.Attempts < e.maxAttempts {
		number := line.Attempts + 1

		claimed, attempt, stop, err := e.openAttempt(ctx, orderID, line, number, progress)
		if stop || err != nil {
			return err
		}

		line = claimed

		machine, ok := e.machines[line.Machine.Kind]
		if !ok {
			return fmt.Errorf("no hay una máquina configurada para el tipo %q", line.Machine.Kind)
		}

		result, dispatchErr := machine.Dispatch(ctx, e.dispatchRequest(orderID, &line))

		verdict := classifyDispatch(result, dispatchErr)

		attemptErr := e.closeAttempt(ctx, attempt, verdict, progress)

		retrying := verdict.outcome == store.OutcomeRejected && number < e.maxAttempts

		var stateErr error
		line, stateErr = e.applyVerdict(line, number, verdict, result, retrying)

		saved, lineErr := e.store.SaveLine(ctx, line)
		if errors.Is(lineErr, store.ErrStale) {
			return nil
		}
		if lineErr != nil || !retrying {
			return errors.Join(attemptErr, stateErr, lineErr)
		}

		line = saved
	}

	return nil
}

func (e *Engine) openAttempt(ctx context.Context, orderID string, line store.Line, number int, progress *attemptProgress) (store.Line, store.Attempt, bool, error) {
	claimedAt := e.now()

	line.DeadlineAt = claimedAt.Add(e.deadline)
	line.ReconcileUntil = claimedAt.Add(e.deadline + e.reconcileWindow)

	claimed, err := e.store.SaveLine(ctx, line)
	if errors.Is(err, store.ErrStale) {
		return store.Line{}, store.Attempt{}, true, nil
	}
	if err != nil {
		return store.Line{}, store.Attempt{}, true, err
	}

	attempt := store.Attempt{
		ID:             e.nextID(),
		LineID:         claimed.ID,
		Number:         number,
		IdempotencyKey: idempotencyKey(orderID, claimed.ID, number),
		Outcome:        store.OutcomeAmbiguous,
		StartedAt:      e.now(),
	}

	if err := e.store.AppendAttempt(ctx, attempt); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			return store.Line{}, store.Attempt{}, true, nil
		}

		return store.Line{}, store.Attempt{}, true, err
	}

	progress.number = number
	progress.attempt = attempt
	progress.open = true

	return claimed, attempt, false, nil
}

func (e *Engine) dispatchRequest(orderID string, line *store.Line) dispenser.Request {
	return dispenser.Request{
		OrderID:     orderID,
		LineID:      line.ID,
		Machine:     line.Machine,
		SKU:         line.SKU,
		AmountCents: line.AmountCents,
		SaveRef: func(refCtx context.Context, ref string) error {
			pending := *line
			pending.Ref = ref

			saved, err := e.store.SaveLine(refCtx, pending)
			if err != nil {
				return err
			}

			*line = saved

			return nil
		},
	}
}

func (e *Engine) closeAttempt(ctx context.Context, attempt store.Attempt, verdict dispatchVerdict, progress *attemptProgress) error {
	attempt.Outcome = verdict.outcome
	attempt.Detail = verdict.detail
	attempt.FinishedAt = e.now()

	if err := e.store.SaveAttempt(ctx, attempt); err != nil {
		return err
	}

	progress.open = false

	return nil
}

func (e *Engine) applyVerdict(line store.Line, number int, verdict dispatchVerdict, result dispenser.DispatchResult, retrying bool) (store.Line, error) {
	var stateErr error
	if !retrying {
		line.State, stateErr = transitionLine(line.State, verdict.state)
	}

	line.Attempts = number

	if verdict.outcome == store.OutcomeAccepted {
		acceptedAt := e.now()

		line.Ref = result.ProviderRef
		line.DeadlineAt = acceptedAt.Add(e.deadline)
		line.ReconcileUntil = acceptedAt.Add(e.deadline + e.reconcileWindow)
		line.LastError = ""

		return line, stateErr
	}

	if result.ProviderRef != "" {
		line.Ref = result.ProviderRef
	}

	line.LastError = verdict.detail
	line.DeadlineAt = time.Time{}
	line.ReconcileUntil = time.Time{}

	if verdict.outcome == store.OutcomeError {
		doubtfulAt := e.now()
		line.DeadlineAt = doubtfulAt.Add(e.reconcileInterval)
		line.ReconcileUntil = doubtfulAt.Add(e.reconcileWindow)
	}

	return line, stateErr
}

func classifyDispatch(result dispenser.DispatchResult, dispatchErr error) dispatchVerdict {
	switch {
	case dispatchErr != nil && (isProviderRejection(dispatchErr) || errors.Is(dispatchErr, dispenser.ErrNotDispatched)):
		return dispatchVerdict{
			state:   linestate.Failed,
			outcome: store.OutcomeRejected,
			detail:  dispatchErr.Error(),
		}
	case dispatchErr != nil:
		return dispatchVerdict{
			state:   linestate.InDoubt,
			outcome: store.OutcomeError,
			detail:  dispatchErr.Error(),
		}
	case result.Accepted:
		return dispatchVerdict{
			state:   linestate.Sent,
			outcome: store.OutcomeAccepted,
			detail:  result.Detail,
		}
	default:
		return dispatchVerdict{
			state:   linestate.InDoubt,
			outcome: store.OutcomeError,
			detail:  "la máquina no confirmó la aceptación del despacho",
		}
	}
}

func isProviderRejection(err error) bool {
	var rejection *dispenser.Rejection

	return errors.As(err, &rejection)
}

func idempotencyKey(orderID, lineID string, number int) string {
	return orderID + ":" + lineID + ":" + strconv.Itoa(number)
}
