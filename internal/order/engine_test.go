package order_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"compras-remotas/internal/dispenser"
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
	attempt dispenser.Attempt
	err     error
}

func (s *stubMachine) Check(ctx context.Context, machine dispenser.MachineRef) (dispenser.Availability, error) {
	return dispenser.Availability{Available: true}, nil
}

func (s *stubMachine) Dispatch(ctx context.Context, req dispenser.Request) (dispenser.Attempt, error) {
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
	attempt    dispenser.Attempt
}

func (s *flakyMachine) Check(ctx context.Context, machine dispenser.MachineRef) (dispenser.Availability, error) {
	return dispenser.Availability{Available: true}, nil
}

func (s *flakyMachine) Dispatch(ctx context.Context, req dispenser.Request) (dispenser.Attempt, error) {
	s.mu.Lock()
	s.calls = append(s.calls, req)
	index := len(s.calls)
	s.mu.Unlock()

	if index <= s.rejections {
		return dispenser.Attempt{}, &dispenser.Rejection{Provider: "batidos", HTTPStatus: 400, Code: 40005, Message: "sin stock"}
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

	loaded, err := st.Order(context.Background(), orderID)
	if err != nil {
		t.Fatalf("Order(%q): %v", orderID, err)
	}

	return loaded
}

func lineFor(t *testing.T, o store.Order, machineType string) store.Line {
	t.Helper()

	for _, line := range o.Lines {
		if line.MachineType == machineType {
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

func TestEnginePlaceCreatesOneLinePerMachine(t *testing.T) {
	st := store.NewMemory()
	engine := order.New(
		st,
		map[string]dispenser.Dispenser{"batidos": &stubMachine{}, "snacks": &stubMachine{}},
		order.WithClock(frozenClock()),
		order.WithIDGenerator(sequentialIDs()),
	)

	draft := comboDraft()
	placed := place(t, engine, draft)

	if placed.Status != store.OrderPendingPayment {
		t.Fatalf("Status=%q, se esperaba %q", placed.Status, store.OrderPendingPayment)
	}
	if !placed.CreatedAt.Equal(engineNow) {
		t.Fatalf("CreatedAt=%v, se esperaba %v", placed.CreatedAt, engineNow)
	}
	if !placed.PaidAt.IsZero() {
		t.Fatalf("PaidAt=%v, se esperaba vacío", placed.PaidAt)
	}
	if placed.KioskID != draft.KioskID || placed.ComboID != draft.ComboID {
		t.Fatalf("la orden no copió el mueble y el combo: %+v", placed)
	}
	if placed.AmountCents != draft.AmountCents || placed.Currency != draft.Currency {
		t.Fatalf("la orden no copió el importe y la moneda: %+v", placed)
	}
	if placed.ID == "" {
		t.Fatal("la orden quedó sin id")
	}

	if len(placed.Lines) != 2 {
		t.Fatalf("la orden tiene %d líneas, se esperaban 2", len(placed.Lines))
	}
	if placed.Lines[0].ID == "" || placed.Lines[0].ID == placed.Lines[1].ID {
		t.Fatalf("las líneas quedaron con ids vacíos o repetidos: %q, %q", placed.Lines[0].ID, placed.Lines[1].ID)
	}

	for i, want := range draft.Items {
		line := placed.Lines[i]

		if line.OrderID != placed.ID {
			t.Fatalf("la línea %d apunta a la orden %q, se esperaba %q", i+1, line.OrderID, placed.ID)
		}
		if line.Position != i+1 {
			t.Fatalf("la línea %d tiene Position=%d", i+1, line.Position)
		}
		if line.MachineType != want.Machine.Type {
			t.Fatalf("la línea %d tiene MachineType=%q, se esperaba %q", i+1, line.MachineType, want.Machine.Type)
		}
		if line.SKU != want.SKU {
			t.Fatalf("la línea %d tiene SKU=%q, se esperaba %q", i+1, line.SKU, want.SKU)
		}
		if line.AmountCents != want.AmountCents {
			t.Fatalf("la línea %d tiene AmountCents=%d, se esperaba %d", i+1, line.AmountCents, want.AmountCents)
		}
		if line.State != dispenser.LinePending {
			t.Fatalf("la línea %d nace en %q, se esperaba %q", i+1, line.State, dispenser.LinePending)
		}
		if line.Attempts != 0 {
			t.Fatalf("la línea %d nace con %d intentos", i+1, line.Attempts)
		}
		if line.Ref != "" || line.LastError != "" || !line.DeadlineAt.IsZero() {
			t.Fatalf("la línea %d nace con datos de despacho: %+v", i+1, line)
		}
	}

	stored := mustOrder(t, st, placed.ID)
	if !reflect.DeepEqual(stored, placed) {
		t.Fatalf("la orden guardada es %+v, se esperaba %+v", stored, placed)
	}
}

func TestEnginePlaceRejectsAnInvalidDraftAndCreatesNothing(t *testing.T) {
	cases := []struct {
		name         string
		mutate       func(*order.Draft)
		wantPosition string
	}{
		{
			name:   "sin ítems",
			mutate: func(draft *order.Draft) { draft.Items = nil },
		},
		{
			name:         "sin tipo de máquina",
			mutate:       func(draft *order.Draft) { draft.Items[0].Machine.Type = "" },
			wantPosition: "ítem 1",
		},
		{
			name:         "con un tipo de máquina desconocido",
			mutate:       func(draft *order.Draft) { draft.Items[1].Machine.Type = "cafetera" },
			wantPosition: "ítem 2",
		},
		{
			name:         "sin SKU",
			mutate:       func(draft *order.Draft) { draft.Items[1].SKU = "" },
			wantPosition: "ítem 2",
		},
		{
			name: "sin identificador de máquina",
			mutate: func(draft *order.Draft) {
				draft.Items[0].Machine = dispenser.MachineRef{KioskID: "kiosk-1", Type: "batidos"}
			},
			wantPosition: "ítem 1",
		},
		{
			name:   "con importe de orden negativo",
			mutate: func(draft *order.Draft) { draft.AmountCents = -1 },
		},
		{
			name:         "con importe de ítem negativo",
			mutate:       func(draft *order.Draft) { draft.Items[1].AmountCents = -1 },
			wantPosition: "ítem 2",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newRecordingStore()
			engine := order.New(
				st,
				map[string]dispenser.Dispenser{"batidos": &stubMachine{}, "snacks": &stubMachine{}},
				order.WithClock(frozenClock()),
				order.WithIDGenerator(sequentialIDs()),
			)

			draft := comboDraft()
			tc.mutate(&draft)

			placed, err := engine.Place(context.Background(), draft)
			if err == nil {
				t.Fatalf("Place aceptó un borrador inválido y devolvió %+v", placed)
			}
			if !reflect.DeepEqual(placed, store.Order{}) {
				t.Fatalf("Place devolvió %+v junto al error", placed)
			}
			if tc.wantPosition != "" && !strings.Contains(err.Error(), tc.wantPosition) {
				t.Fatalf("el error %q no nombra %q", err.Error(), tc.wantPosition)
			}

			if created := st.createdIDs(); len(created) != 0 {
				t.Fatalf("Place creó %v con un borrador inválido", created)
			}
		})
	}
}

func TestEnginePlaceRejectsAnItemWithoutAConfiguredMachineType(t *testing.T) {
	cases := []struct {
		name         string
		machineType  string
		wantPosition string
	}{
		{name: "con el tipo de máquina vacío", machineType: "", wantPosition: "ítem 1"},
		{name: "con un tipo de máquina no configurado", machineType: "cafetera", wantPosition: "ítem 1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newRecordingStore()
			engine := order.New(
				st,
				map[string]dispenser.Dispenser{"batidos": &stubMachine{}, "snacks": &stubMachine{}},
				order.WithClock(frozenClock()),
				order.WithIDGenerator(sequentialIDs()),
			)

			draft := comboDraft()
			draft.Items[0].Machine.Type = tc.machineType

			placed, err := engine.Place(context.Background(), draft)
			if err == nil {
				t.Fatalf("Place aceptó un ítem con Machine.Type=%q y devolvió %+v", tc.machineType, placed)
			}
			if !reflect.DeepEqual(placed, store.Order{}) {
				t.Fatalf("Place devolvió %+v junto al error", placed)
			}
			if !strings.Contains(err.Error(), tc.wantPosition) {
				t.Fatalf("el error %q no nombra %q", err.Error(), tc.wantPosition)
			}

			if created := st.createdIDs(); len(created) != 0 {
				t.Fatalf("Place creó %v con un ítem sin tipo de máquina válido", created)
			}
		})
	}
}

func TestEnginePlaceTakesTheLineMachineTypeFromTheMachineRef(t *testing.T) {
	st := newRecordingStore()
	machines := map[string]dispenser.Dispenser{"batidos": &stubMachine{}, "snacks": &stubMachine{}}
	engine := order.New(
		st,
		machines,
		order.WithClock(frozenClock()),
		order.WithIDGenerator(sequentialIDs()),
	)

	draft := comboDraft()
	placed := place(t, engine, draft)

	stored := mustOrder(t, st, placed.ID)
	if len(stored.Lines) != len(draft.Items) {
		t.Fatalf("la orden guardada tiene %d líneas, se esperaban %d", len(stored.Lines), len(draft.Items))
	}

	for i, item := range draft.Items {
		line := stored.Lines[i]

		if line.MachineType != item.Machine.Type {
			t.Fatalf("la línea %d se guardó con MachineType=%q, se esperaba el tipo de la máquina %q", i+1, line.MachineType, item.Machine.Type)
		}
		if !reflect.DeepEqual(line.Machine, item.Machine) {
			t.Fatalf("la línea %d se guardó con la máquina %+v, se esperaba %+v", i+1, line.Machine, item.Machine)
		}
		if _, ok := machines[line.MachineType]; !ok {
			t.Fatalf("la línea %d se guardó con un tipo sin driver: %q", i+1, line.MachineType)
		}
	}
}

func TestEngineConfirmPaymentDispatchesBothMachines(t *testing.T) {
	st := newRecordingStore()
	batido := &stubMachine{attempt: dispenser.Attempt{Accepted: true, Ref: "order-no-1"}}
	snack := &stubMachine{attempt: dispenser.Attempt{Accepted: true}}
	engine := order.New(
		st,
		map[string]dispenser.Dispenser{"batidos": batido, "snacks": snack},
		order.WithClock(frozenClock()),
		order.WithIDGenerator(sequentialIDs()),
	)

	placed := place(t, engine, comboDraft())
	if err := engine.ConfirmPayment(context.Background(), placed.ID, engineNow); err != nil {
		t.Fatalf("ConfirmPayment: %v", err)
	}

	got := mustOrder(t, st, placed.ID)
	if got.Status != store.OrderDispatching {
		t.Fatalf("Status=%q, se esperaba %q", got.Status, store.OrderDispatching)
	}
	if !got.PaidAt.Equal(engineNow) {
		t.Fatalf("PaidAt=%v, se esperaba %v", got.PaidAt, engineNow)
	}

	deadline := engineNow.Add(2 * time.Minute)
	for _, machineType := range []string{"batidos", "snacks"} {
		line := lineFor(t, got, machineType)

		if line.State != dispenser.LineSent {
			t.Fatalf("la línea de %s quedó en %q, se esperaba %q", machineType, line.State, dispenser.LineSent)
		}
		if line.Attempts != 1 {
			t.Fatalf("la línea de %s tiene %d intentos, se esperaba 1", machineType, line.Attempts)
		}
		if line.DeadlineAt.IsZero() {
			t.Fatalf("la línea de %s quedó sin plazo", machineType)
		}
		if !line.DeadlineAt.Equal(deadline) {
			t.Fatalf("la línea de %s vence %v, se esperaba %v", machineType, line.DeadlineAt, deadline)
		}
		if line.LastError != "" {
			t.Fatalf("la línea de %s quedó con error %q", machineType, line.LastError)
		}
	}

	if ref := lineFor(t, got, "batidos").Ref; ref != "order-no-1" {
		t.Fatalf("el Ref del batido es %q, se esperaba el asa del intento", ref)
	}
	if ref := lineFor(t, got, "snacks").Ref; ref != "" {
		t.Fatalf("el Ref del snack es %q, se esperaba vacío", ref)
	}

	if batido.callCount() != 1 || snack.callCount() != 1 {
		t.Fatalf("despachos: batidos=%d, snacks=%d, se esperaba uno de cada uno", batido.callCount(), snack.callCount())
	}

	drinkLine := lineFor(t, got, "batidos")
	req := batido.onlyCall(t)
	if req.OrderID != placed.ID || req.LineID != drinkLine.ID {
		t.Fatalf("el request llevó order=%q line=%q, se esperaba order=%q line=%q", req.OrderID, req.LineID, placed.ID, drinkLine.ID)
	}
	if req.SKU != "sku-batido" || req.AmountCents != 1500 {
		t.Fatalf("el request llevó SKU=%q importe=%d", req.SKU, req.AmountCents)
	}
	if req.Machine.DeviceID != "device-1" {
		t.Fatalf("el request llevó la máquina %+v, se esperaba la del borrador", req.Machine)
	}

	appended := st.appendedFor(t, drinkLine.ID)
	wantKey := fmt.Sprintf("%s:%s:%d", placed.ID, drinkLine.ID, 1)
	if appended.IdempotencyKey != wantKey {
		t.Fatalf("la clave de idempotencia es %q, se esperaba %q", appended.IdempotencyKey, wantKey)
	}
	if appended.Outcome != "ambiguous" {
		t.Fatalf("el intento se registró como %q, se esperaba %q", appended.Outcome, "ambiguous")
	}
	if !appended.StartedAt.Equal(engineNow) {
		t.Fatalf("el intento arrancó en %v, se esperaba %v", appended.StartedAt, engineNow)
	}
	if saved := st.savedFor(t, drinkLine.ID); saved.Outcome != "accepted" {
		t.Fatalf("el intento se cerró como %q, se esperaba %q", saved.Outcome, "accepted")
	}
}

func TestEngineConfirmPaymentMarksARejectedLineFailed(t *testing.T) {
	st := newRecordingStore()
	batido := &stubMachine{err: &dispenser.Rejection{Provider: "batidos", HTTPStatus: 400, Code: 40005, Message: "nonce repetido"}}
	snack := &stubMachine{attempt: dispenser.Attempt{Accepted: true}}
	engine := order.New(
		st,
		map[string]dispenser.Dispenser{"batidos": batido, "snacks": snack},
		order.WithClock(frozenClock()),
		order.WithIDGenerator(sequentialIDs()),
	)

	placed := place(t, engine, comboDraft())
	if err := engine.ConfirmPayment(context.Background(), placed.ID, engineNow); err != nil {
		t.Fatalf("ConfirmPayment: %v", err)
	}

	got := mustOrder(t, st, placed.ID)
	if got.Status != store.OrderDispatching {
		t.Fatalf("Status=%q, se esperaba %q", got.Status, store.OrderDispatching)
	}

	failed := lineFor(t, got, "batidos")
	if failed.State != dispenser.LineFailed {
		t.Fatalf("la línea rechazada quedó en %q, se esperaba %q", failed.State, dispenser.LineFailed)
	}
	if !strings.Contains(failed.LastError, "nonce repetido") {
		t.Fatalf("LastError=%q, se esperaba el mensaje del proveedor", failed.LastError)
	}
	if !strings.Contains(failed.LastError, "batidos") || !strings.Contains(failed.LastError, "40005") {
		t.Fatalf("LastError=%q, se esperaba que nombre al proveedor y el código", failed.LastError)
	}
	if !failed.DeadlineAt.IsZero() {
		t.Fatalf("una línea rechazada no tiene plazo armado: %v", failed.DeadlineAt)
	}

	sent := lineFor(t, got, "snacks")
	if sent.State != dispenser.LineSent {
		t.Fatalf("la línea sana quedó en %q, se esperaba %q", sent.State, dispenser.LineSent)
	}

	if saved := st.savedFor(t, failed.ID); saved.Outcome != "rejected" {
		t.Fatalf("el intento rechazado se cerró como %q, se esperaba %q", saved.Outcome, "rejected")
	}
}

func TestEngineConfirmPaymentDispatchesAfterARestartWithAFreshEngine(t *testing.T) {
	st := store.NewMemory()

	placingEngine := order.New(
		st,
		map[string]dispenser.Dispenser{
			"batidos": &stubMachine{attempt: dispenser.Attempt{Accepted: true}},
			"snacks":  &stubMachine{attempt: dispenser.Attempt{Accepted: true}},
		},
		order.WithClock(frozenClock()),
		order.WithIDGenerator(sequentialIDs()),
	)

	placed := place(t, placingEngine, comboDraft())

	batido := &stubMachine{attempt: dispenser.Attempt{Accepted: true, Ref: "order-no-1"}}
	snack := &stubMachine{attempt: dispenser.Attempt{Accepted: true}}
	afterRestart := order.New(
		st,
		map[string]dispenser.Dispenser{"batidos": batido, "snacks": snack},
		order.WithClock(frozenClock()),
		order.WithIDGenerator(sequentialIDs()),
	)

	if err := afterRestart.ConfirmPayment(context.Background(), placed.ID, engineNow); err != nil {
		t.Fatalf("ConfirmPayment en un motor nuevo: %v", err)
	}

	if batido.callCount() != 1 || snack.callCount() != 1 {
		t.Fatalf("despachos tras el reinicio: batidos=%d, snacks=%d, se esperaba uno de cada uno", batido.callCount(), snack.callCount())
	}

	got := mustOrder(t, st, placed.ID)
	for _, machineType := range []string{"batidos", "snacks"} {
		line := lineFor(t, got, machineType)
		if line.State != dispenser.LineSent {
			t.Fatalf("la línea de %s quedó en %q, se esperaba %q", machineType, line.State, dispenser.LineSent)
		}
		if line.LastError != "" {
			t.Fatalf("la línea de %s se despachó con el error %q", machineType, line.LastError)
		}
	}

	drinkLine := lineFor(t, got, "batidos")
	if req := batido.onlyCall(t); req.Machine.DeviceID != "device-1" {
		t.Fatalf("el despacho llevó la máquina %+v, se esperaba la guardada con la línea", req.Machine)
	}
	if drinkLine.Ref != "order-no-1" {
		t.Fatalf("el Ref del batido es %q, se esperaba el asa del intento", drinkLine.Ref)
	}
}

func TestEngineConfirmPaymentKeepsAnUncertainLineInDoubt(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{name: "error de red", err: errors.New("dial tcp 10.0.0.1:443: connect: connection refused")},
		{name: "contexto cortado", err: fmt.Errorf("batidos: no se pudo enviar: %w", context.Canceled)},
		{name: "error desconocido", err: errors.New("el proveedor devolvió algo inesperado")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newRecordingStore()
			batido := &stubMachine{err: tc.err}
			snack := &stubMachine{attempt: dispenser.Attempt{Accepted: true}}
			engine := order.New(
				st,
				map[string]dispenser.Dispenser{"batidos": batido, "snacks": snack},
				order.WithClock(frozenClock()),
				order.WithIDGenerator(sequentialIDs()),
			)

			placed := place(t, engine, comboDraft())
			if err := engine.ConfirmPayment(context.Background(), placed.ID, engineNow); err != nil {
				t.Fatalf("ConfirmPayment: %v", err)
			}

			got := mustOrder(t, st, placed.ID)

			doubtful := lineFor(t, got, "batidos")
			if doubtful.State == dispenser.LineFailed {
				t.Fatalf("una llamada sin respuesta quedó %q: no se sabe si el producto salió", doubtful.State)
			}
			if doubtful.State != dispenser.LineInDoubt {
				t.Fatalf("la línea incierta quedó en %q, se esperaba %q", doubtful.State, dispenser.LineInDoubt)
			}
			if doubtful.LastError == "" {
				t.Fatal("la línea incierta quedó sin motivo")
			}
			if doubtful.DeadlineAt.IsZero() {
				t.Fatal("una línea incierta sin próxima consulta armada nunca se reconcilia")
			}
			if !doubtful.ReconcileUntil.After(doubtful.DeadlineAt) {
				t.Fatalf("el tope de reconciliación %v tiene que ser posterior a la próxima consulta %v", doubtful.ReconcileUntil, doubtful.DeadlineAt)
			}

			if saved := st.savedFor(t, doubtful.ID); saved.Outcome != "error" {
				t.Fatalf("el intento incierto se cerró como %q, se esperaba %q", saved.Outcome, "error")
			}
		})
	}
}

