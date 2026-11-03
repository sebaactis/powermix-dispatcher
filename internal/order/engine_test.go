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
	"compras-remotas/internal/dispenser/batidos"
	"compras-remotas/internal/dispenser/snacks"
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
				MachineType: "batidos",
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
				MachineType: "snacks",
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
		if line.MachineType != want.MachineType {
			t.Fatalf("la línea %d tiene MachineType=%q, se esperaba %q", i+1, line.MachineType, want.MachineType)
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
			mutate:       func(draft *order.Draft) { draft.Items[0].MachineType = "" },
			wantPosition: "ítem 1",
		},
		{
			name:         "con un tipo de máquina desconocido",
			mutate:       func(draft *order.Draft) { draft.Items[1].MachineType = "cafetera" },
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
	batido := &stubMachine{err: &batidos.APIError{HTTPStatus: 400, Code: 40005, Message: "nonce repetido"}}
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
			if !doubtful.DeadlineAt.IsZero() {
				t.Fatalf("una línea incierta no tiene plazo armado: %v", doubtful.DeadlineAt)
			}

			if saved := st.savedFor(t, doubtful.ID); saved.Outcome != "error" {
				t.Fatalf("el intento incierto se cerró como %q, se esperaba %q", saved.Outcome, "error")
			}
		})
	}
}

func TestEngineConfirmPaymentFailsTheOrderWhenBothMachinesReject(t *testing.T) {
	st := newRecordingStore()
	batido := &stubMachine{err: &batidos.APIError{HTTPStatus: 400, Code: 40004, Message: "firma inválida"}}
	snack := &stubMachine{err: &snacks.APIError{HTTPStatus: 401, Code: 40004, Message: "firma inválida"}}
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
