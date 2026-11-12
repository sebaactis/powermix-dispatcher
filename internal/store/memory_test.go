package store_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"compras-remotas/internal/linestate"
	"compras-remotas/internal/machine"
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
				ID:       drinkLineID,
				OrderID:  "order-1",
				Position: 1,
				Machine: machine.Ref{
					KioskID:      "kiosk-1",
					Kind:         "batidos",
					Name:         "Batidos Powermix",
					DeviceID:     "device-1",
					DeviceNo:     "1",
					DeviceTypeID: "2",
					MID:          "mid-batidos",
				},
				SKU:            "sku-batido",
				AmountCents:    1500,
				State:          linestate.Pending,
				Ref:            "exchange-code-1",
				ReconcileUntil: baseTime.Add(4 * time.Minute),
			},
			{
				ID:       snackLineID,
				OrderID:  "order-1",
				Position: 2,
				Machine: machine.Ref{
					KioskID: "kiosk-1",
					Kind:    "snacks",
					Name:    "Snacks Powermix",
					MID:     "mid-snacks",
				},
				SKU:         "sku-snack",
				AmountCents: 1000,
				State:       linestate.Pending,
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
	order, err := m.LoadOrder(context.Background(), orderID)
	if err != nil {
		t.Fatalf("Order(%q): %v", orderID, err)
	}
	return order
}

func mustSaveLine(t *testing.T, m *store.Memory, line store.Line) store.Line {
	t.Helper()

	saved, err := m.SaveLine(context.Background(), line)
	if err != nil {
		t.Fatalf("SaveLine(%q): %v", line.ID, err)
	}

	return saved
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

	got, err := m.LoadOrder(context.Background(), "order-inexistente")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Order devolvió %v, se esperaba %v", err, store.ErrNotFound)
	}
	if got.ID != "" || len(got.Lines) != 0 {
		t.Fatalf("Order devolvió %+v junto al error, se esperaba una orden vacía", got)
	}
}

func TestMemorySaveOrderReturnsNotFoundForUnknownOrder(t *testing.T) {
	m := store.NewMemory()

	_, err := m.SaveOrder(context.Background(), fixtureOrder())
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SaveOrder devolvió %v, se esperaba %v", err, store.ErrNotFound)
	}

	if _, err := m.LoadOrder(context.Background(), "order-1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SaveOrder creó la orden: Order devolvió %v", err)
	}
}

func TestMemorySaveOrderOnlyPersistsTheHeader(t *testing.T) {
	m := store.NewMemory()
	mustCreate(t, m, fixtureOrder())

	stale := mustOrder(t, m, "order-1")

	line := stale.Lines[0]
	line.State = linestate.Sent
	line.Ref = "order-no-1"

	saved, err := m.SaveLine(context.Background(), line)
	if err != nil {
		t.Fatalf("SaveLine: %v", err)
	}

	if saved.Version != line.Version+1 {
		t.Fatalf("la línea guardada debía quedar en la versión %d y quedó en la %d", line.Version+1, saved.Version)
	}

	stale.Status = store.OrderDispatching
	stale.ClosedAt = baseTime.Add(2 * time.Minute)
	stale.Lines = stale.Lines[:1]
	stale.Lines[0].State = linestate.Delivered
	stale.Lines[0].Ref = "ref-vieja"

	if _, err := m.SaveOrder(context.Background(), stale); err != nil {
		t.Fatalf("SaveOrder: %v", err)
	}

	got := mustOrder(t, m, "order-1")

	if got.Status != store.OrderDispatching {
		t.Fatalf("el estado de la orden sí se persiste y quedó %q", got.Status)
	}

	if len(got.Lines) != len(stale.Lines)+1 {
		t.Fatalf("SaveOrder no debe tocar las líneas: se guardaron %d y quedaron %d", len(stale.Lines), len(got.Lines))
	}

	if got.Lines[0].State != linestate.Sent || got.Lines[0].Ref != "order-no-1" {
		t.Fatalf("la línea escrita por su propio camino debe sobrevivir al guardado de la orden: %+v", got.Lines[0])
	}

	if got.Lines[0].Version != saved.Version {
		t.Fatalf("SaveOrder no debe revertir la versión de la línea: %d y %d", got.Lines[0].Version, saved.Version)
	}
}

func TestMemorySaveOrderReturnsTheNextVersion(t *testing.T) {
	m := store.NewMemory()
	mustCreate(t, m, fixtureOrder())

	loaded := mustOrder(t, m, "order-1")

	if loaded.Version != 0 {
		t.Fatalf("la orden nació con Version=%d, se esperaba 0", loaded.Version)
	}

	loaded.Status = store.OrderDispatching
	loaded.ClosedAt = baseTime.Add(2 * time.Minute)

	saved, err := m.SaveOrder(context.Background(), loaded)
	if err != nil {
		t.Fatalf("SaveOrder: %v", err)
	}

	if saved.Version != loaded.Version+1 {
		t.Fatalf("SaveOrder devolvió Version=%d, se esperaba %d", saved.Version, loaded.Version+1)
	}
	if saved.Status != store.OrderDispatching || !saved.ClosedAt.Equal(baseTime.Add(2*time.Minute)) {
		t.Fatalf("SaveOrder devolvió %+v, se esperaba lo que se guardó", saved)
	}

	stored := mustOrder(t, m, "order-1")
	if stored.Version != saved.Version {
		t.Fatalf("la orden guardada quedó en Version=%d, se esperaba %d", stored.Version, saved.Version)
	}
	if stored.Status != store.OrderDispatching || !stored.ClosedAt.Equal(baseTime.Add(2*time.Minute)) {
		t.Fatalf("la orden guardada no coincide con lo devuelto: %+v", stored)
	}
}