func TestEngineConfirmPaymentFailsTheOrderWhenBothMachinesReject(t *testing.T) {
	st := newRecordingStore()
	batido := &stubMachine{err: &dispenser.Rejection{Provider: "batidos", HTTPStatus: 400, Code: 40004, Message: "firma inválida"}}
	snack := &stubMachine{err: &dispenser.Rejection{Provider: "snacks", HTTPStatus: 401, Code: 40004, Message: "firma inválida"}}
	engine := order.New(
		st,
		map[string]dispenser.Dispenser{"batidos": batido, "snacks": snack},
		order.WithClock(frozenClock()),
		order.WithIDGenerator(sequentialIDs()),
	)

	placed := place(t, engine, comboDraft())
	if err := engine.ConfirmPayment(context.Background(), placed.ID, engineNow); err != nil {
		t.Fatalf("ConfirmPayment: %v", err)
	}

	got := mustOrder(t, st, placed.ID)
	if got.Status != store.OrderFailed {
		t.Fatalf("Status=%q, se esperaba %q", got.Status, store.OrderFailed)
	}

	for _, machineType := range []string{"batidos", "snacks"} {
		if line := lineFor(t, got, machineType); line.State != dispenser.LineFailed {
			t.Fatalf("la línea de %s quedó en %q, se esperaba %q", machineType, line.State, dispenser.LineFailed)
		}
	}
}

