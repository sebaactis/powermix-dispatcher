package order

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/store"
)

const defaultMaxAttempts = 2

const (
	outcomeAmbiguous = "ambiguous"
	outcomeAccepted  = "accepted"
	outcomeRejected  = "rejected"
	outcomeError     = "error"
)

type dispatchVerdict struct {
	state   dispenser.LineState
	outcome string
	detail  string
}

func (e *Engine) dispatchLine(ctx context.Context, orderID string, line store.Line) error {
	for line.Attempts < e.maxAttempts {
		number := line.Attempts + 1

		attempt := store.Attempt{
			ID:             e.nextID(),
			LineID:         line.ID,
			Number:         number,
			IdempotencyKey: idempotencyKey(orderID, line.ID, number),
			Outcome:        outcomeAmbiguous,
			StartedAt:      e.now(),
		}

		if err := e.store.AppendAttempt(ctx, attempt); err != nil {
			if errors.Is(err, store.ErrAlreadyExists) {
				return nil
			}

			return err
		}

		machine, ok := e.machines[line.MachineType]
		if !ok {
			return fmt.Errorf("no hay una máquina configurada para el tipo %q", line.MachineType)
		}

		result, dispatchErr := machine.Dispatch(ctx, dispenser.Request{
			OrderID:     orderID,
			LineID:      line.ID,
			Machine:     line.Machine,
			SKU:         line.SKU,
			AmountCents: line.AmountCents,
		})

		verdict := classifyDispatch(result, dispatchErr)

		attempt.Outcome = verdict.outcome
		attempt.Detail = verdict.detail
		attempt.FinishedAt = e.now()

		attemptErr := e.store.SaveAttempt(ctx, attempt)

		state, stateErr := transitionLine(line.State, verdict.state)

		line.State = state
		line.Attempts = number

		if verdict.outcome == outcomeAccepted {
			acceptedAt := e.now()

			line.Ref = result.ProviderRef
			line.DeadlineAt = acceptedAt.Add(e.deadline)
			line.ReconcileUntil = acceptedAt.Add(e.deadline + e.reconcileWindow)
			line.LastError = ""
		} else {
			line.LastError = verdict.detail

			if verdict.outcome == outcomeError {
				doubtfulAt := e.now()
				line.DeadlineAt = doubtfulAt.Add(e.reconcileInterval)
				line.ReconcileUntil = doubtfulAt.Add(e.reconcileWindow)
			}
		}

		saved, lineErr := e.store.SaveLine(ctx, line)
		if errors.Is(lineErr, store.ErrStale) {
			return nil
		}
		if lineErr == nil {
			line = saved
		}

		if verdict.outcome != outcomeRejected {
			return errors.Join(attemptErr, stateErr, lineErr)
		}

		if line.Attempts >= e.maxAttempts {
			return errors.Join(attemptErr, stateErr, lineErr)
		}

		retry, retryErr := AdvanceLine(line.State, dispenser.LineSent)
		if retryErr != nil {
			return errors.Join(attemptErr, stateErr, lineErr, retryErr)
		}

		line.State = retry

		savedRetry, saveErr := e.store.SaveLine(ctx, line)
		if errors.Is(saveErr, store.ErrStale) {
			return nil
		}
		if saveErr != nil {
			return errors.Join(attemptErr, stateErr, lineErr, saveErr)
		}

		line = savedRetry
	}

	return nil
}

func classifyDispatch(result dispenser.DispatchResult, dispatchErr error) dispatchVerdict {
	switch {
	case dispatchErr != nil && isProviderRejection(dispatchErr):
		return dispatchVerdict{
			state:   dispenser.LineFailed,
			outcome: outcomeRejected,
			detail:  dispatchErr.Error(),
		}
	case dispatchErr != nil:
		return dispatchVerdict{
			state:   dispenser.LineInDoubt,
			outcome: outcomeError,
			detail:  dispatchErr.Error(),
		}
	case result.Accepted:
		return dispatchVerdict{
			state:   dispenser.LineSent,
			outcome: outcomeAccepted,
			detail:  result.Detail,
		}
	default:
		return dispatchVerdict{
			state:   dispenser.LineInDoubt,
			outcome: outcomeError,
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
