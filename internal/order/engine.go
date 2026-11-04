package order

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/notify"
	"compras-remotas/internal/store"
)

const defaultLineDeadline = 2 * time.Minute

const defaultReconcileWindow = 2 * time.Minute

const defaultReconcileInterval = 30 * time.Second

const defaultMaxAttempts = 2

const (
	outcomeAmbiguous = "ambiguous"
	outcomeAccepted  = "accepted"
	outcomeRejected  = "rejected"
	outcomeError     = "error"
)

type Draft struct {
	KioskID     string
	ComboID     string
	ClientKey   string
	AmountCents int64
	Currency    string
	Items       []Item
}

type Item struct {
	SKU         string
	AmountCents int64
	Machine     dispenser.MachineRef
}

const (
	kindOrderInDoubt = "order_in_doubt"
	kindOrderFailed  = "order_failed"
)

const reasonReconcileExpired = "se agotó el plazo de reconciliación sin confirmar la entrega"

type Option func(*Engine)

type Engine struct {
	store             store.Store
	machines          map[string]dispenser.Dispenser
	now               func() time.Time
	deadline          time.Duration
	reconcileWindow   time.Duration
	reconcileInterval time.Duration
	maxAttempts       int
	notifier          notify.Notifier
	newID             func() string

	idMu sync.Mutex
}

type dispatchVerdict struct {
	state   dispenser.LineState
	outcome string
	detail  string
}

func New(st store.Store, machines map[string]dispenser.Dispenser, opts ...Option) *Engine {
	engine := &Engine{
		store:             st,
		machines:          machines,
		now:               time.Now,
		deadline:          defaultLineDeadline,
		reconcileWindow:   defaultReconcileWindow,
		reconcileInterval: defaultReconcileInterval,
		maxAttempts:       defaultMaxAttempts,
		newID:             newRandomID,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(engine)
		}
	}

	return engine
}

func WithClock(now func() time.Time) Option {
	return func(engine *Engine) {
		if now != nil {
			engine.now = now
		}
	}
}

func WithLineDeadline(d time.Duration) Option {
	return func(engine *Engine) {
		if d > 0 {
			engine.deadline = d
		}
	}
}

func WithReconcileWindow(d time.Duration) Option {
	return func(engine *Engine) {
		if d > 0 {
			engine.reconcileWindow = d
		}
	}
}

func WithReconcileInterval(d time.Duration) Option {
	return func(engine *Engine) {
		if d > 0 {
			engine.reconcileInterval = d
		}
	}
}

func WithNotifier(n notify.Notifier) Option {
	return func(engine *Engine) {
		if n != nil {
			engine.notifier = n
		}
	}
}

func WithMaxAttempts(n int) Option {
	return func(engine *Engine) {
		if n > 0 {
			engine.maxAttempts = n
		}
	}
}

func WithIDGenerator(newID func() string) Option {
	return func(engine *Engine) {
		if newID != nil {
			engine.newID = newID
		}
	}
}

func (e *Engine) Place(ctx context.Context, draft Draft) (store.Order, error) {
	if err := e.validateDraft(draft); err != nil {
		return store.Order{}, err
	}

	if draft.ClientKey != "" {
		existing, err := e.store.OrderByClientKey(ctx, draft.ClientKey)
		switch {
		case err == nil:
			return existing, nil
		case !errors.Is(err, store.ErrNotFound):
			return store.Order{}, err
		}
	}

	orderID := e.nextID()
	placed := store.Order{
		ID:          orderID,
		KioskID:     draft.KioskID,
		ComboID:     draft.ComboID,
		ClientKey:   draft.ClientKey,
		AmountCents: draft.AmountCents,
		Currency:    draft.Currency,
		Status:      store.OrderPendingPayment,
		CreatedAt:   e.now(),
		Lines:       make([]store.Line, 0, len(draft.Items)),
	}

	for i, item := range draft.Items {
		lineID := e.nextID()

		placed.Lines = append(placed.Lines, store.Line{
			ID:          lineID,
			OrderID:     orderID,
			Position:    i + 1,
			MachineType: item.Machine.Type,
			Machine:     item.Machine,
			SKU:         item.SKU,
			AmountCents: item.AmountCents,
			State:       dispenser.LinePending,
		})
	}

	if err := e.store.CreateOrder(ctx, placed); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) && draft.ClientKey != "" {
			existing, readErr := e.store.OrderByClientKey(ctx, draft.ClientKey)
			if readErr == nil {
				return existing, nil
			}
		}

		return store.Order{}, err
	}

	return placed, nil
}