func TestEngineConfirmPaymentIsIdempotent(t *testing.T) {
	st := newRecordingStore()
	batido := &stubMachine{attempt: dispenser.Attempt{Accepted: true}}
	snack := &stubMachine{attempt: dispenser.Attempt{Accepted: true}}
	engine := order.New(
		st,
		map[string]dispenser.Dispenser{"batidos": batido, "snacks": snack},
		order.WithClock(frozenClock()),
		order.WithIDGenerator(sequentialIDs()),
	)

	placed := place(t, engine, comboDraft())

	if err := engine.ConfirmPayment(context.Background(), placed.ID, engineNow); err != nil {
		t.Fatalf("ConfirmPayment: %v", err)
	}
	repeatedAt := engineNow.Add(5 * time.Minute)
	if err := engine.ConfirmPayment(context.Background(), placed.ID, repeatedAt); err != nil {
		t.Fatalf("ConfirmPayment repetido: %v", err)
	}

	if batido.callCount() != 1 || snack.callCount() != 1 {
		t.Fatalf("despachos: batidos=%d, snacks=%d, se esperaba uno de cada uno", batido.callCount(), snack.callCount())
	}

	got := mustOrder(t, st, placed.ID)
	if got.Status != store.OrderDispatching {
		t.Fatalf("Status=%q, se esperaba %q", got.Status, store.OrderDispatching)
	}
	if !got.PaidAt.Equal(engineNow) {
		t.Fatalf("el webhook repetido movió PaidAt a %v", got.PaidAt)
	}
	for _, machineType := range []string{"batidos", "snacks"} {
		if line := lineFor(t, got, machineType); line.Attempts != 1 {
			t.Fatalf("la línea de %s acumuló %d intentos, se esperaba 1", machineType, line.Attempts)
		}
	}
}

