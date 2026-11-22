package order

import (
	"context"
	"errors"
	"fmt"

	"compras-remotas/internal/linestate"
	"compras-remotas/internal/machine"
	"compras-remotas/internal/store"
)

var (
	ErrCallbackIncomplete  = errors.New("el callback está incompleto")
	ErrCallbackUnknownLine = errors.New("el callback no corresponde a ninguna línea de la orden")
	ErrCallbackAmbiguous   = errors.New("el callback corresponde a más de una línea de la orden")
)

type CallbackState string

const (
	CallbackDelivered CallbackState = "delivered"
	CallbackFailed    CallbackState = "failed"
	CallbackProgress  CallbackState = "progress"
)

type Callback struct {
	Source       string
	DedupeKey    string
	OrderID      string
	Ref          string
	ThirdOrderNo string
	MachineType  machine.Kind
	State        CallbackState
	Reason       string
}

func (e *Engine) ApplyCallback(ctx context.Context, cb Callback) (duplicate bool, err error) {
	line, duplicate, err := e.beginCallback(ctx, cb)
	if err != nil {
		return duplicate, err
	}

	if lineClosed(line) {
		return e.recordCallback(ctx, cb, e.settleOrder(ctx, cb.OrderID))
	}

	if err := errors.Join(e.applyLineCallback(ctx, line, cb), e.settleOrder(ctx, cb.OrderID)); err != nil {
		return false, err
	}

	return e.recordCallback(ctx, cb, nil)
}

func (e *Engine) SyncLineFromCallback(ctx context.Context, cb Callback) (duplicate bool, err error) {
	line, duplicate, err := e.beginCallback(ctx, cb)
	if err != nil {
		return duplicate, err
	}

	if lineClosed(line) {
		return e.recordCallback(ctx, cb, e.settleOrder(ctx, cb.OrderID))
	}

	if cb.State == CallbackProgress {
		line = adoptRef(line, cb)

		var brewingErr error
		line, brewingErr = e.markBrewing(ctx, line)
		if brewingErr != nil {
			return false, errors.Join(brewingErr, e.settleOrder(ctx, cb.OrderID))
		}

		if lineClosed(line) {
			return e.recordCallback(ctx, cb, e.settleOrder(ctx, cb.OrderID))
		}
	}

	_, reconcileErr := e.reconcileLine(ctx, line)
	settleErr := e.settleOrder(ctx, cb.OrderID)

	if err := errors.Join(reconcileErr, settleErr); err != nil {
		return false, err
	}

	return e.recordCallback(ctx, cb, nil)
}

func (e *Engine) beginCallback(ctx context.Context, cb Callback) (store.Line, bool, error) {
	line, err := e.callbackLine(ctx, cb)
	if err == nil {
		return line, false, nil
	}

	if errors.Is(err, ErrCallbackIncomplete) {
		return store.Line{}, false, err
	}

	duplicate, registerErr := e.recordCallback(ctx, cb, err)

	return store.Line{}, duplicate, registerErr
}

func (e *Engine) callbackLine(ctx context.Context, cb Callback) (store.Line, error) {
	if cb.Source == "" || cb.DedupeKey == "" || cb.OrderID == "" || cb.State == "" {
		return store.Line{}, ErrCallbackIncomplete
	}

	loaded, err := e.store.LoadOrder(ctx, cb.OrderID)
	if err != nil {
		return store.Line{}, err
	}

	if line, found := lineByRef(loaded.Lines, cb.Ref); found {
		return line, nil
	}

	if line, found := lineByThirdOrderNo(loaded.Lines, cb.ThirdOrderNo, cb.MachineType); found {
		return line, nil
	}

	return lineByMachineType(loaded.Lines, cb.MachineType)
}

func lineByRef(lines []store.Line, ref string) (store.Line, bool) {
	if ref == "" {
		return store.Line{}, false
	}

	for _, line := range lines {
		if line.Ref == ref {
			return line, true
		}
	}

	return store.Line{}, false
}

