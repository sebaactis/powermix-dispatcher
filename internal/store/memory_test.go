package store_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/store"
)

const (
	drinkLineID = "line-batido"
	snackLineID = "line-snack"
)

var baseTime = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func fixtureOrder() store.Order {
	return store.Order{
		ID:          "order-1",
		KioskID:     "kiosk-1",
		ComboID:     "combo-1",
		AmountCents: 2500,
		Currency:    "ARS",
		Status:      store.OrderPaid,
		CreatedAt:   baseTime,
		PaidAt:      baseTime.Add(time.Minute),
		Lines: []store.Line{
			{
				ID:          drinkLineID,
				OrderID:     "order-1",
				Position:    1,
				MachineType: "batidos",
				SKU:         "sku-batido",
				AmountCents: 1500,
				State:       dispenser.LinePending,
				Ref:         "exchange-code-1",
			},
			{
				ID:          snackLineID,
				OrderID:     "order-1",
				Position:    2,
				MachineType: "snacks",
				SKU:         "sku-snack",
				AmountCents: 1000,
				State:       dispenser.LinePending,
			},
		},
	}
}

func mustCreate(t *testing.T, m *store.Memory, order store.Order) {
	t.Helper()
	if err := m.CreateOrder(context.Background(), order); err != nil {
		t.Fatalf("CreateOrder(%q): %v", order.ID, err)
	}
}

func mustOrder(t *testing.T, m *store.Memory, orderID string) store.Order {
	t.Helper()
	order, err := m.Order(context.Background(), orderID)
	if err != nil {
		t.Fatalf("Order(%q): %v", orderID, err)
	}
	return order
}

func mustSaveLine(t *testing.T, m *store.Memory, line store.Line) {
	t.Helper()
	if err := m.SaveLine(context.Background(), line); err != nil {
		t.Fatalf("SaveLine(%q): %v", line.ID, err)
	}
}

func mustDue(t *testing.T, m *store.Memory, now time.Time, limit int) []store.Line {
	t.Helper()
	due, err := m.DueLines(context.Background(), now, limit)
	if err != nil {
		t.Fatalf("DueLines(limit=%d): %v", limit, err)
	}
	return due
}

func lineIDs(lines []store.Line) []string {
	ids := make([]string, 0, len(lines))
	for _, line := range lines {
		ids = append(ids, line.ID)
	}
	return ids
}

func lineByID(t *testing.T, lines []store.Line, lineID string) store.Line {
	t.Helper()
	for _, line := range lines {
		if line.ID == lineID {
			return line
		}
	}
	t.Fatalf("no se encontró la línea %q en %v", lineID, lineIDs(lines))
	return store.Line{}
}

func TestMemoryCreateAndReadOrderRoundTrip(t *testing.T) {
	m := store.NewMemory()
	want := fixtureOrder()
	mustCreate(t, m, want)

	got := mustOrder(t, m, want.ID)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Order devolvió %+v, se esperaba %+v", got, want)
	}
}

func TestMemoryRejectsDuplicateOrderID(t *testing.T) {
	m := store.NewMemory()
	mustCreate(t, m, fixtureOrder())

	duplicate := fixtureOrder()
	duplicate.AmountCents = 9999
	err := m.CreateOrder(context.Background(), duplicate)
	if !errors.Is(err, store.ErrAlreadyExists) {
		t.Fatalf("CreateOrder duplicado devolvió %v, se esperaba %v", err, store.ErrAlreadyExists)
	}

	got := mustOrder(t, m, "order-1")
	if got.AmountCents != 2500 {
		t.Fatalf("el duplicado pisó la orden guardada: amount_cents=%d, se esperaba 2500", got.AmountCents)
	}
}

func TestMemoryOrderReturnsNotFoundForUnknownOrder(t *testing.T) {
	m := store.NewMemory()

	got, err := m.Order(context.Background(), "order-inexistente")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Order devolvió %v, se esperaba %v", err, store.ErrNotFound)
	}
	if got.ID != "" || len(got.Lines) != 0 {
		t.Fatalf("Order devolvió %+v junto al error, se esperaba una orden vacía", got)
	}
}