func TestEngineConfirmPaymentDoesNotDispatchTwiceAfterARestart(t *testing.T) {
	st := newRecordingStore()
	batido := &stubMachine{attempt: dispenser.Attempt{Accepted: true}}
	engine := order.New(
		st,
		map[string]dispenser.Dispenser{"batidos": batido},
		order.WithClock(frozenClock()),
		order.WithIDGenerator(sequentialIDs()),
	)

	placed := place(t, engine, drinkOnlyDraft())
	line := placed.Lines[0]

	previous := store.Attempt{
		ID:             "attempt-previo",
		LineID:         line.ID,
		Number:         1,
		IdempotencyKey: fmt.Sprintf("%s:%s:%d", placed.ID, line.ID, 1),
		Outcome:        "ambiguous",
		StartedAt:      engineNow.Add(-time.Minute),
	}
	if err := st.Memory.AppendAttempt(context.Background(), previous); err != nil {
		t.Fatalf("AppendAttempt del intento previo: %v", err)
	}

	if err := engine.ConfirmPayment(context.Background(), placed.ID, engineNow); err != nil {
		t.Fatalf("ConfirmPayment: %v", err)
	}

	if batido.callCount() != 0 {
		t.Fatalf("la máquina recibió %d despachos después de un reinicio, se esperaba 0", batido.callCount())
	}

	got := mustOrder(t, st, placed.ID)
	if reloaded := got.Lines[0]; reloaded.State != dispenser.LinePending {
		t.Fatalf("la línea quedó en %q, se esperaba %q", reloaded.State, dispenser.LinePending)
	}
	if got.Lines[0].Attempts != 0 {
		t.Fatalf("la línea acumuló %d intentos, se esperaba 0", got.Lines[0].Attempts)
	}
}