func TestMemorySaveOrderRejectsAStaleVersionAndKeepsTheStoredOrder(t *testing.T) {
	m := store.NewMemory()
	mustCreate(t, m, fixtureOrder())

	stale := mustOrder(t, m, "order-1")

	fresh := stale
	fresh.Status = store.OrderDispatching
	fresh.PaidAt = baseTime.Add(2 * time.Minute)
	fresh.ClosedAt = baseTime.Add(3 * time.Minute)

	saved, err := m.SaveOrder(context.Background(), fresh)
	if err != nil {
		t.Fatalf("el primer guardado tenía que aceptarse: %v", err)
	}

	stale.Status = store.OrderFailed
	stale.PaidAt = baseTime.Add(9 * time.Minute)
	stale.ClosedAt = baseTime.Add(9 * time.Minute)

	if _, err := m.SaveOrder(context.Background(), stale); !errors.Is(err, store.ErrStale) {
		t.Fatalf("SaveOrder con una versión vieja devolvió %v, se esperaba %v", err, store.ErrStale)
	}

	stored := mustOrder(t, m, "order-1")
	if stored.Version != saved.Version {
		t.Fatalf("un guardado con versión vieja movió la versión: %d, se esperaba %d", stored.Version, saved.Version)
	}
	if stored.Status != store.OrderDispatching {
		t.Fatalf("un guardado con versión vieja pisó el estado: %q, se esperaba %q", stored.Status, store.OrderDispatching)
	}
	if !stored.PaidAt.Equal(fresh.PaidAt) {
		t.Fatalf("un guardado con versión vieja pisó paid_at: %v, se esperaba %v", stored.PaidAt, fresh.PaidAt)
	}
	if !stored.ClosedAt.Equal(fresh.ClosedAt) {
		t.Fatalf("un guardado con versión vieja pisó closed_at: %v, se esperaba %v", stored.ClosedAt, fresh.ClosedAt)
	}
}

func TestMemoryLineKeepsItsMachineThroughThePort(t *testing.T) {
	m := store.NewMemory()
	want := fixtureOrder()
	mustCreate(t, m, want)

	created := mustOrder(t, m, want.ID)
	if created.Lines[0].Machine != want.Lines[0].Machine {
		t.Fatalf("CreateOrder perdió la máquina: %+v, se esperaba %+v", created.Lines[0].Machine, want.Lines[0].Machine)
	}
	if created.Lines[1].Machine != want.Lines[1].Machine {
		t.Fatalf("CreateOrder perdió la máquina: %+v, se esperaba %+v", created.Lines[1].Machine, want.Lines[1].Machine)
	}

	line := lineByID(t, created.Lines, drinkLineID)
	line.Machine.MID = "mid-cambiado"
	line.State = linestate.Sent
	mustSaveLine(t, m, line)

	saved := lineByID(t, mustOrder(t, m, want.ID).Lines, drinkLineID)
	if saved.Machine != line.Machine {
		t.Fatalf("SaveLine perdió la máquina: %+v, se esperaba %+v", saved.Machine, line.Machine)
	}

	read := lineByID(t, mustOrder(t, m, want.ID).Lines, drinkLineID)
	read.Machine.DeviceID = "device-tocado"
	read.Machine.MID = "mid-tocado"

	again := lineByID(t, mustOrder(t, m, want.ID).Lines, drinkLineID)
	if again.Machine != line.Machine {
		t.Fatalf("mutar la máquina de la línea leída cambió lo guardado: %+v, se esperaba %+v", again.Machine, line.Machine)
	}

	untouched := lineByID(t, mustOrder(t, m, want.ID).Lines, snackLineID)
	if untouched.Machine != want.Lines[1].Machine {
		t.Fatalf("SaveLine tocó la máquina de otra línea: %+v, se esperaba %+v", untouched.Machine, want.Lines[1].Machine)
	}
}