func TestMemorySaveOrderReturnsNotFoundForUnknownOrder(t *testing.T) {
	m := store.NewMemory()

	err := m.SaveOrder(context.Background(), fixtureOrder())
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SaveOrder devolvió %v, se esperaba %v", err, store.ErrNotFound)
	}

	if _, err := m.Order(context.Background(), "order-1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SaveOrder creó la orden: Order devolvió %v", err)
	}
}

func TestMemorySaveOrderReplacesTheWholeOrder(t *testing.T) {
	m := store.NewMemory()
	mustCreate(t, m, fixtureOrder())

	edited := mustOrder(t, m, "order-1")
	edited.Status = store.OrderDispatching
	edited.ClosedAt = baseTime.Add(2 * time.Minute)
	edited.Lines = edited.Lines[:1]
	edited.Lines[0].State = dispenser.LineSent
	edited.Lines[0].Ref = "order-no-1"
	edited.Lines[0].Attempts = 1
	edited.Lines[0].DeadlineAt = baseTime.Add(3 * time.Minute)

	if err := m.SaveOrder(context.Background(), edited); err != nil {
		t.Fatalf("SaveOrder: %v", err)
	}

	got := mustOrder(t, m, "order-1")
	if !reflect.DeepEqual(got, edited) {
		t.Fatalf("Order devolvió %+v, se esperaba %+v", got, edited)
	}

	err := m.SaveLine(context.Background(), store.Line{ID: snackLineID, OrderID: "order-1", State: dispenser.LineDelivered})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SaveLine sobre una línea reemplazada devolvió %v, se esperaba %v", err, store.ErrNotFound)
	}
}

func TestMemorySaveLineUpdatesOnlyThatLine(t *testing.T) {
	m := store.NewMemory()
	mustCreate(t, m, fixtureOrder())

	line := lineByID(t, mustOrder(t, m, "order-1").Lines, drinkLineID)
	line.State = dispenser.LineBrewing
	line.Attempts = 2
	line.Ref = "order-no-1"
	line.DeadlineAt = baseTime.Add(2 * time.Minute)
	line.LastError = "el proveedor no respondió"
	mustSaveLine(t, m, line)

	got := mustOrder(t, m, "order-1")
	if !reflect.DeepEqual(lineByID(t, got.Lines, drinkLineID), line) {
		t.Fatalf("la línea guardada no se leyó igual: %+v", lineByID(t, got.Lines, drinkLineID))
	}

	wantSnack := fixtureOrder().Lines[1]
	if !reflect.DeepEqual(lineByID(t, got.Lines, snackLineID), wantSnack) {
		t.Fatalf("SaveLine tocó otra línea: %+v, se esperaba %+v", lineByID(t, got.Lines, snackLineID), wantSnack)
	}
}

