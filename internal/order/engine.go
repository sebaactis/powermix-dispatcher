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
	"compras-remotas/internal/dispenser/batidos"
	"compras-remotas/internal/dispenser/snacks"
	"compras-remotas/internal/store"
)

const defaultLineDeadline = 2 * time.Minute

const (
	outcomeAmbiguous = "ambiguous"
	outcomeAccepted  = "accepted"
	outcomeRejected  = "rejected"
	outcomeError     = "error"
)

type Draft struct {
	KioskID     string
	ComboID     string
	AmountCents int64
	Currency    string
	Items       []Item
}

type Item struct {
	MachineType string
	SKU         string
	AmountCents int64
	Machine     dispenser.MachineRef
}

type Option func(*Engine)

type Engine struct {
	store    store.Store
	machines map[string]dispenser.Dispenser
	now      func() time.Time
	deadline time.Duration
	newID    func() string

	idMu   sync.Mutex
	refsMu sync.RWMutex
	refs   map[string]dispenser.MachineRef
}

type dispatchVerdict struct {
	state   dispenser.LineState
	outcome string
	detail  string
}

func New(st store.Store, machines map[string]dispenser.Dispenser, opts ...Option) *Engine {
	engine := &Engine{
		store:    st,
		machines: machines,
		now:      time.Now,
		deadline: defaultLineDeadline,
		newID:    newRandomID,
		refs:     make(map[string]dispenser.MachineRef),
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

	orderID := e.nextID()
	placed := store.Order{
		ID:          orderID,
		KioskID:     draft.KioskID,
		ComboID:     draft.ComboID,
		AmountCents: draft.AmountCents,
		Currency:    draft.Currency,
		Status:      store.OrderPendingPayment,
		CreatedAt:   e.now(),
		Lines:       make([]store.Line, 0, len(draft.Items)),
	}

	refs := make(map[string]dispenser.MachineRef, len(draft.Items))

	for i, item := range draft.Items {
		lineID := e.nextID()
		refs[lineID] = item.Machine

		placed.Lines = append(placed.Lines, store.Line{
			ID:          lineID,
			OrderID:     orderID,
			Position:    i + 1,
			MachineType: item.MachineType,
			SKU:         item.SKU,
			AmountCents: item.AmountCents,
			State:       dispenser.LinePending,
		})
	}

	if err := e.store.CreateOrder(ctx, placed); err != nil {
		return store.Order{}, err
	}

	e.refsMu.Lock()
	for lineID, ref := range refs {
		e.refs[lineID] = ref
	}
	e.refsMu.Unlock()

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

func (e *Engine) dispatchLine(ctx context.Context, orderID string, line store.Line) error {
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

	ref, ok := e.machineRef(line.ID)
	if !ok {
		return fmt.Errorf("no se conoce la máquina de la línea %q", line.ID)
	}

	machine, ok := e.machines[line.MachineType]
	if !ok {
		return fmt.Errorf("no hay una máquina configurada para el tipo %q", line.MachineType)
	}

	result, dispatchErr := machine.Dispatch(ctx, dispenser.Request{
		OrderID:     orderID,
		LineID:      line.ID,
		Machine:     ref,
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
		line.Ref = result.Ref
		line.DeadlineAt = e.now().Add(e.deadline)
	} else {
		line.LastError = verdict.detail
	}

	lineErr := e.store.SaveLine(ctx, line)

	return errors.Join(attemptErr, stateErr, lineErr)
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

		if item.MachineType == "" {
			return fmt.Errorf("ítem %d: falta el tipo de máquina", position)
		}

		if _, ok := e.machines[item.MachineType]; !ok {
			return fmt.Errorf("ítem %d: no hay una máquina configurada para el tipo %q", position, item.MachineType)
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

func (e *Engine) machineRef(lineID string) (dispenser.MachineRef, bool) {
	e.refsMu.RLock()
	defer e.refsMu.RUnlock()

	ref, ok := e.refs[lineID]

	return ref, ok
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
	var batidosErr *batidos.APIError
	if errors.As(err, &batidosErr) {
		return true
	}

	var snacksErr *snacks.APIError

	return errors.As(err, &snacksErr)
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
