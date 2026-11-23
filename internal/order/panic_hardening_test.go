package order_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/linestate"
	"compras-remotas/internal/machine"
	"compras-remotas/internal/notify"
	"compras-remotas/internal/order"
	"compras-remotas/internal/store"
)

func TestEnginePanickedAttemptIsClosedAndCounted(t *testing.T) {
	_ = panicCaptureDefaultSlog(t)
	ctx := context.Background()
	st := newRecordingStore()

	engine := order.New(
		st,
		map[machine.Kind]dispenser.Dispenser{"batidos": &panicDispenser{panicVal: "batidos boom"}},
		order.WithClock(frozenClock()),
		order.WithIDGenerator(sequentialIDs()),
	)

	placed := place(t, engine, drinkOnlyDraft())
	if err := engine.MarkPaid(ctx, placed.ID, engineNow); err == nil {
		t.Fatal("se esperaba el error del panic recuperado")
	}

	line := lineFor(t, mustOrder(t, st, placed.ID), "batidos")
	if line.State != linestate.InDoubt {
		t.Fatalf("la línea quedó en %q, se esperaba %q", line.State, linestate.InDoubt)
	}
	if line.Attempts != 1 {
		t.Fatalf("Attempts = %d, se esperaba 1: el intento con panic tiene que contarse", line.Attempts)
	}

	saved := st.savedFor(t, line.ID)
	if saved.Outcome != store.OutcomeError {
		t.Fatalf("Outcome = %q, se esperaba %q", saved.Outcome, store.OutcomeError)
	}
	if saved.FinishedAt.IsZero() {
		t.Fatal("el intento con panic quedó sin FinishedAt")
	}
	if !strings.Contains(saved.Detail, "batidos boom") {
		t.Fatalf("Detail = %q, se esperaba el motivo del panic", saved.Detail)
	}
}

// panicOnDoubtStore entra en panic al guardar una línea en duda, es decir,
// dentro del manejador que registra el panic del despacho.
type panicOnDoubtStore struct {
	*store.Memory
}

func (s *panicOnDoubtStore) SaveLine(ctx context.Context, line store.Line) (store.Line, error) {
	if line.State == linestate.InDoubt {
		panic("store boom al marcar en duda")
	}
	return s.Memory.SaveLine(ctx, line)
}

func TestEngineSurvivesAPanicInsideThePanicHandler(t *testing.T) {
	_ = panicCaptureDefaultSlog(t)
	ctx := context.Background()
	st := &panicOnDoubtStore{Memory: store.NewMemory()}

	engine := order.New(
		st,
		map[machine.Kind]dispenser.Dispenser{"batidos": &panicDispenser{panicVal: "batidos boom"}},
		order.WithClock(frozenClock()),
		order.WithIDGenerator(sequentialIDs()),
	)

	placed := place(t, engine, drinkOnlyDraft())
	err := engine.MarkPaid(ctx, placed.ID, engineNow)
	if err == nil || !strings.Contains(err.Error(), "store boom al marcar en duda") {
		t.Fatalf("err = %v, se esperaba el segundo panic como error", err)
	}

	line := lineFor(t, mustOrder(t, st, placed.ID), "batidos")
	if line.State != linestate.Pending || line.DeadlineAt.IsZero() {
		t.Fatalf("línea = %+v, se esperaba pending con plazo para que el barrido la retome", line)
	}
}

type panickingNotifier struct{}

func (panickingNotifier) Notify(context.Context, notify.Event) error {
	panic("notifier boom")
}

func TestEngineSurvivesAPanicWhileNotifyingADispatchPanic(t *testing.T) {
	_ = panicCaptureDefaultSlog(t)
	ctx := context.Background()
	st := store.NewMemory()

	engine := order.New(
		st,
		map[machine.Kind]dispenser.Dispenser{"batidos": &panicDispenser{panicVal: "batidos boom"}},
		order.WithClock(frozenClock()),
		order.WithIDGenerator(sequentialIDs()),
		order.WithNotifier(panickingNotifier{}),
	)

	placed := place(t, engine, drinkOnlyDraft())
	err := engine.MarkPaid(ctx, placed.ID, engineNow)
	if err == nil || !strings.Contains(err.Error(), "notifier boom") {
		t.Fatalf("err = %v, se esperaba el panic del aviso como error", err)
	}

	if line := lineFor(t, mustOrder(t, st, placed.ID), "batidos"); line.State != linestate.InDoubt {
		t.Fatalf("la línea quedó en %q, se esperaba %q", line.State, linestate.InDoubt)
	}
}