func TestMemorySaveLineReturnsNotFoundForUnknownLine(t *testing.T) {
	m := store.NewMemory()
	mustCreate(t, m, fixtureOrder())

	err := m.SaveLine(context.Background(), store.Line{
		ID:      "line-inexistente",
		OrderID: "order-1",
		State:   dispenser.LineDelivered,
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SaveLine devolvió %v, se esperaba %v", err, store.ErrNotFound)
	}

	got := mustOrder(t, m, "order-1")
	if len(got.Lines) != 2 {
		t.Fatalf("SaveLine creó una línea: la orden quedó con %d líneas", len(got.Lines))
	}
}

func TestMemoryAppendAttemptRejectsDuplicateIdempotencyKey(t *testing.T) {
	m := store.NewMemory()
	ctx := context.Background()
	mustCreate(t, m, fixtureOrder())

	first := store.Attempt{
		ID:             "attempt-1",
		LineID:         drinkLineID,
		Number:         1,
		IdempotencyKey: "key-1",
		Outcome:        "accepted",
		StartedAt:      baseTime,
		FinishedAt:     baseTime.Add(time.Second),
	}
	if err := m.AppendAttempt(ctx, first); err != nil {
		t.Fatalf("AppendAttempt: %v", err)
	}

	repeated := first
	repeated.ID = "attempt-2"
	repeated.Number = 2
	repeated.Outcome = "error"
	if err := m.AppendAttempt(ctx, repeated); !errors.Is(err, store.ErrAlreadyExists) {
		t.Fatalf("AppendAttempt con clave repetida devolvió %v, se esperaba %v", err, store.ErrAlreadyExists)
	}

	otherLine := first
	otherLine.ID = "attempt-3"
	otherLine.LineID = snackLineID
	if err := m.AppendAttempt(ctx, otherLine); !errors.Is(err, store.ErrAlreadyExists) {
		t.Fatalf("AppendAttempt con clave repetida en otra línea devolvió %v, se esperaba %v", err, store.ErrAlreadyExists)
	}

	otherKey := first
	otherKey.ID = "attempt-4"
	otherKey.LineID = snackLineID
	otherKey.IdempotencyKey = "key-2"
	otherKey.Outcome = "ambiguous"
	if err := m.AppendAttempt(ctx, otherKey); err != nil {
		t.Fatalf("AppendAttempt con clave nueva: %v", err)
	}
}

func TestMemorySaveAttemptUpdatesTheExistingAttempt(t *testing.T) {
	m := store.NewMemory()
	ctx := context.Background()

	attempt := store.Attempt{
		ID:             "attempt-1",
		LineID:         drinkLineID,
		Number:         1,
		IdempotencyKey: "key-1",
		Outcome:        "ambiguous",
		StartedAt:      baseTime,
	}
	if err := m.AppendAttempt(ctx, attempt); err != nil {
		t.Fatalf("AppendAttempt: %v", err)
	}

	updated := attempt
	updated.ID = "attempt-1-cerrado"
	updated.Outcome = "accepted"
	updated.Detail = "la máquina aceptó el despacho"
	updated.FinishedAt = baseTime.Add(time.Second)
	if err := m.SaveAttempt(ctx, updated); err != nil {
		t.Fatalf("SaveAttempt: %v", err)
	}

	if err := m.AppendAttempt(ctx, updated); !errors.Is(err, store.ErrAlreadyExists) {
		t.Fatalf("SaveAttempt liberó la clave de idempotencia: AppendAttempt devolvió %v, se esperaba %v", err, store.ErrAlreadyExists)
	}

	other := attempt
	other.ID = "attempt-2"
	other.LineID = snackLineID
	other.IdempotencyKey = "key-2"
	if err := m.AppendAttempt(ctx, other); err != nil {
		t.Fatalf("SaveAttempt dejó el store sin aceptar una clave nueva: %v", err)
	}
}

func TestMemorySaveAttemptReturnsNotFoundForUnknownKey(t *testing.T) {
	m := store.NewMemory()
	ctx := context.Background()

	unknown := store.Attempt{
		ID:             "attempt-1",
		LineID:         drinkLineID,
		Number:         1,
		IdempotencyKey: "key-inexistente",
		Outcome:        "accepted",
		StartedAt:      baseTime,
	}

	err := m.SaveAttempt(ctx, unknown)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SaveAttempt devolvió %v, se esperaba %v", err, store.ErrNotFound)
	}

	if err := m.AppendAttempt(ctx, unknown); err != nil {
		t.Fatalf("SaveAttempt creó el intento: AppendAttempt devolvió %v", err)
	}
}

func TestMemorySaveAttemptDoesNotChangeTheStoredAttemptCount(t *testing.T) {
	m := store.NewMemory()
	ctx := context.Background()

	first := store.Attempt{
		ID:             "attempt-1",
		LineID:         drinkLineID,
		Number:         1,
		IdempotencyKey: "key-1",
		Outcome:        "ambiguous",
		StartedAt:      baseTime,
	}
	if err := m.AppendAttempt(ctx, first); err != nil {
		t.Fatalf("AppendAttempt: %v", err)
	}

	for i := 0; i < 3; i++ {
		updated := first
		updated.Outcome = "accepted"
		updated.FinishedAt = baseTime.Add(time.Duration(i+1) * time.Second)
		if err := m.SaveAttempt(ctx, updated); err != nil {
			t.Fatalf("SaveAttempt #%d: %v", i+1, err)
		}
	}

	if err := m.AppendAttempt(ctx, first); !errors.Is(err, store.ErrAlreadyExists) {
		t.Fatalf("tras tres actualizaciones AppendAttempt devolvió %v, se esperaba %v", err, store.ErrAlreadyExists)
	}

	second := store.Attempt{
		ID:             "attempt-2",
		LineID:         snackLineID,
		Number:         1,
		IdempotencyKey: "key-2",
		Outcome:        "ambiguous",
		StartedAt:      baseTime,
	}
	if err := m.AppendAttempt(ctx, second); err != nil {
		t.Fatalf("el store quedó rechazando claves nuevas después de actualizar: %v", err)
	}
}

