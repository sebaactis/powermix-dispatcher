package order

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/store"
)

const defaultMaxAttempts = 2

type dispatchVerdict struct {
	state   dispenser.LineState
	outcome store.AttemptOutcome
	detail  string
}

func (e *Engine) dispatchLine(ctx context.Context, orderID string, line store.Line) error {
	for line.Attempts < e.maxAttempts {
		number := line.Attempts + 1

		claimedAt := e.now()

		line.DeadlineAt = claimedAt.Add(e.deadline)
		line.ReconcileUntil = claimedAt.Add(e.deadline + e.reconcileWindow)

		claimed, claimErr := e.store.SaveLine(ctx, line)
		if errors.Is(claimErr, store.ErrStale) {
			return nil
		}
		if claimErr != nil {
			return claimErr
		}

		line = claimed

		attempt := store.Attempt{
			ID:             e.nextID(),
			LineID:         line.ID,
			Number:         number,
			IdempotencyKey: idempotencyKey(orderID, line.ID, number),
			Outcome:        store.OutcomeAmbiguous,
			StartedAt:      e.now(),
		}

		if err := e.store.AppendAttempt(ctx, attempt); err != nil {
			if errors.Is(err, store.ErrAlreadyExists) {
				return nil
			}

			return err
		}

		machine, ok := e.machines[line.Machine.Kind]
		if !ok {
			return fmt.Errorf("no hay una máquina configurada para el tipo %q", line.Machine.Kind)
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

		retrying := verdict.outcome == store.OutcomeRejected && number < e.maxAttempts

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
		} else {
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
		}

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

func classifyDispatch(result dispenser.DispatchResult, dispatchErr error) dispatchVerdict {
	switch {
	case dispatchErr != nil && isProviderRejection(dispatchErr):
		return dispatchVerdict{
			state:   dispenser.LineFailed,
			outcome: store.OutcomeRejected,
			detail:  dispatchErr.Error(),
		}
	case dispatchErr != nil:
		return dispatchVerdict{
			state:   dispenser.LineInDoubt,
			outcome: store.OutcomeError,
			detail:  dispatchErr.Error(),
		}
	case result.Accepted:
		return dispatchVerdict{
			state:   dispenser.LineSent,
			outcome: store.OutcomeAccepted,
			detail:  result.Detail,
		}
	default:
		return dispatchVerdict{
			state:   dispenser.LineInDoubt,
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