func TestEngineConfirmPaymentHonoursTheLineDeadline(t *testing.T) {
	cases := []struct {
		name     string
		deadline time.Duration
		want     time.Duration
	}{
		{name: "plazo configurado", deadline: 30 * time.Second, want: 30 * time.Second},
		{name: "plazo por defecto", deadline: 0, want: 2 * time.Minute},
		{name: "plazo negativo conserva el valor por defecto", deadline: -time.Minute, want: 2 * time.Minute},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newRecordingStore()
			engine := order.New(
				st,
				map[string]dispenser.Dispenser{
					"batidos": &stubMachine{attempt: dispenser.Attempt{Accepted: true}},
					"snacks":  &stubMachine{attempt: dispenser.Attempt{Accepted: true}},
				},
				order.WithClock(frozenClock()),
				order.WithIDGenerator(sequentialIDs()),
				order.WithLineDeadline(tc.deadline),
			)

			placed := place(t, engine, comboDraft())
			if err := engine.ConfirmPayment(context.Background(), placed.ID, engineNow); err != nil {
				t.Fatalf("ConfirmPayment: %v", err)
			}

			got := mustOrder(t, st, placed.ID)
			want := engineNow.Add(tc.want)

			for _, machineType := range []string{"batidos", "snacks"} {
				line := lineFor(t, got, machineType)
				if !line.DeadlineAt.Equal(want) {
					t.Fatalf("la línea de %s vence %v, se esperaba %v", machineType, line.DeadlineAt, want)
				}
			}
		})
	}
}

func TestEngineConfirmPaymentRetriesARejectedLineWithAFreshKey(t *testing.T) {
	st := newRecordingStore()
	batido := &flakyMachine{rejections: 1, attempt: dispenser.Attempt{Accepted: true, Ref: "order-no-1"}}
	snack := &stubMachine{attempt: dispenser.Attempt{Accepted: true}}
	engine := order.New(
		st,
		map[string]dispenser.Dispenser{"batidos": batido, "snacks": snack},
		order.WithClock(frozenClock()),
		order.WithIDGenerator(sequentialIDs()),
	)

	placed := place(t, engine, comboDraft())
	if err := engine.ConfirmPayment(context.Background(), placed.ID, engineNow); err != nil {
		t.Fatalf("ConfirmPayment: %v", err)
	}

	if batido.callCount() != 2 {
		t.Fatalf("la máquina rechazada recibió %d despachos, se esperaban 2: el fallido se reintenta", batido.callCount())
	}
	if snack.callCount() != 1 {
		t.Fatalf("la línea sana recibió %d despachos, se esperaba 1: el otro producto no se toca", snack.callCount())
	}

	got := mustOrder(t, st, placed.ID)
	line := lineFor(t, got, "batidos")

	if line.State != dispenser.LineSent {
		t.Fatalf("la línea reintentada quedó en %q, se esperaba %q", line.State, dispenser.LineSent)
	}
	if line.Attempts != 2 {
		t.Fatalf("la línea reintentada acumuló %d intentos, se esperaban 2", line.Attempts)
	}
	if line.Ref != "order-no-1" {
		t.Fatalf("el Ref de la línea reintentada es %q, se esperaba el asa del intento aceptado", line.Ref)
	}
	if !line.DeadlineAt.Equal(engineNow.Add(2 * time.Minute)) {
		t.Fatalf("la línea reintentada vence %v, se esperaba %v", line.DeadlineAt, engineNow.Add(2*time.Minute))
	}
	if line.LastError != "" {
		t.Fatalf("la línea aceptada conserva el error del intento fallido: %q", line.LastError)
	}

	attempts := st.attemptsFor(t, line.ID)
	if len(attempts) != 2 {
		t.Fatalf("se registraron %d intentos, se esperaban 2", len(attempts))
	}

	for i, attempt := range attempts {
		if attempt.Number != i+1 {
			t.Fatalf("el intento %d se registró con Number=%d", i+1, attempt.Number)
		}
		if attempt.Outcome != "ambiguous" {
			t.Fatalf("el intento %d se registró como %q, se esperaba %q", i+1, attempt.Outcome, "ambiguous")
		}
	}

	wantFirst := fmt.Sprintf("%s:%s:%d", placed.ID, line.ID, 1)
	wantSecond := fmt.Sprintf("%s:%s:%d", placed.ID, line.ID, 2)

	if attempts[0].IdempotencyKey != wantFirst {
		t.Fatalf("la clave del primer intento es %q, se esperaba %q", attempts[0].IdempotencyKey, wantFirst)
	}
	if attempts[1].IdempotencyKey != wantSecond {
		t.Fatalf("la clave del segundo intento es %q, se esperaba %q", attempts[1].IdempotencyKey, wantSecond)
	}
	if attempts[0].IdempotencyKey == attempts[1].IdempotencyKey {
		t.Fatalf("los dos intentos comparten la clave %q: un reintento es un intento nuevo", attempts[0].IdempotencyKey)
	}
}

func TestEngineConfirmPaymentStopsRetryingWhenTheAttemptsRunOut(t *testing.T) {
	st := newRecordingStore()
	batido := &stubMachine{err: &dispenser.Rejection{Provider: "batidos", HTTPStatus: 400, Code: 40005, Message: "sin stock"}}
	snack := &stubMachine{attempt: dispenser.Attempt{Accepted: true}}
	engine := order.New(
		st,
		map[string]dispenser.Dispenser{"batidos": batido, "snacks": snack},
		order.WithClock(frozenClock()),
		order.WithIDGenerator(sequentialIDs()),
		order.WithMaxAttempts(2),
	)

	placed := place(t, engine, comboDraft())
	if err := engine.ConfirmPayment(context.Background(), placed.ID, engineNow); err != nil {
		t.Fatalf("ConfirmPayment: %v", err)
	}

	if batido.callCount() != 2 {
		t.Fatalf("la máquina que siempre rechaza recibió %d despachos, se esperaban exactamente 2", batido.callCount())
	}

	got := mustOrder(t, st, placed.ID)
	line := lineFor(t, got, "batidos")

	if line.State != dispenser.LineFailed {
		t.Fatalf("la línea quedó en %q, se esperaba %q", line.State, dispenser.LineFailed)
	}
	if line.Attempts != 2 {
		t.Fatalf("la línea acumuló %d intentos, se esperaban 2", line.Attempts)
	}
	if !line.DeadlineAt.IsZero() {
		t.Fatalf("una línea fallada no tiene plazo armado: %v", line.DeadlineAt)
	}
	if !strings.Contains(line.LastError, "sin stock") {
		t.Fatalf("LastError=%q, se esperaba el mensaje del proveedor", line.LastError)
	}
}