func TestMemorySaveLineUpdatesOnlyThatLine(t *testing.T) {
	m := store.NewMemory()
	mustCreate(t, m, fixtureOrder())

	line := lineByID(t, mustOrder(t, m, "order-1").Lines, drinkLineID)
	line.State = linestate.Brewing
	line.Attempts = 2
	line.Ref = "order-no-1"
	line.DeadlineAt = baseTime.Add(2 * time.Minute)
	line.ReconcileUntil = baseTime.Add(4 * time.Minute)
	line.LastError = "el proveedor no respondió"
	line = mustSaveLine(t, m, line)

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

	_, err := m.SaveLine(context.Background(), store.Line{
		ID:      "line-inexistente",
		OrderID: "order-1",
		State:   linestate.Delivered,
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SaveLine devolvió %v, se esperaba %v", err, store.ErrNotFound)
	}

	got := mustOrder(t, m, "order-1")
	if len(got.Lines) != 2 {
		t.Fatalf("SaveLine creó una línea: la orden quedó con %d líneas", len(got.Lines))
	}
}

func TestMemoryCreateOrderStartsEveryLineAtVersionZero(t *testing.T) {
	m := store.NewMemory()
	created := fixtureOrder()

	for i := range created.Lines {
		created.Lines[i].Version = 7
	}

	mustCreate(t, m, created)

	for _, line := range mustOrder(t, m, created.ID).Lines {
		if line.Version != 0 {
			t.Fatalf("la línea %q nació con Version=%d, se esperaba 0", line.ID, line.Version)
		}
	}
}

func TestMemorySaveLineReturnsThePersistedVersion(t *testing.T) {
	m := store.NewMemory()
	mustCreate(t, m, fixtureOrder())

	line := lineByID(t, mustOrder(t, m, "order-1").Lines, drinkLineID)
	line.State = linestate.Sent
	line.Ref = "order-no-1"

	saved := mustSaveLine(t, m, line)
	if saved.Version != line.Version+1 {
		t.Fatalf("SaveLine devolvió Version=%d, se esperaba %d", saved.Version, line.Version+1)
	}
	if saved.State != line.State || saved.Ref != line.Ref {
		t.Fatalf("SaveLine devolvió %+v, se esperaba lo que se guardó", saved)
	}

	stored := lineByID(t, mustOrder(t, m, "order-1").Lines, drinkLineID)
	if !reflect.DeepEqual(stored, saved) {
		t.Fatalf("la línea guardada es %+v, se esperaba %+v", stored, saved)
	}
}

func TestMemorySaveLineRejectsAStaleVersionAndKeepsTheStoredLine(t *testing.T) {
	m := store.NewMemory()
	mustCreate(t, m, fixtureOrder())

	stale := lineByID(t, mustOrder(t, m, "order-1").Lines, drinkLineID)

	fresh := stale
	fresh.State = linestate.Delivered
	saved := mustSaveLine(t, m, fresh)

	stale.State = linestate.Failed
	stale.LastError = "el worker guardó su copia vieja"

	_, err := m.SaveLine(context.Background(), stale)
	if !errors.Is(err, store.ErrStale) {
		t.Fatalf("SaveLine con una versión vieja devolvió %v, se esperaba %v", err, store.ErrStale)
	}

	stored := lineByID(t, mustOrder(t, m, "order-1").Lines, drinkLineID)
	if !reflect.DeepEqual(stored, saved) {
		t.Fatalf("un guardado con versión vieja tocó la línea: %+v, se esperaba %+v", stored, saved)
	}
	if stored.State != linestate.Delivered {
		t.Fatalf("el cierre de la línea se perdió: State=%q, se esperaba %q", stored.State, linestate.Delivered)
	}
}

func TestMemorySaveLineRejectsTheSecondSaveFromTheSameCopy(t *testing.T) {
	m := store.NewMemory()
	mustCreate(t, m, fixtureOrder())

	copied := lineByID(t, mustOrder(t, m, "order-1").Lines, drinkLineID)
	copied.State = linestate.Sent

	first := mustSaveLine(t, m, copied)
	if first.Version != copied.Version+1 {
		t.Fatalf("el primer guardado devolvió Version=%d, se esperaba %d", first.Version, copied.Version+1)
	}

	copied.State = linestate.Failed
	copied.LastError = "segundo guardado con la misma copia"

	_, err := m.SaveLine(context.Background(), copied)
	if !errors.Is(err, store.ErrStale) {
		t.Fatalf("el segundo guardado de la misma copia devolvió %v, se esperaba %v", err, store.ErrStale)
	}

	stored := lineByID(t, mustOrder(t, m, "order-1").Lines, drinkLineID)
	if !reflect.DeepEqual(stored, first) {
		t.Fatalf("el segundo guardado pisó la línea: %+v, se esperaba %+v", stored, first)
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
		name          string
		webhook       store.Webhook
		wantDuplicate bool
	}{
		{
			name:          "la primera vez el evento es nuevo",
			webhook:       store.Webhook{ID: "wh-1", Source: "payment", DedupeKey: "evt-1", ReceivedAt: baseTime},
			wantDuplicate: false,
		},
		{
			name:          "la repetición no es nueva",
			webhook:       store.Webhook{ID: "wh-2", Source: "payment", DedupeKey: "evt-1", ReceivedAt: baseTime.Add(time.Minute)},
			wantDuplicate: true,
		},
		{
			name:          "otra clave es nueva",
			webhook:       store.Webhook{ID: "wh-3", Source: "payment", DedupeKey: "evt-2", ReceivedAt: baseTime},
			wantDuplicate: false,
		},
		{
			name:          "la misma clave en otra fuente es nueva",
			webhook:       store.Webhook{ID: "wh-4", Source: "batidos", DedupeKey: "evt-1", ReceivedAt: baseTime},
			wantDuplicate: false,
		},
		{
			name:          "la repetición en la otra fuente tampoco es nueva",
			webhook:       store.Webhook{ID: "wh-5", Source: "batidos", DedupeKey: "evt-1", ReceivedAt: baseTime},
			wantDuplicate: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			duplicate, err := m.RegisterWebhook(ctx, tc.webhook)
			if err != nil {
				t.Fatalf("RegisterWebhook: %v", err)
			}
			if duplicate != tc.wantDuplicate {
				t.Fatalf("duplicate=%v, se esperaba %v", duplicate, tc.wantDuplicate)
			}
		})
	}
}