func (e *Engine) ConfirmPayment(ctx context.Context, orderID string, at time.Time) error {
	loaded, err := e.store.Order(ctx, orderID)
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

	reloaded, err := e.store.Order(ctx, orderID)
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
		Ref:         line.Ref,
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
	state, stateErr := transitionLine(line.State, target)

	line.State = state
	line.DeadlineAt = time.Time{}
	line.ReconcileUntil = time.Time{}

	if target == dispenser.LineDelivered {
		line.LastError = ""
	} else if detail != "" {
		line.LastError = detail
	}

	return errors.Join(stateErr, e.store.SaveLine(ctx, line))
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

		return true, errors.Join(stateErr, e.store.SaveLine(ctx, line))
	}

	next := now.Add(e.reconcileInterval)
	if next.After(line.ReconcileUntil) {
		next = line.ReconcileUntil
	}

	line.DeadlineAt = next

	return false, e.store.SaveLine(ctx, line)
}

func (e *Engine) settleOrder(ctx context.Context, orderID string) error {
	loaded, err := e.store.Order(ctx, orderID)
	if err != nil {
		return err
	}

	next, err := AdvanceOrder(loaded.Status, loaded.Lines)
	if err != nil {
		return fmt.Errorf("orden %s: %w", orderID, err)
	}

	if next != loaded.Status {
		loaded.Status = next

		if err := e.store.SaveOrder(ctx, loaded); err != nil {
			return err
		}
	}

	return e.notifyOrder(ctx, loaded)
}

func (e *Engine) notifyOrder(ctx context.Context, placed store.Order) error {
	if e.notifier == nil {
		return nil
	}

	event := notify.Event{OrderID: placed.ID}

	switch placed.Status {
	case store.OrderInDoubt:
		event.Kind = kindOrderInDoubt
		event.Detail = "el pedido quedó en duda: no se pudo confirmar la entrega y hace falta una persona"
	case store.OrderFailed:
		event.Kind = kindOrderFailed
		event.Detail = "el pedido quedó fallido: el proveedor informó que no hubo entrega"
	default:
		return nil
	}

	return e.notifier.Notify(ctx, event)
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

			line.Ref = result.Ref
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

		lineErr := e.store.SaveLine(ctx, line)

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

		if saveErr := e.store.SaveLine(ctx, line); saveErr != nil {
			return errors.Join(attemptErr, stateErr, lineErr, saveErr)
		}
	}

	return nil
}

func (e *Engine) validateDraft(draft Draft) error {
	if len(draft.Items) == 0 {
		return errors.New("la orden necesita al menos un ítem")
	}

	if draft.AmountCents < 0 {
		return fmt.Errorf("la orden tiene un importe negativo: %d", draft.AmountCents)
	}

	for i, item := range draft.Items {
		position := i + 1

		if item.Machine.Type == "" {
			return fmt.Errorf("ítem %d: falta el tipo de máquina", position)
		}

		if _, ok := e.machines[item.Machine.Type]; !ok {
			return fmt.Errorf("ítem %d: no hay una máquina configurada para el tipo %q", position, item.Machine.Type)
		}

		if item.SKU == "" {
			return fmt.Errorf("ítem %d: falta el SKU", position)
		}

		if !machineIdentified(item.Machine) {
			return fmt.Errorf("ítem %d: falta el identificador de la máquina", position)
		}

		if item.AmountCents < 0 {
			return fmt.Errorf("ítem %d: el importe no puede ser negativo: %d", position, item.AmountCents)
		}
	}

	return nil
}

func (e *Engine) nextID() string {
	e.idMu.Lock()
	defer e.idMu.Unlock()

	return e.newID()
}

func machineIdentified(machine dispenser.MachineRef) bool {
	return machine.DeviceID != "" || machine.DeviceNo != "" || machine.MID != ""
}

func classifyDispatch(result dispenser.Attempt, dispatchErr error) dispatchVerdict {
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

func transitionLine(current, next dispenser.LineState) (dispenser.LineState, error) {
	if current == dispenser.LinePending && next != dispenser.LineSent {
		if _, err := AdvanceLine(current, dispenser.LineSent); err != nil {
			return current, err
		}

		current = dispenser.LineSent
	}

	return AdvanceLine(current, next)
}

func idempotencyKey(orderID, lineID string, number int) string {
	return orderID + ":" + lineID + ":" + strconv.Itoa(number)
}

func newRandomID() string {
	var buf [16]byte

	if _, err := rand.Read(buf[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}

	return hex.EncodeToString(buf[:])
}