func TestMemoryRegisterWebhookDeduplicatesBySourceAndKey(t *testing.T) {
	m := store.NewMemory()
	ctx := context.Background()

	cases := []struct {
		name      string
		webhook   store.Webhook
		wantFirst bool
	}{
		{
			name:      "la primera vez el evento es nuevo",
			webhook:   store.Webhook{ID: "wh-1", Source: "payment", DedupeKey: "evt-1", ReceivedAt: baseTime},
			wantFirst: true,
		},
		{
			name:      "la repetición no es nueva",
			webhook:   store.Webhook{ID: "wh-2", Source: "payment", DedupeKey: "evt-1", ReceivedAt: baseTime.Add(time.Minute)},
			wantFirst: false,
		},
		{
			name:      "otra clave es nueva",
			webhook:   store.Webhook{ID: "wh-3", Source: "payment", DedupeKey: "evt-2", ReceivedAt: baseTime},
			wantFirst: true,
		},
		{
			name:      "la misma clave en otra fuente es nueva",
			webhook:   store.Webhook{ID: "wh-4", Source: "batidos", DedupeKey: "evt-1", ReceivedAt: baseTime},
			wantFirst: true,
		},
		{
			name:      "la repetición en la otra fuente tampoco es nueva",
			webhook:   store.Webhook{ID: "wh-5", Source: "batidos", DedupeKey: "evt-1", ReceivedAt: baseTime},
			wantFirst: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			first, err := m.RegisterWebhook(ctx, tc.webhook)
			if err != nil {
				t.Fatalf("RegisterWebhook: %v", err)
			}
			if first != tc.wantFirst {
				t.Fatalf("first=%v, se esperaba %v", first, tc.wantFirst)
			}
		})
	}
}

func TestMemoryDueLinesSelectsAndOrdersDeadlines(t *testing.T) {
	m := store.NewMemory()
	now := baseTime.Add(10 * time.Minute)

	first := store.Order{
		ID:          "order-1",
		KioskID:     "kiosk-1",
		ComboID:     "combo-1",
		AmountCents: 300,
		Currency:    "ARS",
		Status:      store.OrderDispatching,
		CreatedAt:   baseTime,
		Lines: []store.Line{
			{ID: "line-a", OrderID: "order-1", Position: 1, MachineType: "batidos", State: dispenser.LineSent, DeadlineAt: baseTime.Add(time.Minute)},
			{ID: "line-b", OrderID: "order-1", Position: 2, MachineType: "snacks", State: dispenser.LineBrewing, DeadlineAt: baseTime.Add(time.Minute)},
			{ID: "line-c", OrderID: "order-1", Position: 3, MachineType: "batidos", State: dispenser.LineSent, DeadlineAt: baseTime.Add(5 * time.Minute)},
			{ID: "line-future", OrderID: "order-1", Position: 4, MachineType: "snacks", State: dispenser.LineSent, DeadlineAt: now.Add(time.Second)},
			{ID: "line-sin-plazo", OrderID: "order-1", Position: 5, MachineType: "batidos", State: dispenser.LineBrewing},
			{ID: "line-delivered", OrderID: "order-1", Position: 6, MachineType: "snacks", State: dispenser.LineDelivered, DeadlineAt: baseTime},
			{ID: "line-failed", OrderID: "order-1", Position: 7, MachineType: "batidos", State: dispenser.LineFailed, DeadlineAt: baseTime},
			{ID: "line-in-doubt", OrderID: "order-1", Position: 8, MachineType: "snacks", State: dispenser.LineInDoubt, DeadlineAt: baseTime},
			{ID: "line-pending", OrderID: "order-1", Position: 9, MachineType: "batidos", State: dispenser.LinePending, DeadlineAt: baseTime},
		},
	}
	second := store.Order{
		ID:          "order-2",
		KioskID:     "kiosk-2",
		ComboID:     "combo-1",
		AmountCents: 100,
		Currency:    "ARS",
		Status:      store.OrderDispatching,
		CreatedAt:   baseTime,
		Lines: []store.Line{
			{ID: "line-justo-ahora", OrderID: "order-2", Position: 1, MachineType: "snacks", State: dispenser.LineBrewing, DeadlineAt: now},
		},
	}

	mustCreate(t, m, first)
	mustCreate(t, m, second)

	wantAll := []string{"line-a", "line-b", "line-c", "line-justo-ahora"}

	cases := []struct {
		limit int
		want  []string
	}{
		{limit: 0, want: wantAll},
		{limit: -1, want: wantAll},
		{limit: 100, want: wantAll},
		{limit: 4, want: wantAll},
		{limit: 3, want: wantAll[:3]},
		{limit: 2, want: wantAll[:2]},
		{limit: 1, want: wantAll[:1]},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("limit=%d", tc.limit), func(t *testing.T) {
			got := lineIDs(mustDue(t, m, now, tc.limit))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("DueLines devolvió %v, se esperaba %v", got, tc.want)
			}
		})
	}
}