func dueLine(id string, position int, kind machine.Kind, state linestate.State, deadline time.Time) store.Line {
	return store.Line{
		ID:         id,
		OrderID:    "order-1",
		Position:   position,
		Machine:    machine.Ref{Kind: kind},
		State:      state,
		DeadlineAt: deadline,
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
			dueLine("line-a", 1, "batidos", linestate.Sent, baseTime.Add(time.Minute)),
			dueLine("line-b", 2, "snacks", linestate.Brewing, baseTime.Add(time.Minute)),
			dueLine("line-c", 3, "batidos", linestate.Sent, baseTime.Add(5*time.Minute)),
			dueLine("line-future", 4, "snacks", linestate.Sent, now.Add(time.Second)),
			dueLine("line-sin-plazo", 5, "batidos", linestate.Brewing, time.Time{}),
			dueLine("line-delivered", 6, "snacks", linestate.Delivered, baseTime),
			dueLine("line-failed", 7, "batidos", linestate.Failed, baseTime),
			dueLine("line-in-doubt", 8, "snacks", linestate.InDoubt, baseTime),
			dueLine("line-in-doubt-sin-plazo", 10, "snacks", linestate.InDoubt, time.Time{}),
			dueLine("line-pending", 9, "batidos", linestate.Pending, baseTime),
			dueLine("line-pending-sin-plazo", 11, "batidos", linestate.Pending, time.Time{}),
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
			{ID: "line-justo-ahora", OrderID: "order-2", Position: 1, Machine: machine.Ref{Kind: "snacks"}, State: linestate.Brewing, DeadlineAt: now},
		},
	}

	mustCreate(t, m, first)
	mustCreate(t, m, second)

	wantAll := []string{"line-in-doubt", "line-pending", "line-a", "line-b", "line-c", "line-justo-ahora"}

	cases := []struct {
		limit int
		want  []string
	}{
		{limit: 0, want: wantAll},
		{limit: -1, want: wantAll},
		{limit: 100, want: wantAll},
		{limit: len(wantAll), want: wantAll},
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
	input.Lines[0].State = linestate.Sent
	input.Lines[0].DeadlineAt = deadline
	mustCreate(t, m, input)

	input.Status = store.OrderCancelled
	input.Lines[0].State = linestate.Delivered
	input.Lines[0].DeadlineAt = baseTime.Add(time.Hour)
	input.Lines = append(input.Lines, store.Line{ID: "line-extra"})

	stored := mustOrder(t, m, input.ID)
	if stored.Status != store.OrderPaid {
		t.Fatalf("mutar la orden de entrada cambió lo guardado: status=%q", stored.Status)
	}
	if len(stored.Lines) != 2 {
		t.Fatalf("mutar la orden de entrada agregó líneas: %v", lineIDs(stored.Lines))
	}
	if stored.Lines[0].State != linestate.Sent {
		t.Fatalf("mutar la orden de entrada cambió la línea guardada: state=%q", stored.Lines[0].State)
	}
	if !stored.Lines[0].DeadlineAt.Equal(deadline) {
		t.Fatalf("mutar la orden de entrada cambió el plazo guardado: %v", stored.Lines[0].DeadlineAt)
	}

	stored.Lines[0].State = linestate.Failed
	stored.Lines[0].DeadlineAt = baseTime
	stored.Lines = append(stored.Lines, store.Line{ID: "line-extra"})

	again := mustOrder(t, m, input.ID)
	if len(again.Lines) != 2 {
		t.Fatalf("mutar la orden leída agregó líneas: %v", lineIDs(again.Lines))
	}
	if again.Lines[0].State != linestate.Sent {
		t.Fatalf("mutar la orden leída cambió lo guardado: state=%q", again.Lines[0].State)
	}
	if !again.Lines[0].DeadlineAt.Equal(deadline) {
		t.Fatalf("mutar la orden leída cambió el plazo guardado: %v", again.Lines[0].DeadlineAt)
	}
}