func TestEngineConfirmPaymentNeverRetriesAnUncertainLine(t *testing.T) {
	st := newRecordingStore()
	batido := &stubMachine{err: errors.New("dial tcp 10.0.0.1:443: connect: connection refused")}
	snack := &stubMachine{attempt: dispenser.Attempt{Accepted: true}}
	engine := order.New(
		st,
		map[string]dispenser.Dispenser{"batidos": batido, "snacks": snack},
		order.WithClock(frozenClock()),
		order.WithIDGenerator(sequentialIDs()),
	)

	placed := place(t, engine, comboDraft())
	if err := engine.ConfirmPayment(context.Background(), placed.ID, engineNow); err != nil {
		t.Fatalf("ConfirmPayment: %v", err)
	}

	if batido.callCount() != 1 {
		t.Fatalf("la máquina sin respuesta recibió %d despachos, se esperaba exactamente 1: un intento incierto no se reintenta", batido.callCount())
	}

	got := mustOrder(t, st, placed.ID)
	line := lineFor(t, got, "batidos")

	if line.State != dispenser.LineInDoubt {
		t.Fatalf("la línea incierta quedó en %q, se esperaba %q", line.State, dispenser.LineInDoubt)
	}
	if line.Attempts != 1 {
		t.Fatalf("la línea incierta acumuló %d intentos, se esperaba 1", line.Attempts)
	}
}

func TestEngineConfirmPaymentWithMaxAttemptsOneDoesNotRetry(t *testing.T) {
	st := newRecordingStore()
	batido := &stubMachine{err: &dispenser.Rejection{Provider: "batidos", HTTPStatus: 400, Code: 40005, Message: "sin stock"}}
	snack := &stubMachine{attempt: dispenser.Attempt{Accepted: true}}
	engine := order.New(
		st,
		map[string]dispenser.Dispenser{"batidos": batido, "snacks": snack},
		order.WithClock(frozenClock()),
		order.WithIDGenerator(sequentialIDs()),
		order.WithMaxAttempts(1),
	)

	placed := place(t, engine, comboDraft())
	if err := engine.ConfirmPayment(context.Background(), placed.ID, engineNow); err != nil {
		t.Fatalf("ConfirmPayment: %v", err)
	}

	if batido.callCount() != 1 {
		t.Fatalf("con un solo intento permitido la máquina recibió %d despachos, se esperaba 1", batido.callCount())
	}

	got := mustOrder(t, st, placed.ID)
	line := lineFor(t, got, "batidos")

	if line.State != dispenser.LineFailed {
		t.Fatalf("la línea quedó en %q, se esperaba %q", line.State, dispenser.LineFailed)
	}
	if line.Attempts != 1 {
		t.Fatalf("la línea acumuló %d intentos, se esperaba 1", line.Attempts)
	}
}

func TestEngineConfirmPaymentSkipsALineThatExhaustedItsAttempts(t *testing.T) {
	st := newRecordingStore()
	batido := &stubMachine{attempt: dispenser.Attempt{Accepted: true}}
	engine := order.New(
		st,
		map[string]dispenser.Dispenser{"batidos": batido},
		order.WithClock(frozenClock()),
		order.WithIDGenerator(sequentialIDs()),
	)

	placed := place(t, engine, drinkOnlyDraft())
	line := placed.Lines[0]
	line.Attempts = 2

	if err := st.SaveLine(context.Background(), line); err != nil {
		t.Fatalf("SaveLine: %v", err)
	}

	if err := engine.ConfirmPayment(context.Background(), placed.ID, engineNow); err != nil {
		t.Fatalf("ConfirmPayment: %v", err)
	}

	if batido.callCount() != 0 {
		t.Fatalf("una línea con los intentos agotados recibió %d despachos, se esperaban 0", batido.callCount())
	}

	got := mustOrder(t, st, placed.ID)
	if reloaded := got.Lines[0]; reloaded.Attempts != 2 {
		t.Fatalf("la línea acumuló %d intentos, se esperaban 2", reloaded.Attempts)
	}
	if reloaded := got.Lines[0]; reloaded.State != dispenser.LinePending {
		t.Fatalf("la línea quedó en %q, se esperaba no tocarla en %q", reloaded.State, dispenser.LinePending)
	}
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
	attempt      dispenser.Attempt
	dispatchErr  error
	status       dispenser.Status
	reconcileErr error
}

func (m *reconcileMachine) Check(ctx context.Context, machine dispenser.MachineRef) (dispenser.Availability, error) {
	return dispenser.Availability{Available: true}, nil
}