func TestMemoryDueLinesIsEmptyForAnEmptyStore(t *testing.T) {
	m := store.NewMemory()

	due := mustDue(t, m, baseTime, 0)
	if len(due) != 0 {
		t.Fatalf("DueLines devolvió %v en un store vacío", lineIDs(due))
	}
}

func TestMemoryReturnsCopiesOfStoredOrders(t *testing.T) {
	m := store.NewMemory()
	input := fixtureOrder()
	deadline := baseTime.Add(time.Minute)
	input.Lines[0].State = dispenser.LineSent
	input.Lines[0].DeadlineAt = deadline
	mustCreate(t, m, input)

	input.Status = store.OrderCancelled
	input.Lines[0].State = dispenser.LineDelivered
	input.Lines[0].DeadlineAt = baseTime.Add(time.Hour)
	input.Lines = append(input.Lines, store.Line{ID: "line-extra"})

	stored := mustOrder(t, m, input.ID)
	if stored.Status != store.OrderPaid {
		t.Fatalf("mutar la orden de entrada cambió lo guardado: status=%q", stored.Status)
	}
	if len(stored.Lines) != 2 {
		t.Fatalf("mutar la orden de entrada agregó líneas: %v", lineIDs(stored.Lines))
	}
	if stored.Lines[0].State != dispenser.LineSent {
		t.Fatalf("mutar la orden de entrada cambió la línea guardada: state=%q", stored.Lines[0].State)
	}
	if !stored.Lines[0].DeadlineAt.Equal(deadline) {
		t.Fatalf("mutar la orden de entrada cambió el plazo guardado: %v", stored.Lines[0].DeadlineAt)
	}

	stored.Lines[0].State = dispenser.LineFailed
	stored.Lines[0].DeadlineAt = baseTime
	stored.Lines = append(stored.Lines, store.Line{ID: "line-extra"})

	again := mustOrder(t, m, input.ID)
	if len(again.Lines) != 2 {
		t.Fatalf("mutar la orden leída agregó líneas: %v", lineIDs(again.Lines))
	}
	if again.Lines[0].State != dispenser.LineSent {
		t.Fatalf("mutar la orden leída cambió lo guardado: state=%q", again.Lines[0].State)
	}
	if !again.Lines[0].DeadlineAt.Equal(deadline) {
		t.Fatalf("mutar la orden leída cambió el plazo guardado: %v", again.Lines[0].DeadlineAt)
	}
}

func TestMemoryReturnsCopiesOfDueLines(t *testing.T) {
	m := store.NewMemory()
	order := fixtureOrder()
	order.Lines[0].State = dispenser.LineSent
	order.Lines[0].DeadlineAt = baseTime
	mustCreate(t, m, order)

	due := mustDue(t, m, baseTime, 0)
	if len(due) != 1 || due[0].ID != drinkLineID {
		t.Fatalf("DueLines devolvió %v, se esperaba [%s]", lineIDs(due), drinkLineID)
	}

	due[0].State = dispenser.LineDelivered
	due[0].DeadlineAt = time.Time{}
	due[0].Attempts = 99

	again := mustDue(t, m, baseTime, 0)
	if len(again) != 1 {
		t.Fatalf("mutar el resultado cambió el store: %v", lineIDs(again))
	}
	if again[0].State != dispenser.LineSent {
		t.Fatalf("mutar el resultado cambió el estado guardado: %q", again[0].State)
	}
	if !again[0].DeadlineAt.Equal(baseTime) {
		t.Fatalf("mutar el resultado cambió el plazo guardado: %v", again[0].DeadlineAt)
	}
	if again[0].Attempts != 0 {
		t.Fatalf("mutar el resultado cambió los intentos guardados: %d", again[0].Attempts)
	}

	stored := mustOrder(t, m, order.ID)
	if stored.Lines[0].State != dispenser.LineSent {
		t.Fatalf("mutar el resultado de DueLines cambió la orden: %q", stored.Lines[0].State)
	}
}