func TestMemoryReturnsCopiesOfDueLines(t *testing.T) {
	m := store.NewMemory()
	order := fixtureOrder()
	order.Lines[0].State = linestate.Sent
	order.Lines[0].DeadlineAt = baseTime
	mustCreate(t, m, order)

	due := mustDue(t, m, baseTime, 0)
	if len(due) != 1 || due[0].ID != drinkLineID {
		t.Fatalf("DueLines devolvió %v, se esperaba [%s]", lineIDs(due), drinkLineID)
	}

	due[0].State = linestate.Delivered
	due[0].DeadlineAt = time.Time{}
	due[0].Attempts = 99

	again := mustDue(t, m, baseTime, 0)
	if len(again) != 1 {
		t.Fatalf("mutar el resultado cambió el store: %v", lineIDs(again))
	}
	if again[0].State != linestate.Sent {
		t.Fatalf("mutar el resultado cambió el estado guardado: %q", again[0].State)
	}
	if !again[0].DeadlineAt.Equal(baseTime) {
		t.Fatalf("mutar el resultado cambió el plazo guardado: %v", again[0].DeadlineAt)
	}
	if again[0].Attempts != 0 {
		t.Fatalf("mutar el resultado cambió los intentos guardados: %d", again[0].Attempts)
	}

	stored := mustOrder(t, m, order.ID)
	if stored.Lines[0].State != linestate.Sent {
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
					Machine:     machine.Ref{Kind: "snacks"},
					SKU:         "sku-snack",
					AmountCents: 100,
					State:       linestate.Sent,
					DeadlineAt:  baseTime,
				}},
			}

			if err := m.CreateOrder(ctx, order); err != nil {
				t.Errorf("CreateOrder(%q): %v", orderID, err)
				return
			}

			loaded, err := m.LoadOrder(ctx, orderID)
			if err != nil {
				t.Errorf("Order(%q): %v", orderID, err)
				return
			}
			if len(loaded.Lines) != 1 {
				t.Errorf("Order(%q) devolvió %d líneas, se esperaba 1", orderID, len(loaded.Lines))
				return
			}

			line := loaded.Lines[0]
			line.State = linestate.Brewing
			line.Attempts++
			if _, err := m.SaveLine(ctx, line); err != nil {
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
			duplicate, err := m.RegisterWebhook(ctx, webhook)
			if err != nil {
				t.Errorf("RegisterWebhook(%q): %v", webhook.ID, err)
			} else if duplicate {
				t.Errorf("RegisterWebhook(%q) devolvió duplicate=true", webhook.ID)
			}

			if _, err := m.DueLines(ctx, now, 10); err != nil {
				t.Errorf("DueLines: %v", err)
			}
		}(i)
	}

	wg.Wait()

	for i := 0; i < workers; i++ {
		orderID := fmt.Sprintf("order-%03d", i)
		order, err := m.LoadOrder(ctx, orderID)
		if err != nil {
			t.Fatalf("Order(%q): %v", orderID, err)
		}
		if len(order.Lines) != 1 {
			t.Fatalf("Order(%q) devolvió %d líneas, se esperaba 1", orderID, len(order.Lines))
		}
		if order.Lines[0].State != linestate.Brewing {
			t.Fatalf("Order(%q) tiene state=%q, se esperaba %q", orderID, order.Lines[0].State, linestate.Brewing)
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

func TestMemoryRejectsADuplicateClientKeyAndFindsByKey(t *testing.T) {
	ctx := context.Background()
	m := store.NewMemory()

	first := store.Order{ID: "o-1", ClientKey: "tap-1", Status: store.OrderPendingPayment}
	if err := m.CreateOrder(ctx, first); err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}

	duplicate := store.Order{ID: "o-2", ClientKey: "tap-1", Status: store.OrderPendingPayment}
	if err := m.CreateOrder(ctx, duplicate); err != store.ErrAlreadyExists {
		t.Fatalf("CreateOrder con clave repetida = %v, se esperaba ErrAlreadyExists", err)
	}

	got, err := m.OrderByClientKey(ctx, "tap-1")
	if err != nil {
		t.Fatalf("OrderByClientKey: %v", err)
	}
	if got.ID != "o-1" {
		t.Fatalf("OrderByClientKey devolvió %q, se esperaba %q", got.ID, "o-1")
	}

	if _, err := m.OrderByClientKey(ctx, "no-existe"); err != store.ErrNotFound {
		t.Fatalf("OrderByClientKey de una clave desconocida = %v, se esperaba ErrNotFound", err)
	}

	for _, id := range []string{"o-3", "o-4"} {
		if err := m.CreateOrder(ctx, store.Order{ID: id, Status: store.OrderPendingPayment}); err != nil {
			t.Fatalf("dos órdenes sin clave tienen que convivir: %v", err)
		}
	}
	if _, err := m.OrderByClientKey(ctx, ""); err != store.ErrNotFound {
		t.Fatalf("la clave vacía no se indexa: %v", err)
	}
}

func unclaimedLine(lineID, orderID string, position int, kind machine.Kind) store.Line {
	return store.Line{
		ID:       lineID,
		OrderID:  orderID,
		Position: position,
		Machine:  machine.Ref{Kind: kind},
		State:    linestate.Pending,
	}
}

func TestMemoryDueLinesSelectsAClaimedPendingLineAndSkipsAnUnclaimedOne(t *testing.T) {
	m := store.NewMemory()
	now := baseTime.Add(10 * time.Minute)

	claimed := unclaimedLine("line-reclamada", "order-1", 1, "batidos")
	claimed.DeadlineAt = baseTime.Add(time.Minute)

	unclaimed := unclaimedLine("line-sin-plazo", "order-1", 2, "snacks")

	mustCreate(t, m, store.Order{
		ID:        "order-1",
		Status:    store.OrderDispatching,
		CreatedAt: baseTime,
		Lines:     []store.Line{claimed, unclaimed},
	})

	got := lineIDs(mustDue(t, m, now, 0))
	want := []string{"line-reclamada"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DueLines devolvió %v, se esperaba %v: una línea pendiente con plazo venció, una sin plazo no", got, want)
	}
}

func TestMemoryStalledLinesSelectsUnclaimedPendingLinesOfPaidOrders(t *testing.T) {
	m := store.NewMemory()
	cut := baseTime.Add(5 * time.Minute)

	stalledOrder := func(orderID string, status store.OrderStatus, paidAt time.Time, lines ...store.Line) store.Order {
		return store.Order{
			ID:        orderID,
			Status:    status,
			CreatedAt: baseTime,
			PaidAt:    paidAt,
			Lines:     lines,
		}
	}

	claimed := unclaimedLine("line-reclamada", "order-paga", 2, "snacks")
	claimed.DeadlineAt = baseTime.Add(time.Minute)

	sent := unclaimedLine("line-enviada", "order-paga", 3, "batidos")
	sent.State = linestate.Sent

	mustCreate(t, m, stalledOrder("order-paga", store.OrderPaid, baseTime,
		unclaimedLine("line-b", "order-paga", 1, "snacks"),
		claimed,
		sent,
		unclaimedLine("line-a", "order-paga", 4, "batidos"),
	))
	mustCreate(t, m, stalledOrder("order-despachando", store.OrderDispatching, baseTime.Add(time.Minute),
		unclaimedLine("line-c", "order-despachando", 1, "batidos"),
	))
	mustCreate(t, m, stalledOrder("order-reciente", store.OrderPaid, baseTime.Add(10*time.Minute),
		unclaimedLine("line-reciente", "order-reciente", 1, "batidos"),
	))
	mustCreate(t, m, stalledOrder("order-sin-pago", store.OrderPendingPayment, baseTime,
		unclaimedLine("line-sin-pago", "order-sin-pago", 1, "batidos"),
	))
	mustCreate(t, m, stalledOrder("order-entregada", store.OrderDelivered, baseTime,
		unclaimedLine("line-entregada", "order-entregada", 1, "batidos"),
	))

	wantAll := []string{"line-a", "line-b", "line-c"}

	cases := []struct {
		limit int
		want  []string
	}{
		{limit: 0, want: wantAll},
		{limit: -1, want: wantAll},
		{limit: 100, want: wantAll},
		{limit: 2, want: wantAll[:2]},
		{limit: 1, want: wantAll[:1]},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("limit=%d", tc.limit), func(t *testing.T) {
			stalled, err := m.StalledLines(context.Background(), cut, tc.limit)
			if err != nil {
				t.Fatalf("StalledLines(limit=%d): %v", tc.limit, err)
			}

			if got := lineIDs(stalled); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("StalledLines devolvió %v, se esperaba %v", got, tc.want)
			}
		})
	}

	none, err := m.StalledLines(context.Background(), baseTime.Add(-time.Minute), 10)
	if err != nil {
		t.Fatalf("StalledLines sin órdenes viejas: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("StalledLines devolvió %v antes del corte, se esperaba nada", lineIDs(none))
	}
}

