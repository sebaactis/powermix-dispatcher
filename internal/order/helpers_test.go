package order_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/machine"
	"compras-remotas/internal/notify"
	"compras-remotas/internal/order"
	"compras-remotas/internal/store"
)

var engineNow = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func frozenClock() func() time.Time {
	return func() time.Time { return engineNow }
}

func sequentialIDs() func() string {
	var mu sync.Mutex
	next := 0

	return func() string {
		mu.Lock()
		defer mu.Unlock()

		next++

		return fmt.Sprintf("id-%d", next)
	}
}

type stubMachine struct {
	mu      sync.Mutex
	calls   []dispenser.Request
	attempt dispenser.DispatchResult
	err     error
}

func (s *stubMachine) Check(ctx context.Context, machine dispenser.MachineRef) (dispenser.Availability, error) {
	return dispenser.Availability{Available: true}, nil
}

func (s *stubMachine) Dispatch(ctx context.Context, req dispenser.Request) (dispenser.DispatchResult, error) {
	s.mu.Lock()
	s.calls = append(s.calls, req)
	s.mu.Unlock()

	return s.attempt, s.err
}

func (s *stubMachine) Reconcile(ctx context.Context, req dispenser.Request) (dispenser.Status, error) {
	return dispenser.Status{}, nil
}

func (s *stubMachine) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.calls)
}

func (s *stubMachine) onlyCall(t *testing.T) dispenser.Request {
	t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.calls) != 1 {
		t.Fatalf("la máquina recibió %d despachos, se esperaba 1", len(s.calls))
	}

	return s.calls[0]
}

type flakyMachine struct {
	mu         sync.Mutex
	calls      []dispenser.Request
	rejections int
	attempt    dispenser.DispatchResult
}

func (s *flakyMachine) Check(ctx context.Context, machine dispenser.MachineRef) (dispenser.Availability, error) {
	return dispenser.Availability{Available: true}, nil
}

func (s *flakyMachine) Dispatch(ctx context.Context, req dispenser.Request) (dispenser.DispatchResult, error) {
	s.mu.Lock()
	s.calls = append(s.calls, req)
	index := len(s.calls)
	s.mu.Unlock()

	if index <= s.rejections {
		return dispenser.DispatchResult{}, &dispenser.Rejection{Provider: "batidos", HTTPStatus: 400, Code: 40005, Message: "sin stock"}
	}

	return s.attempt, nil
}

func (s *flakyMachine) Reconcile(ctx context.Context, req dispenser.Request) (dispenser.Status, error) {
	return dispenser.Status{}, nil
}

func (s *flakyMachine) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.calls)
}

type recordingStore struct {
	*store.Memory

	mu       sync.Mutex
	created  []string
	appended []store.Attempt
	saved    []store.Attempt
}

func newRecordingStore() *recordingStore {
	return &recordingStore{Memory: store.NewMemory()}
}

func (r *recordingStore) CreateOrder(ctx context.Context, o store.Order) error {
	if err := r.Memory.CreateOrder(ctx, o); err != nil {
		return err
	}

	r.mu.Lock()
	r.created = append(r.created, o.ID)
	r.mu.Unlock()

	return nil
}

func (r *recordingStore) AppendAttempt(ctx context.Context, attempt store.Attempt) error {
	if err := r.Memory.AppendAttempt(ctx, attempt); err != nil {
		return err
	}

	r.mu.Lock()
	r.appended = append(r.appended, attempt)
	r.mu.Unlock()

	return nil
}

func (r *recordingStore) SaveAttempt(ctx context.Context, attempt store.Attempt) error {
	if err := r.Memory.SaveAttempt(ctx, attempt); err != nil {
		return err
	}

	r.mu.Lock()
	r.saved = append(r.saved, attempt)
	r.mu.Unlock()

	return nil
}

func (r *recordingStore) createdIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]string(nil), r.created...)
}

func (r *recordingStore) appendedFor(t *testing.T, lineID string) store.Attempt {
	t.Helper()

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, attempt := range r.appended {
		if attempt.LineID == lineID {
			return attempt
		}
	}

	t.Fatalf("no se registró ningún intento para la línea %q", lineID)

	return store.Attempt{}
}