func TestEngineBackgroundPanicOutsideALineSettlesAndAlerts(t *testing.T) {
	_ = panicCaptureDefaultSlog(t)
	now := time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC)
	st := &panicOnLoadOrderStore{Memory: store.NewMemory()}
	notifier := &panicFakeNotifier{}
	gw := newPanicFakeGateway()

	var bgMu sync.Mutex
	var bgErr error

	engine := order.New(
		st,
		map[machine.Kind]dispenser.Dispenser{
			machine.Batidos: &panicDispenser{result: dispenser.DispatchResult{Accepted: true}},
			machine.Snacks:  &panicDispenser{result: dispenser.DispatchResult{Accepted: true}},
		},
		order.WithClock(func() time.Time { return now }),
		order.WithIDGenerator(panicSeqIDs()),
		order.WithNotifier(notifier),
		order.WithQRPayments("mercadopago", gw),
		order.WithBackgroundErrors(func(_ string, err error) {
			bgMu.Lock()
			defer bgMu.Unlock()
			bgErr = err
		}),
	)

	placed, err := engine.CreateOrder(context.Background(), panicTestDraft())
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	intent, err := engine.RequestQR(context.Background(), placed.ID, "pos-1")
	if err != nil {
		t.Fatalf("RequestQR: %v", err)
	}

	gw.approve(intent.ProviderID, placed.AmountCents)
	// LoadOrder #1 = claimPaid, #2 = settleOrder dentro de spawn (entra en panic).
	st.arm(2, errors.New("settle boom"))

	if err := engine.SyncPayment(context.Background(), intent.ProviderID); err != nil {
		t.Fatalf("SyncPayment: %v", err)
	}
	engine.Wait()

	bgMu.Lock()
	gotErr := bgErr
	bgMu.Unlock()
	if gotErr == nil {
		t.Fatal("se esperaba el error del panic en backgroundErrors")
	}

	loaded, err := st.Memory.LoadOrder(context.Background(), placed.ID)
	if err != nil {
		t.Fatalf("LoadOrder: %v", err)
	}
	if loaded.Status != store.OrderDispatching {
		t.Fatalf("la orden quedó en %q, se esperaba %q tras recalcularla", loaded.Status, store.OrderDispatching)
	}

	events := notifier.snapshot()
	if len(events) != 1 || events[0].Kind != notify.KindDispatchPanic || events[0].OrderID != placed.ID {
		t.Fatalf("avisos = %+v, se esperaba 1 %q para la orden", events, notify.KindDispatchPanic)
	}
}

func TestEngineSweepRecoversAPanicWhileResumingAStalledLine(t *testing.T) {
	_ = panicCaptureDefaultSlog(t)
	ctx := context.Background()
	st := newRecordingStore()
	clock := manualClockAt(engineNow)
	notifier := &panicFakeNotifier{}

	engine := order.New(
		st,
		map[machine.Kind]dispenser.Dispenser{"batidos": &panicDispenser{panicVal: "sweep boom"}},
		order.WithClock(clock.clock()),
		order.WithIDGenerator(sequentialIDs()),
		order.WithNotifier(notifier),
	)

	placed := place(t, engine, drinkOnlyDraft())

	stalled := mustOrder(t, st, placed.ID)
	stalled.Status = store.OrderPaid
	stalled.PaidAt = engineNow
	if _, err := st.SaveOrder(ctx, stalled); err != nil {
		t.Fatalf("SaveOrder: %v", err)
	}

	clock.set(engineNow.Add(5 * time.Minute))
	if err := engine.SyncOverdueLines(ctx, 10); err == nil {
		t.Fatal("se esperaba el error del panic recuperado")
	}

	line := lineFor(t, mustOrder(t, st, placed.ID), "batidos")
	if line.State != linestate.InDoubt || line.Attempts != 1 {
		t.Fatalf("línea = %+v, se esperaba in_doubt con 1 intento", line)
	}
	if saved := st.savedFor(t, line.ID); saved.Outcome != store.OutcomeError || saved.FinishedAt.IsZero() {
		t.Fatalf("intento = %+v, se esperaba cerrado con error", saved)
	}

	found := false
	for _, ev := range notifier.snapshot() {
		if ev.Kind == notify.KindDispatchPanic && ev.OrderID == placed.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("avisos = %+v, se esperaba %q", notifier.snapshot(), notify.KindDispatchPanic)
	}
}