func TestMemoryNotificationSentStartsFalseAndThenStaysTrue(t *testing.T) {
	m := store.NewMemory()
	ctx := context.Background()
	mustCreate(t, m, fixtureOrder())

	sent, err := m.NotificationSent(ctx, "order-1", "order_in_doubt")
	if err != nil {
		t.Fatalf("NotificationSent: %v", err)
	}
	if sent {
		t.Fatal("una orden recién creada informa un aviso enviado")
	}

	if err := m.RecordNotification(ctx, "order-1", "order_in_doubt", baseTime); err != nil {
		t.Fatalf("RecordNotification: %v", err)
	}

	sent, err = m.NotificationSent(ctx, "order-1", "order_in_doubt")
	if err != nil {
		t.Fatalf("NotificationSent: %v", err)
	}
	if !sent {
		t.Fatal("el aviso grabado no se informa como enviado")
	}

	if err := m.RecordNotification(ctx, "order-1", "order_in_doubt", baseTime.Add(time.Minute)); err != nil {
		t.Fatalf("grabar dos veces el mismo aviso devolvió error: %v", err)
	}

	sent, err = m.NotificationSent(ctx, "order-1", "order_in_doubt")
	if err != nil {
		t.Fatalf("NotificationSent: %v", err)
	}
	if !sent {
		t.Fatal("la segunda grabación borró el aviso")
	}
}

