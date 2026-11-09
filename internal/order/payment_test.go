package order_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/machine"
	"compras-remotas/internal/order"
	"compras-remotas/internal/store"
)

func TestEngineConfirmPaymentDispatchesBothMachines(t *testing.T) {
	st := newRecordingStore()
	batido := &stubMachine{attempt: dispenser.DispatchResult{Accepted: true, ProviderRef: "order-no-1"}}
	snack := &stubMachine{attempt: dispenser.DispatchResult{Accepted: true}}
	engine := order.New(
		st,
		map[machine.Kind]dispenser.Dispenser{"batidos": batido, "snacks": snack},
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
	snack := &stubMachine{attempt: dispenser.DispatchResult{Accepted: true}}
	engine := order.New(
		st,
		map[machine.Kind]dispenser.Dispenser{"batidos": batido, "snacks": snack},
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
		map[machine.Kind]dispenser.Dispenser{
			"batidos": &stubMachine{attempt: dispenser.DispatchResult{Accepted: true}},
			"snacks":  &stubMachine{attempt: dispenser.DispatchResult{Accepted: true}},
		},
		order.WithClock(frozenClock()),
		order.WithIDGenerator(sequentialIDs()),
	)

	placed := place(t, placingEngine, comboDraft())

	batido := &stubMachine{attempt: dispenser.DispatchResult{Accepted: true, ProviderRef: "order-no-1"}}
	snack := &stubMachine{attempt: dispenser.DispatchResult{Accepted: true}}
	afterRestart := order.New(
		st,
		map[machine.Kind]dispenser.Dispenser{"batidos": batido, "snacks": snack},
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
			snack := &stubMachine{attempt: dispenser.DispatchResult{Accepted: true}}
			engine := order.New(
				st,
				map[machine.Kind]dispenser.Dispenser{"batidos": batido, "snacks": snack},
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
		map[machine.Kind]dispenser.Dispenser{"batidos": batido, "snacks": snack},
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
	batido := &stubMachine{attempt: dispenser.DispatchResult{Accepted: true}}
	snack := &stubMachine{attempt: dispenser.DispatchResult{Accepted: true}}
	engine := order.New(
		st,
		map[machine.Kind]dispenser.Dispenser{"batidos": batido, "snacks": snack},
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
	batido := &stubMachine{attempt: dispenser.DispatchResult{Accepted: true}}
	engine := order.New(
		st,
		map[machine.Kind]dispenser.Dispenser{"batidos": batido},
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
				map[machine.Kind]dispenser.Dispenser{
					"batidos": &stubMachine{attempt: dispenser.DispatchResult{Accepted: true}},
					"snacks":  &stubMachine{attempt: dispenser.DispatchResult{Accepted: true}},
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

func TestEngineConfirmPaymentKeepsARetriedLinePendingInsteadOfFailed(t *testing.T) {
	st := newRecordingStore()
	batido := &flakyMachine{rejections: 1, attempt: dispenser.DispatchResult{Accepted: true, ProviderRef: "order-no-1"}}
	snack := &stubMachine{attempt: dispenser.DispatchResult{Accepted: true}}
	engine := order.New(
		st,
		map[machine.Kind]dispenser.Dispenser{"batidos": batido, "snacks": snack},
		order.WithClock(frozenClock()),
		order.WithIDGenerator(sequentialIDs()),
	)

	placed := place(t, engine, comboDraft())
	if err := engine.ConfirmPayment(context.Background(), placed.ID, engineNow); err != nil {
		t.Fatalf("ConfirmPayment: %v", err)
	}

	line := lineFor(t, mustOrder(t, st, placed.ID), "batidos")

	got := st.lineStatesFor(line.ID)
	want := []dispenser.LineState{dispenser.LinePending, dispenser.LineSent}
	if !slices.Equal(got, want) {
		t.Fatalf("la línea reintentada se guardó con los estados %v, se esperaba %v: un rechazo con intentos pendientes no pasa por failed", got, want)
	}
}

func TestEngineConfirmPaymentRetriesARejectedLineWithAFreshKey(t *testing.T) {
	st := newRecordingStore()
	batido := &flakyMachine{rejections: 1, attempt: dispenser.DispatchResult{Accepted: true, ProviderRef: "order-no-1"}}
	snack := &stubMachine{attempt: dispenser.DispatchResult{Accepted: true}}
	engine := order.New(
		st,
		map[machine.Kind]dispenser.Dispenser{"batidos": batido, "snacks": snack},
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
	snack := &stubMachine{attempt: dispenser.DispatchResult{Accepted: true}}
	engine := order.New(
		st,
		map[machine.Kind]dispenser.Dispenser{"batidos": batido, "snacks": snack},
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
	snack := &stubMachine{attempt: dispenser.DispatchResult{Accepted: true}}
	engine := order.New(
		st,
		map[machine.Kind]dispenser.Dispenser{"batidos": batido, "snacks": snack},
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
	snack := &stubMachine{attempt: dispenser.DispatchResult{Accepted: true}}
	engine := order.New(
		st,
		map[machine.Kind]dispenser.Dispenser{"batidos": batido, "snacks": snack},
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
	batido := &stubMachine{attempt: dispenser.DispatchResult{Accepted: true}}
	engine := order.New(
		st,
		map[machine.Kind]dispenser.Dispenser{"batidos": batido},
		order.WithClock(frozenClock()),
		order.WithIDGenerator(sequentialIDs()),
	)

	placed := place(t, engine, drinkOnlyDraft())
	line := placed.Lines[0]
	line.Attempts = 2

	if _, err := st.SaveLine(context.Background(), line); err != nil {
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