func lineByThirdOrderNo(lines []store.Line, thirdOrderNo string, machineType machine.Kind) (store.Line, bool) {
	if thirdOrderNo == "" || machineType == "" {
		return store.Line{}, false
	}

	for _, line := range lines {
		if line.ID == thirdOrderNo && line.Machine.Kind == machineType {
			return line, true
		}
	}

	return store.Line{}, false
}

func lineByMachineType(lines []store.Line, machineType machine.Kind) (store.Line, error) {
	if machineType == "" {
		return store.Line{}, ErrCallbackUnknownLine
	}

	candidates := make([]store.Line, 0, 1)

	for _, line := range lines {
		if line.Machine.Kind == machineType {
			candidates = append(candidates, line)
		}
	}

	switch len(candidates) {
	case 0:
		return store.Line{}, ErrCallbackUnknownLine
	case 1:
		if candidates[0].Ref != "" {
			return store.Line{}, ErrCallbackUnknownLine
		}

		return candidates[0], nil
	default:
		return store.Line{}, ErrCallbackAmbiguous
	}
}

func adoptRef(line store.Line, cb Callback) store.Line {
	if line.Ref == "" && cb.Ref != "" {
		line.Ref = cb.Ref
	}

	return line
}

func (e *Engine) recordCallback(ctx context.Context, cb Callback, cause error) (bool, error) {
	duplicate, err := e.store.RegisterWebhook(ctx, store.Webhook{
		ID:         e.nextID(),
		Source:     cb.Source,
		DedupeKey:  cb.DedupeKey,
		ReceivedAt: e.now(),
	})
	if err != nil {
		return false, errors.Join(cause, err)
	}

	return duplicate, cause
}

func lineClosed(line store.Line) bool {
	return line.State == linestate.Delivered || line.State == linestate.Failed
}

func (e *Engine) applyLineCallback(ctx context.Context, line store.Line, cb Callback) error {
	line = adoptRef(line, cb)

	switch cb.State {
	case CallbackDelivered:
		return e.closeLine(ctx, line, linestate.Delivered, "")
	case CallbackFailed:
		return e.closeLine(ctx, line, linestate.Failed, cb.Reason)
	case CallbackProgress:
		_, err := e.markBrewing(ctx, line)

		return err
	default:
		return fmt.Errorf("estado de callback desconocido: %q", cb.State)
	}
}

func (e *Engine) markBrewing(ctx context.Context, line store.Line) (store.Line, error) {
	saved, err := e.saveBrewingLine(ctx, line)
	if !errors.Is(err, store.ErrStale) {
		return saved, err
	}

	fresh, reloadErr := e.reloadLine(ctx, line)
	if reloadErr != nil {
		return line, reloadErr
	}

	if fresh.Ref == "" && line.Ref != "" {
		fresh.Ref = line.Ref
	}

	saved, retryErr := e.saveBrewingLine(ctx, fresh)
	if !errors.Is(retryErr, store.ErrStale) {
		return saved, retryErr
	}

	latest, reloadErr := e.reloadLine(ctx, line)
	if reloadErr != nil {
		return fresh, reloadErr
	}

	if latest.Ref == "" && line.Ref != "" {
		latest.Ref = line.Ref
	}

	e.log().WarnContext(ctx, "no se marcó la línea en elaboración porque cambió concurrentemente; se continúa con la última versión",
		"order_id", latest.OrderID,
		"line_id", latest.ID,
		"state", string(latest.State),
	)

	return latest, nil
}

func (e *Engine) saveBrewingLine(ctx context.Context, line store.Line) (store.Line, error) {
	if line.State == linestate.Brewing || lineClosed(line) {
		return line, nil
	}

	state, err := transitionLine(line.State, linestate.Brewing)
	if err != nil {
		return line, nil
	}

	next := line
	next.State = state

	saved, err := e.store.SaveLine(ctx, next)
	if err != nil {
		return line, err
	}

	return saved, nil
}