func (m *reconcileMachine) Dispatch(ctx context.Context, req dispenser.Request) (dispenser.Attempt, error) {
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

func newReconcileFixture(t *testing.T, machine dispenser.Dispenser, opts ...order.Option) *reconcileFixture {
	t.Helper()

	st := store.NewMemory()
	clock := manualClockAt(engineNow)

	engineOpts := []order.Option{
		order.WithClock(clock.clock()),
		order.WithIDGenerator(sequentialIDs()),
	}
	engineOpts = append(engineOpts, opts...)

	engine := order.New(st, map[string]dispenser.Dispenser{"batidos": machine}, engineOpts...)
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
	return &reconcileMachine{attempt: dispenser.Attempt{Accepted: true, Ref: "exchange-code-1"}}
}

func TestEngineConfirmPaymentArmsTheReconcileWindow(t *testing.T) {
	cases := []struct {
		name     string
		opts     []order.Option
		deadline time.Duration
		until    time.Duration
	}{
		{
			name:     "valores por defecto: dos minutos de espera y cuatro de tope",
			deadline: 2 * time.Minute,
			until:    4 * time.Minute,
		},
		{
			name:     "ventana configurada",
			opts:     []order.Option{order.WithReconcileWindow(90 * time.Second)},
			deadline: 2 * time.Minute,
			until:    3*time.Minute + 30*time.Second,
		},
		{
			name:     "ventana no positiva conserva el valor por defecto",
			opts:     []order.Option{order.WithReconcileWindow(-time.Minute)},
			deadline: 2 * time.Minute,
			until:    4 * time.Minute,
		},
		{
			name:     "los dos plazos salen del mismo ahora",
			opts:     []order.Option{order.WithLineDeadline(time.Minute), order.WithReconcileWindow(2 * time.Minute)},
			deadline: time.Minute,
			until:    3 * time.Minute,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setup := newReconcileFixture(t, acceptedDrinkMachine(), tc.opts...)

			line := setup.line(t)
			if want := engineNow.Add(tc.deadline); !line.DeadlineAt.Equal(want) {
				t.Fatalf("DeadlineAt=%v, se esperaba %v", line.DeadlineAt, want)
			}
			if want := engineNow.Add(tc.until); !line.ReconcileUntil.Equal(want) {
				t.Fatalf("ReconcileUntil=%v, se esperaba %v", line.ReconcileUntil, want)
			}
		})
	}
}

func TestEngineReconcileDueMarksADeliveredLineFromTheProvider(t *testing.T) {
	machine := acceptedDrinkMachine()
	machine.status = dispenser.Status{State: dispenser.LineDelivered, Ref: "order-no-1"}

	setup := newReconcileFixture(t, machine)
	setup.clock.set(engineNow.Add(2 * time.Minute))

	if err := setup.reconcileDue(t); err != nil {
		t.Fatalf("ReconcileDue: %v", err)
	}

	if machine.reconcileCount() != 1 {
		t.Fatalf("la máquina recibió %d consultas de reconciliación, se esperaba 1", machine.reconcileCount())
	}
	if machine.dispatchCount() != 1 {
		t.Fatalf("la reconciliación volvió a despachar: %d despachos, se esperaba 1", machine.dispatchCount())
	}

	req := machine.onlyReconcile(t)
	wantLine := setup.placed.Lines[0]

	if req.OrderID != setup.placed.ID || req.LineID != wantLine.ID {
		t.Fatalf("la consulta llevó order=%q line=%q, se esperaba order=%q line=%q", req.OrderID, req.LineID, setup.placed.ID, wantLine.ID)
	}
	if req.Ref != "exchange-code-1" {
		t.Fatalf("la consulta llevó Ref=%q, se esperaba el asa guardada en la línea", req.Ref)
	}
	if req.SKU != "sku-batido" || req.AmountCents != 1500 {
		t.Fatalf("la consulta llevó SKU=%q importe=%d", req.SKU, req.AmountCents)
	}
	if req.Machine.DeviceID != "device-1" {
		t.Fatalf("la consulta llevó la máquina %+v, se esperaba la de la línea", req.Machine)
	}

	line := setup.line(t)
	if line.State != dispenser.LineDelivered {
		t.Fatalf("la línea quedó en %q, se esperaba %q", line.State, dispenser.LineDelivered)
	}
	if !line.DeadlineAt.IsZero() {
		t.Fatalf("la línea entregada conserva el plazo %v", line.DeadlineAt)
	}
	if !line.ReconcileUntil.IsZero() {
		t.Fatalf("la línea entregada conserva el tope de reconciliación %v", line.ReconcileUntil)
	}
	if line.LastError != "" {
		t.Fatalf("la línea entregada conserva el error %q", line.LastError)
	}

	if got := setup.order(t); got.Status != store.OrderDelivered {
		t.Fatalf("Status=%q, se esperaba %q: la orden no puede quedarse en %q", got.Status, store.OrderDelivered, store.OrderDispatching)
	}
}

func TestEngineReconcileDueMarksAFailedLineAndSettlesTheOrder(t *testing.T) {
	machine := acceptedDrinkMachine()
	machine.status = dispenser.Status{State: dispenser.LineFailed, Reason: "la máquina informó que no entregó"}

	setup := newReconcileFixture(t, machine)
	setup.clock.set(engineNow.Add(2 * time.Minute))

	if err := setup.reconcileDue(t); err != nil {
		t.Fatalf("ReconcileDue: %v", err)
	}

	if machine.dispatchCount() != 1 {
		t.Fatalf("la máquina recibió %d despachos, se esperaba 1: una línea resuelta por reconciliación se informa, no se redespacha", machine.dispatchCount())
	}

	line := setup.line(t)
	if line.State != dispenser.LineFailed {
		t.Fatalf("la línea quedó en %q, se esperaba %q", line.State, dispenser.LineFailed)
	}
	if !line.DeadlineAt.IsZero() || !line.ReconcileUntil.IsZero() {
		t.Fatalf("la línea fallida conserva plazos: deadline=%v until=%v", line.DeadlineAt, line.ReconcileUntil)
	}
	if !strings.Contains(line.LastError, "no entregó") {
		t.Fatalf("LastError=%q, se esperaba el motivo del proveedor", line.LastError)
	}

	if got := setup.order(t); got.Status != store.OrderFailed {
		t.Fatalf("Status=%q, se esperaba %q: la orden no puede quedarse en %q", got.Status, store.OrderFailed, store.OrderDispatching)
	}
}

func TestEngineReconcileDueKeepsAskingWhileTheWindowIsOpen(t *testing.T) {
	cases := []struct {
		name     string
		opts     []order.Option
		interval time.Duration
	}{
		{name: "intervalo por defecto", interval: 30 * time.Second},
		{name: "intervalo configurado", opts: []order.Option{order.WithReconcileInterval(time.Minute)}, interval: time.Minute},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			machine := acceptedDrinkMachine()
			machine.status = dispenser.Status{State: dispenser.LineInDoubt, Reason: "el proveedor no informa el pedido"}

			setup := newReconcileFixture(t, machine, tc.opts...)
			until := setup.line(t).ReconcileUntil

			setup.clock.set(engineNow.Add(2 * time.Minute))

			if err := setup.reconcileDue(t); err != nil {
				t.Fatalf("ReconcileDue: %v", err)
			}

			line := setup.line(t)
			if want := engineNow.Add(2*time.Minute + tc.interval); !line.DeadlineAt.Equal(want) {
				t.Fatalf("DeadlineAt=%v, se esperaba %v: la duda empuja el plazo un intervalo", line.DeadlineAt, want)
			}
			if !line.ReconcileUntil.Equal(until) {
				t.Fatalf("ReconcileUntil=%v, se esperaba %v sin tocar: el tope no se mueve", line.ReconcileUntil, until)
			}
			if line.State != dispenser.LineSent {
				t.Fatalf("la línea quedó en %q, se esperaba %q: mientras la ventana está abierta la línea sigue en vuelo para que el worker la vuelva a ver", line.State, dispenser.LineSent)
			}
			if !strings.Contains(line.LastError, "no informa el pedido") {
				t.Fatalf("LastError=%q, se esperaba el motivo informado por el proveedor", line.LastError)
			}
		})
	}
}