func TestMemoryIsSafeForConcurrentUse(t *testing.T) {
	const workers = 64

	m := store.NewMemory()
	ctx := context.Background()
	now := baseTime.Add(time.Hour)

	var wg sync.WaitGroup
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		go func(i int) {
			defer wg.Done()

			orderID := fmt.Sprintf("order-%03d", i)
			lineID := fmt.Sprintf("line-%03d", i)
			order := store.Order{
				ID:          orderID,
				KioskID:     "kiosk-1",
				ComboID:     "combo-1",
				AmountCents: 100,
				Currency:    "ARS",
				Status:      store.OrderDispatching,
				CreatedAt:   baseTime,
				Lines: []store.Line{{
					ID:          lineID,
					OrderID:     orderID,
					Position:    1,
					MachineType: "snacks",
					SKU:         "sku-snack",
					AmountCents: 100,
					State:       dispenser.LineSent,
					DeadlineAt:  baseTime,
				}},
			}

			if err := m.CreateOrder(ctx, order); err != nil {
				t.Errorf("CreateOrder(%q): %v", orderID, err)
				return
			}

			loaded, err := m.Order(ctx, orderID)
			if err != nil {
				t.Errorf("Order(%q): %v", orderID, err)
				return
			}
			if len(loaded.Lines) != 1 {
				t.Errorf("Order(%q) devolvió %d líneas, se esperaba 1", orderID, len(loaded.Lines))
				return
			}

			line := loaded.Lines[0]
			line.State = dispenser.LineBrewing
			line.Attempts++
			if err := m.SaveLine(ctx, line); err != nil {
				t.Errorf("SaveLine(%q): %v", lineID, err)
			}

			attempt := store.Attempt{
				ID:             "attempt-" + lineID,
				LineID:         lineID,
				Number:         1,
				IdempotencyKey: "key-" + lineID,
				Outcome:        "accepted",
				StartedAt:      baseTime,
			}
			if err := m.AppendAttempt(ctx, attempt); err != nil {
				t.Errorf("AppendAttempt(%q): %v", attempt.ID, err)
			}

			webhook := store.Webhook{
				ID:         "wh-" + lineID,
				Source:     "batidos",
				DedupeKey:  "dedupe-" + lineID,
				ReceivedAt: baseTime,
			}
			first, err := m.RegisterWebhook(ctx, webhook)
			if err != nil {
				t.Errorf("RegisterWebhook(%q): %v", webhook.ID, err)
			} else if !first {
				t.Errorf("RegisterWebhook(%q) devolvió first=false", webhook.ID)
			}

			if _, err := m.DueLines(ctx, now, 10); err != nil {
				t.Errorf("DueLines: %v", err)
			}
		}(i)
	}

	wg.Wait()

	for i := 0; i < workers; i++ {
		orderID := fmt.Sprintf("order-%03d", i)
		order, err := m.Order(ctx, orderID)
		if err != nil {
			t.Fatalf("Order(%q): %v", orderID, err)
		}
		if len(order.Lines) != 1 {
			t.Fatalf("Order(%q) devolvió %d líneas, se esperaba 1", orderID, len(order.Lines))
		}
		if order.Lines[0].State != dispenser.LineBrewing {
			t.Fatalf("Order(%q) tiene state=%q, se esperaba %q", orderID, order.Lines[0].State, dispenser.LineBrewing)
		}
		if order.Lines[0].Attempts != 1 {
			t.Fatalf("Order(%q) tiene attempts=%d, se esperaba 1", orderID, order.Lines[0].Attempts)
		}
	}

	due := mustDue(t, m, now, 0)
	if len(due) != workers {
		t.Fatalf("DueLines devolvió %d líneas, se esperaban %d", len(due), workers)
	}
}