func (r *recordingStore) attemptsFor(t *testing.T, lineID string) []store.Attempt {
	t.Helper()

	r.mu.Lock()
	defer r.mu.Unlock()

	found := make([]store.Attempt, 0, len(r.appended))

	for _, attempt := range r.appended {
		if attempt.LineID == lineID {
			found = append(found, attempt)
		}
	}

	return found
}

func (r *recordingStore) savedFor(t *testing.T, lineID string) store.Attempt {
	t.Helper()

	r.mu.Lock()
	defer r.mu.Unlock()

	for i := len(r.saved) - 1; i >= 0; i-- {
		if r.saved[i].LineID == lineID {
			return r.saved[i]
		}
	}

	t.Fatalf("no se cerró ningún intento para la línea %q", lineID)

	return store.Attempt{}
}

func comboDraft() order.Draft {
	return order.Draft{
		KioskID:     "kiosk-1",
		ComboID:     "combo-1",
		AmountCents: 2500,
		Currency:    "ARS",
		Items: []order.Item{
			{
				SKU:         "sku-batido",
				AmountCents: 1500,
				Machine: dispenser.MachineRef{
					KioskID:      "kiosk-1",
					Type:         "batidos",
					Name:         "Batidos Powermix",
					DeviceID:     "device-1",
					DeviceNo:     "1",
					DeviceTypeID: "2",
				},
			},
			{
				SKU:         "sku-snack",
				AmountCents: 1000,
				Machine: dispenser.MachineRef{
					KioskID: "kiosk-1",
					Type:    "snacks",
					Name:    "Snacks Powermix",
					MID:     "mid-1",
				},
			},
		},
	}
}

func drinkOnlyDraft() order.Draft {
	draft := comboDraft()
	draft.AmountCents = 1500
	draft.Items = draft.Items[:1]

	return draft
}

func mustOrder(t *testing.T, st store.Store, orderID string) store.Order {
	t.Helper()

	loaded, err := st.LoadOrder(context.Background(), orderID)
	if err != nil {
		t.Fatalf("Order(%q): %v", orderID, err)
	}

	return loaded
}

func lineFor(t *testing.T, o store.Order, machineType string) store.Line {
	t.Helper()

	for _, line := range o.Lines {
		if line.MachineType == machine.Kind(machineType) {
			return line
		}
	}

	t.Fatalf("la orden %q no tiene una línea de %q", o.ID, machineType)

	return store.Line{}
}

func place(t *testing.T, engine *order.Engine, draft order.Draft) store.Order {
	t.Helper()

	placed, err := engine.Place(context.Background(), draft)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}

	return placed
}

type closingOnDispatchMachine struct {
	onDispatch func()
	attempt    dispenser.DispatchResult

	mu         sync.Mutex
	dispatches int
}

func (m *closingOnDispatchMachine) Check(ctx context.Context, machine dispenser.MachineRef) (dispenser.Availability, error) {
	return dispenser.Availability{Available: true}, nil
}

func (m *closingOnDispatchMachine) Dispatch(ctx context.Context, req dispenser.Request) (dispenser.DispatchResult, error) {
	m.mu.Lock()
	m.dispatches++
	onDispatch := m.onDispatch
	m.mu.Unlock()

	if onDispatch != nil {
		onDispatch()
	}

	return m.attempt, nil
}

func (m *closingOnDispatchMachine) Reconcile(ctx context.Context, req dispenser.Request) (dispenser.Status, error) {
	return dispenser.Status{}, nil
}

type manualClock struct {
	mu sync.Mutex
	at time.Time
}

func manualClockAt(at time.Time) *manualClock {
	return &manualClock{at: at}
}

func (c *manualClock) clock() func() time.Time {
	return func() time.Time {
		c.mu.Lock()
		defer c.mu.Unlock()

		return c.at
	}
}

func (c *manualClock) set(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.at = at
}