func TestEngineReconcileDueClampsTheLastScanToTheHardStop(t *testing.T) {
	machine := acceptedDrinkMachine()
	machine.status = dispenser.Status{State: dispenser.LineInDoubt, Reason: "el proveedor no informa el pedido"}

	setup := newReconcileFixture(t, machine)
	until := setup.line(t).ReconcileUntil

	setup.clock.set(until.Add(-15 * time.Second))

	if err := setup.reconcileDue(t); err != nil {
		t.Fatalf("ReconcileDue: %v", err)
	}

	line := setup.line(t)
	if !line.DeadlineAt.Equal(until) {
		t.Fatalf("DeadlineAt=%v, se esperaba %v: el último escaneo cae exactamente en el tope, nunca después", line.DeadlineAt, until)
	}
	if !line.ReconcileUntil.Equal(until) {
		t.Fatalf("ReconcileUntil=%v, se esperaba %v sin tocar", line.ReconcileUntil, until)
	}
}

func TestEngineReconcileDueClosesTheLineInDoubtAtTheHardStop(t *testing.T) {
	machine := acceptedDrinkMachine()
	machine.status = dispenser.Status{State: dispenser.LineInDoubt, Reason: "el proveedor no informa el pedido"}
	notifier := &recordingNotifier{}

	setup := newReconcileFixture(t, machine, order.WithNotifier(notifier))
	until := setup.line(t).ReconcileUntil

	setup.clock.set(until)

	if err := setup.reconcileDue(t); err != nil {
		t.Fatalf("ReconcileDue: %v", err)
	}

	line := setup.line(t)
	if line.State != dispenser.LineInDoubt {
		t.Fatalf("la línea quedó en %q, se esperaba %q: sin dato no se afirma nada", line.State, dispenser.LineInDoubt)
	}
	if !line.DeadlineAt.IsZero() {
		t.Fatalf("la línea en duda conserva el plazo %v: se escanearía para siempre", line.DeadlineAt)
	}
	if !line.ReconcileUntil.IsZero() {
		t.Fatalf("la línea en duda conserva el tope %v", line.ReconcileUntil)
	}

	if got := setup.order(t); got.Status != store.OrderInDoubt {
		t.Fatalf("Status=%q, se esperaba %q", got.Status, store.OrderInDoubt)
	}

	event := notifier.only(t)
	if event.OrderID != setup.placed.ID {
		t.Fatalf("el evento apunta a la orden %q, se esperaba %q", event.OrderID, setup.placed.ID)
	}
	if event.Kind != "order_in_doubt" {
		t.Fatalf("Kind=%q, se esperaba %q", event.Kind, "order_in_doubt")
	}
	if event.Detail == "" {
		t.Fatal("el evento quedó sin detalle para la persona que tiene que actuar")
	}

	if err := setup.reconcileDue(t); err != nil {
		t.Fatalf("ReconcileDue sobre una línea ya cerrada: %v", err)
	}
	if machine.reconcileCount() != 1 {
		t.Fatalf("la línea se volvió a consultar: %d consultas, se esperaba 1", machine.reconcileCount())
	}
	if notifier.count() != 1 {
		t.Fatalf("el notificador recibió %d eventos, se esperaba exactamente 1", notifier.count())
	}
}

func TestEngineReconcileDueKeepsTheLineWhenTheProviderAnswersWithAnError(t *testing.T) {
	machine := acceptedDrinkMachine()
	machine.reconcileErr = errors.New("dial tcp 10.0.0.1:443: connect: connection refused")

	setup := newReconcileFixture(t, machine)
	setup.clock.set(engineNow.Add(2 * time.Minute))

	err := setup.reconcileDue(t)
	if err == nil {
		t.Fatal("ReconcileDue devolvió nil con un error del proveedor")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("el error devuelto %q, se esperaba el del proveedor", err.Error())
	}

	line := setup.line(t)
	if line.State != dispenser.LineSent {
		t.Fatalf("la línea quedó en %q, se esperaba %q: un error de consulta no resuelve la línea ni la pierde", line.State, dispenser.LineSent)
	}
	if !strings.Contains(line.LastError, "connection refused") {
		t.Fatalf("LastError=%q, se esperaba el mensaje del error", line.LastError)
	}
	if want := engineNow.Add(2*time.Minute + 30*time.Second); !line.DeadlineAt.Equal(want) {
		t.Fatalf("DeadlineAt=%v, se esperaba %v: la duda empuja el plazo un intervalo", line.DeadlineAt, want)
	}
	if want := engineNow.Add(4 * time.Minute); !line.ReconcileUntil.Equal(want) {
		t.Fatalf("ReconcileUntil=%v, se esperaba %v sin tocar", line.ReconcileUntil, want)
	}

	if got := setup.order(t); got.Status != store.OrderDispatching {
		t.Fatalf("Status=%q, se esperaba %q: la línea sigue en vuelo", got.Status, store.OrderDispatching)
	}
}

func TestEngineReconcileDueIgnoresALineThatIsNotDue(t *testing.T) {
	machine := acceptedDrinkMachine()
	machine.status = dispenser.Status{State: dispenser.LineDelivered}

	setup := newReconcileFixture(t, machine)
	before := setup.line(t)

	if err := setup.reconcileDue(t); err != nil {
		t.Fatalf("ReconcileDue: %v", err)
	}

	if machine.reconcileCount() != 0 {
		t.Fatalf("la máquina recibió %d consultas con el plazo en el futuro, se esperaban 0", machine.reconcileCount())
	}

	after := setup.line(t)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("la línea cambió sin estar vencida: %+v, se esperaba %+v", after, before)
	}
}