func TestMemoryNotificationSentKeepsTheKindsIndependent(t *testing.T) {
	m := store.NewMemory()
	ctx := context.Background()
	mustCreate(t, m, fixtureOrder())

	if err := m.RecordNotification(ctx, "order-1", "order_in_doubt", baseTime); err != nil {
		t.Fatalf("RecordNotification: %v", err)
	}

	cases := []struct {
		name string
		kind string
		want bool
	}{
		{name: "el aviso grabado está enviado", kind: "order_in_doubt", want: true},
		{name: "el otro tipo de aviso sigue pendiente", kind: "order_failed", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sent, err := m.NotificationSent(ctx, "order-1", tc.kind)
			if err != nil {
				t.Fatalf("NotificationSent(%q): %v", tc.kind, err)
			}
			if sent != tc.want {
				t.Fatalf("NotificationSent(%q)=%v, se esperaba %v", tc.kind, sent, tc.want)
			}
		})
	}
}

func TestMemoryNotificationSentKeepsTheOrdersIndependent(t *testing.T) {
	m := store.NewMemory()
	ctx := context.Background()
	mustCreate(t, m, fixtureOrder())
	mustCreate(t, m, store.Order{ID: "order-2", KioskID: "kiosk-1", ComboID: "combo-1", Status: store.OrderPaid, CreatedAt: baseTime})

	if err := m.RecordNotification(ctx, "order-1", "order_in_doubt", baseTime); err != nil {
		t.Fatalf("RecordNotification: %v", err)
	}

	sent, err := m.NotificationSent(ctx, "order-2", "order_in_doubt")
	if err != nil {
		t.Fatalf("NotificationSent: %v", err)
	}
	if sent {
		t.Fatal("el aviso de una orden se informó como enviado en otra")
	}
}

func TestMemoryNotificationsReturnNotFoundForAnUnknownOrder(t *testing.T) {
	m := store.NewMemory()
	ctx := context.Background()
	mustCreate(t, m, fixtureOrder())

	cases := []struct {
		name string
		call func() error
	}{
		{
			name: "preguntar por un aviso de una orden que no existe",
			call: func() error {
				_, err := m.NotificationSent(ctx, "order-fantasma", "order_in_doubt")

				return err
			},
		},
		{
			name: "grabar un aviso de una orden que no existe",
			call: func() error {
				return m.RecordNotification(ctx, "order-fantasma", "order_in_doubt", baseTime)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("err=%v, se esperaba %v", err, store.ErrNotFound)
			}
		})
	}
}

func TestMemoryCreateOrderRejectsLinesThatSQLRejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*store.Order)
		want   error
	}{
		{
			name: "una línea con una familia de máquina desconocida",
			mutate: func(order *store.Order) {
				order.Lines[1].Machine.Kind = "express"
			},
			want: store.ErrInvalidLine,
		},
		{
			name: "una línea sin familia de máquina",
			mutate: func(order *store.Order) {
				order.Lines[0].Machine.Kind = ""
			},
			want: store.ErrInvalidLine,
		},
		{
			name: "una línea con importe negativo",
			mutate: func(order *store.Order) {
				order.Lines[0].AmountCents = -1
			},
			want: store.ErrInvalidLine,
		},
		{
			name: "una línea con un estado que el esquema no conoce",
			mutate: func(order *store.Order) {
				order.Lines[1].State = "cancelled"
			},
			want: store.ErrInvalidLine,
		},
		{
			name: "dos líneas en la misma posición",
			mutate: func(order *store.Order) {
				order.Lines[1].Position = order.Lines[0].Position
			},
			want: store.ErrAlreadyExists,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := store.NewMemory()
			order := fixtureOrder()
			tc.mutate(&order)

			err := m.CreateOrder(context.Background(), order)
			if !errors.Is(err, tc.want) {
				t.Fatalf("CreateOrder devolvió %v, se esperaba %v", err, tc.want)
			}

			if _, err := m.LoadOrder(context.Background(), order.ID); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("una orden rechazada no puede quedar guardada: LoadOrder devolvió %v", err)
			}
		})
	}
}

func TestMemoryCreateOrderRejectsHeadersThatSQLRejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*store.Order)
	}{
		{
			name: "una orden con importe negativo",
			mutate: func(order *store.Order) {
				order.AmountCents = -1
			},
		},
		{
			name: "una orden con un estado que el esquema no conoce",
			mutate: func(order *store.Order) {
				order.Status = "cart"
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := store.NewMemory()
			order := fixtureOrder()
			tc.mutate(&order)

			err := m.CreateOrder(context.Background(), order)
			if !errors.Is(err, store.ErrInvalidOrder) {
				t.Fatalf("CreateOrder devolvió %v, se esperaba %v", err, store.ErrInvalidOrder)
			}

			if _, err := m.LoadOrder(context.Background(), order.ID); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("una orden rechazada no puede quedar guardada: LoadOrder devolvió %v", err)
			}
		})
	}
}