type reconcileMachine struct {
	mu           sync.Mutex
	dispatched   []dispenser.Request
	reconciled   []dispenser.Request
	attempt      dispenser.DispatchResult
	dispatchErr  error
	status       dispenser.Status
	reconcileErr error
}

func (m *reconcileMachine) Check(ctx context.Context, machine dispenser.MachineRef) (dispenser.Availability, error) {
	return dispenser.Availability{Available: true}, nil
}

func (m *reconcileMachine) Dispatch(ctx context.Context, req dispenser.Request) (dispenser.DispatchResult, error) {
	m.mu.Lock()
	m.dispatched = append(m.dispatched, req)
	m.mu.Unlock()

	return m.attempt, m.dispatchErr
}

func (m *reconcileMachine) Reconcile(ctx context.Context, req dispenser.Request) (dispenser.Status, error) {
	m.mu.Lock()
	m.reconciled = append(m.reconciled, req)
	status, err := m.status, m.reconcileErr
	m.mu.Unlock()

	return status, err
}

func (m *reconcileMachine) dispatchCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return len(m.dispatched)
}

func (m *reconcileMachine) reconcileCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return len(m.reconciled)
}

func (m *reconcileMachine) onlyReconcile(t *testing.T) dispenser.Request {
	t.Helper()

	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.reconciled) != 1 {
		t.Fatalf("la máquina recibió %d consultas de reconciliación, se esperaba 1", len(m.reconciled))
	}

	return m.reconciled[0]
}

type recordingNotifier struct {
	mu     sync.Mutex
	events []notify.Event
}

func (n *recordingNotifier) Notify(ctx context.Context, ev notify.Event) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.events = append(n.events, ev)

	return nil
}

func (n *recordingNotifier) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()

	return len(n.events)
}

func (n *recordingNotifier) only(t *testing.T) notify.Event {
	t.Helper()

	n.mu.Lock()
	defer n.mu.Unlock()

	if len(n.events) != 1 {
		t.Fatalf("el notificador recibió %d eventos, se esperaba 1: %+v", len(n.events), n.events)
	}

	return n.events[0]
}

type reconcileFixture struct {
	engine *order.Engine
	store  *store.Memory
	clock  *manualClock
	placed store.Order
}

func newReconcileFixture(t *testing.T, driver dispenser.Dispenser, opts ...order.Option) *reconcileFixture {
	t.Helper()

	st := store.NewMemory()
	clock := manualClockAt(engineNow)

	engineOpts := []order.Option{
		order.WithClock(clock.clock()),
		order.WithIDGenerator(sequentialIDs()),
	}
	engineOpts = append(engineOpts, opts...)

	engine := order.New(st, map[machine.Kind]dispenser.Dispenser{"batidos": driver}, engineOpts...)
	placed := place(t, engine, drinkOnlyDraft())

	if err := engine.ConfirmPayment(context.Background(), placed.ID, engineNow); err != nil {
		t.Fatalf("ConfirmPayment: %v", err)
	}

	fixture := &reconcileFixture{engine: engine, store: st, clock: clock, placed: mustOrder(t, st, placed.ID)}

	armed := fixture.line(t)
	if armed.State != dispenser.LineSent || armed.DeadlineAt.IsZero() || armed.ReconcileUntil.IsZero() {
		t.Fatalf("el fixture necesita una línea despachada con plazo y ventana armados: %+v", armed)
	}

	return fixture
}

func (f *reconcileFixture) line(t *testing.T) store.Line {
	t.Helper()

	return mustOrder(t, f.store, f.placed.ID).Lines[0]
}

func (f *reconcileFixture) order(t *testing.T) store.Order {
	t.Helper()

	return mustOrder(t, f.store, f.placed.ID)
}

func (f *reconcileFixture) reconcileDue(t *testing.T) error {
	t.Helper()

	return f.engine.ReconcileDue(context.Background(), 10)
}

func acceptedDrinkMachine() *reconcileMachine {
	return &reconcileMachine{attempt: dispenser.DispatchResult{Accepted: true, ProviderRef: "exchange-code-1"}}
}