func TestMemorySaveLineRejectsLinesThatSQLRejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*store.Line)
		want   error
	}{
		{
			name: "una familia de máquina desconocida",
			mutate: func(line *store.Line) {
				line.Machine.Kind = "express"
			},
			want: store.ErrInvalidLine,
		},
		{
			name: "una línea sin familia de máquina",
			mutate: func(line *store.Line) {
				line.Machine.Kind = ""
			},
			want: store.ErrInvalidLine,
		},
		{
			name: "un importe negativo",
			mutate: func(line *store.Line) {
				line.AmountCents = -1
			},
			want: store.ErrInvalidLine,
		},
		{
			name: "un estado que el esquema no conoce",
			mutate: func(line *store.Line) {
				line.State = "cancelled"
			},
			want: store.ErrInvalidLine,
		},
		{
			name: "una posición que ya usa otra línea de la orden",
			mutate: func(line *store.Line) {
				line.Position = 2
			},
			want: store.ErrAlreadyExists,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := store.NewMemory()
			mustCreate(t, m, fixtureOrder())

			before := mustOrder(t, m, "order-1").Lines[0]

			changed := before
			tc.mutate(&changed)

			if _, err := m.SaveLine(context.Background(), changed); !errors.Is(err, tc.want) {
				t.Fatalf("SaveLine devolvió %v, se esperaba %v", err, tc.want)
			}

			after := mustOrder(t, m, "order-1").Lines[0]
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("una línea rechazada no puede quedar guardada: %+v, se esperaba %+v", after, before)
			}
		})
	}
}

func TestMemorySaveOrderRejectsHeadersThatSQLRejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*store.Order)
	}{
		{
			name: "un importe negativo",
			mutate: func(order *store.Order) {
				order.AmountCents = -1
			},
		},
		{
			name: "un estado que el esquema no conoce",
			mutate: func(order *store.Order) {
				order.Status = "cart"
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := store.NewMemory()
			mustCreate(t, m, fixtureOrder())

			before := mustOrder(t, m, "order-1")

			changed := before
			tc.mutate(&changed)

			if _, err := m.SaveOrder(context.Background(), changed); !errors.Is(err, store.ErrInvalidOrder) {
				t.Fatalf("SaveOrder devolvió %v, se esperaba %v", err, store.ErrInvalidOrder)
			}

			after := mustOrder(t, m, "order-1")
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("una orden rechazada no puede quedar guardada: %+v, se esperaba %+v", after, before)
			}
		})
	}
}

func TestMemoryAcceptsTheValuesSQLAccepts(t *testing.T) {
	m := store.NewMemory()

	order := fixtureOrder()
	order.AmountCents = 0
	order.Lines[0].AmountCents = 0
	order.Lines[0].State = linestate.State("refunded")
	order.Lines[0].Machine = machine.Ref{KioskID: "kiosk-1", Kind: "batidos", Name: "Batidos Powermix"}

	mustCreate(t, m, order)

	got := mustOrder(t, m, "order-1")
	if got.AmountCents != 0 || got.Lines[0].AmountCents != 0 {
		t.Fatalf("el importe cero es legal en el esquema y volvió distinto: %+v", got)
	}
	if got.Lines[0].State != linestate.State("refunded") {
		t.Fatalf("el estado refunded es legal en el esquema y volvió %q", got.Lines[0].State)
	}

	line := got.Lines[0]
	line.Attempts = 1
	if _, err := m.SaveLine(context.Background(), line); err != nil {
		t.Fatalf("una línea de batidos sin identificadores es legal en el esquema: %v", err)
	}
}

func TestMemoryChecksTheSchemaBeforeTheConflictAndTheVersionBeforeTheSchema(t *testing.T) {
	ctx := context.Background()
	m := store.NewMemory()
	mustCreate(t, m, fixtureOrder())

	repeated := fixtureOrder()
	repeated.AmountCents = -1

	if err := m.CreateOrder(ctx, repeated); !errors.Is(err, store.ErrInvalidOrder) {
		t.Fatalf("una orden repetida y además inválida devolvió %v, se esperaba %v: el CHECK va antes que la clave", err, store.ErrInvalidOrder)
	}

	staleLine := mustOrder(t, m, "order-1").Lines[0]
	staleLine.Version = 7
	staleLine.State = "cancelled"

	if _, err := m.SaveLine(ctx, staleLine); !errors.Is(err, store.ErrStale) {
		t.Fatalf("una línea con la versión vieja y un estado inválido devolvió %v, se esperaba %v: la versión va antes que el CHECK", err, store.ErrStale)
	}

	staleOrder := mustOrder(t, m, "order-1")
	staleOrder.Version = 7
	staleOrder.Status = "cart"

	if _, err := m.SaveOrder(ctx, staleOrder); !errors.Is(err, store.ErrStale) {
		t.Fatalf("una orden con la versión vieja y un estado inválido devolvió %v, se esperaba %v: la versión va antes que el CHECK", err, store.ErrStale)
	}
}
