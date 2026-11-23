package order_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/linestate"
	"compras-remotas/internal/machine"
	"compras-remotas/internal/notify"
	"compras-remotas/internal/order"
	"compras-remotas/internal/payments"
	"compras-remotas/internal/store"
)

type panicDispenser struct {
	panicVal   any
	result     dispenser.DispatchResult
	err        error
	reconcile  dispenser.Status
	reconcileE error
}

func (d *panicDispenser) Dispatch(ctx context.Context, req dispenser.Request) (dispenser.DispatchResult, error) {
	if d.panicVal != nil {
		panic(d.panicVal)
	}
	return d.result, d.err
}

func (d *panicDispenser) Reconcile(ctx context.Context, req dispenser.Request) (dispenser.Status, error) {
	return d.reconcile, d.reconcileE
}

type panicFakeNotifier struct {
	mu     sync.Mutex
	events []notify.Event
}

func (n *panicFakeNotifier) Notify(ctx context.Context, ev notify.Event) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.events = append(n.events, ev)
	return nil
}

func (n *panicFakeNotifier) snapshot() []notify.Event {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]notify.Event, len(n.events))
	copy(out, n.events)
	return out
}

type panicFakeGateway struct {
	mu      sync.Mutex
	charges map[string]payments.Charge
}

func newPanicFakeGateway() *panicFakeGateway {
	return &panicFakeGateway{charges: make(map[string]payments.Charge)}
}

func (g *panicFakeGateway) CreateQR(ctx context.Context, req payments.QRRequest) (payments.Charge, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	c := payments.Charge{
		ProviderID:  "mp-charge-1",
		ExternalRef: req.OrderID,
		QRData:      "qr-data-1",
		Status:      payments.ChargePending,
		AmountCents: req.AmountCents,
	}
	g.charges[c.ProviderID] = c
	return c, nil
}

func (g *panicFakeGateway) Lookup(ctx context.Context, providerID string) (payments.Charge, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	c, ok := g.charges[providerID]
	if !ok {
		return payments.Charge{}, payments.ErrNotFound
	}
	return c, nil
}

func (g *panicFakeGateway) Cancel(ctx context.Context, providerID string) (payments.Charge, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	c := g.charges[providerID]
	c.Status = payments.ChargeCancelled
	g.charges[providerID] = c
	return c, nil
}

func (g *panicFakeGateway) approve(providerID string, amountCents int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	c := g.charges[providerID]
	c.Status = payments.ChargeApproved
	c.PaidAmountCents = amountCents
	g.charges[providerID] = c
}

type panicOnLoadOrderStore struct {
	*store.Memory
	mu       sync.Mutex
	armed    bool
	calls    int
	panicAt  int
	panicVal any
}

func (s *panicOnLoadOrderStore) arm(panicAt int, val any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.armed = true
	s.calls = 0
	s.panicAt = panicAt
	s.panicVal = val
}

func (s *panicOnLoadOrderStore) LoadOrder(ctx context.Context, orderID string) (store.Order, error) {
	s.mu.Lock()
	armed := s.armed
	var call int
	if armed {
		s.calls++
		call = s.calls
	}
	panicAt := s.panicAt
	panicVal := s.panicVal
	s.mu.Unlock()

	if armed && call == panicAt {
		panic(panicVal)
	}
	return s.Memory.LoadOrder(ctx, orderID)
}

func panicCaptureDefaultSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)
	t.Cleanup(func() {
		slog.SetDefault(prev)
	})
	return &buf
}

func panicParseLogRecords(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("invalid slog JSON line %q: %v", line, err)
		}
		records = append(records, rec)
	}
	return records
}

func panicTestDraft() order.Draft {
	return order.Draft{
		KioskID:     "kiosk-1",
		ComboID:     "combo-1",
		ClientKey:   "client-key-1",
		AmountCents: 2000,
		Currency:    "ARS",
		Items: []order.Item{
			{
				SKU:         "BATIDO-1",
				AmountCents: 1200,
				Machine: machine.Ref{
					Kind:         machine.Batidos,
					DeviceID:     "bat-dev-1",
					DeviceNo:     "1",
					DeviceTypeID: "1",
				},
			},
			{
				SKU:         "SNACK-1",
				AmountCents: 800,
				Machine:     machine.Ref{Kind: machine.Snacks, MID: "snk-dev-1"},
			},
		},
	}
}

func panicSeqIDs() func() string {
	var mu sync.Mutex
	n := 0
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		n++
		return fmt.Sprintf("id-%d", n)
	}
}

func panicFindLine(t *testing.T, o store.Order, kind machine.Kind) store.Line {
	t.Helper()
	for _, l := range o.Lines {
		if l.Machine.Kind == kind {
			return l
		}
	}
	t.Fatalf("missing line for machine kind %q", kind)
	return store.Line{}
}

func TestEngineMarkPaidRecoversDispenserPanic(t *testing.T) {
	baseTime := time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC)

	cases := []struct {
		name            string
		batidos         *panicDispenser
		snacks          *panicDispenser
		wantOrderState  store.OrderStatus
		wantSnackState  linestate.State
		wantInitialKind []notify.Kind
		wantLogCount    int
	}{
		{
			name:            "one line panics while sibling is accepted",
			batidos:         &panicDispenser{panicVal: "batidos boom", reconcile: dispenser.Status{State: linestate.Delivered}},
			snacks:          &panicDispenser{result: dispenser.DispatchResult{Accepted: true, ProviderRef: "snk-ref-1"}},
			wantOrderState:  store.OrderDispatching,
			wantSnackState:  linestate.Sent,
			wantInitialKind: []notify.Kind{notify.KindDispatchPanic},
			wantLogCount:    1,
		},
		{
			name:            "both lines panic",
			batidos:         &panicDispenser{panicVal: "batidos boom"},
			snacks:          &panicDispenser{panicVal: "snacks boom"},
			wantOrderState:  store.OrderInDoubt,
			wantSnackState:  linestate.InDoubt,
			wantInitialKind: []notify.Kind{notify.KindOrderInDoubt, notify.KindDispatchPanic},
			wantLogCount:    2,
		},
		{
			name:            "one line panics while sibling is rejected",
			batidos:         &panicDispenser{panicVal: "batidos boom"},
			snacks:          &panicDispenser{err: &dispenser.Rejection{Provider: "snacks", HTTPStatus: 400, Code: 40005, Message: "out of stock"}},
			wantOrderState:  store.OrderInDoubt,
			wantSnackState:  linestate.Failed,
			wantInitialKind: []notify.Kind{notify.KindOrderInDoubt, notify.KindDispatchPanic},
			wantLogCount:    1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logBuf := panicCaptureDefaultSlog(t)
			now := baseTime
			st := store.NewMemory()
			notifier := &panicFakeNotifier{}

			engine := order.New(
				st,
				map[machine.Kind]dispenser.Dispenser{
					machine.Batidos: tc.batidos,
					machine.Snacks:  tc.snacks,
				},
				order.WithClock(func() time.Time { return now }),
				order.WithIDGenerator(panicSeqIDs()),
				order.WithNotifier(notifier),
			)

			placed, err := engine.CreateOrder(context.Background(), panicTestDraft())
			if err != nil {
				t.Fatalf("CreateOrder: %v", err)
			}

			markErr := engine.MarkPaid(context.Background(), placed.ID, now)
			if markErr == nil {
				t.Fatal("expected MarkPaid to return recovered panic error")
			}

			loaded, err := st.LoadOrder(context.Background(), placed.ID)
			if err != nil {
				t.Fatalf("LoadOrder: %v", err)
			}
			if loaded.Status != tc.wantOrderState {
				t.Fatalf("order status = %q, want %q", loaded.Status, tc.wantOrderState)
			}

			batLine := panicFindLine(t, loaded, machine.Batidos)
			if batLine.State != linestate.InDoubt {
				t.Fatalf("batidos line state = %q, want %q", batLine.State, linestate.InDoubt)
			}
			if batLine.DeadlineAt.IsZero() {
				t.Fatal("batidos in_doubt line missing DeadlineAt for reconciliation")
			}
			if !batLine.ReconcileUntil.After(batLine.DeadlineAt) {
				t.Fatalf("batidos ReconcileUntil (%v) must be after DeadlineAt (%v)", batLine.ReconcileUntil, batLine.DeadlineAt)
			}

			snkLine := panicFindLine(t, loaded, machine.Snacks)
			if snkLine.State != tc.wantSnackState {
				t.Fatalf("snacks line state = %q, want %q", snkLine.State, tc.wantSnackState)
			}

			events := notifier.snapshot()
			if len(events) != len(tc.wantInitialKind) {
				t.Fatalf("got %d notifications (%+v), want %d (%v)", len(events), events, len(tc.wantInitialKind), tc.wantInitialKind)
			}
			var panicEvent *notify.Event
			for i := range events {
				if events[i].OrderID != placed.ID {
					t.Fatalf("notification[%d] order_id = %q, want %q", i, events[i].OrderID, placed.ID)
				}
				if events[i].Kind != tc.wantInitialKind[i] {
					t.Fatalf("notification[%d] kind = %q, want %q", i, events[i].Kind, tc.wantInitialKind[i])
				}
				if events[i].Kind == notify.KindDispatchPanic {
					panicEvent = &events[i]
				}
			}
			if panicEvent == nil {
				t.Fatal("expected KindDispatchPanic notification")
			}
			if panicEvent.LineID != batLine.ID {
				t.Fatalf("panic notification line_id = %q, want %q", panicEvent.LineID, batLine.ID)
			}
			if !strings.Contains(panicEvent.Detail, "batidos/BATIDO-1") {
				t.Fatalf("panic notification detail %q missing affected product batidos/BATIDO-1", panicEvent.Detail)
			}
			if tc.name == "both lines panic" && !strings.Contains(panicEvent.Detail, "snacks/SNACK-1") {
				t.Fatalf("panic notification detail %q missing second affected product snacks/SNACK-1", panicEvent.Detail)
			}

			records := panicParseLogRecords(t, logBuf.String())
			panicLogs := 0
			for _, rec := range records {
				if rec["panic"] != nil && rec["stack"] != nil {
					panicLogs++
				}
			}
			if panicLogs != tc.wantLogCount {
				t.Fatalf("got %d structured panic logs, want %d; raw logs:\n%s", panicLogs, tc.wantLogCount, logBuf.String())
			}

			foundBatidosLog := false
			for _, rec := range records {
				if rec["panic"] == "batidos boom" &&
					rec["order_id"] == placed.ID &&
					rec["line_id"] == batLine.ID &&
					strings.Contains(fmt.Sprint(rec["stack"]), "goroutine") {
					foundBatidosLog = true
				}
			}
			if !foundBatidosLog {
				t.Fatalf("missing structured panic log with order_id=%q line_id=%q panic and stack; raw logs:\n%s", placed.ID, batLine.ID, logBuf.String())
			}

			// Verify the panicked line enters reconciliation and that closing the sibling
			// as delivered while the panicked line remains in_doubt still delivers KindOrderInDoubt
			// with the same OrderID, without duplicating KindDispatchPanic.
			if tc.name == "one line panics while sibling is accepted" {
				if _, err := engine.ApplyCallback(context.Background(), order.Callback{
					Source:      "snacks",
					DedupeKey:   "snk-delivered-1",
					OrderID:     placed.ID,
					Ref:         "snk-ref-1",
					MachineType: machine.Snacks,
					State:       order.CallbackDelivered,
				}); err != nil {
					t.Fatalf("ApplyCallback snacks delivered: %v", err)
				}

				afterSibling, err := st.LoadOrder(context.Background(), placed.ID)
				if err != nil {
					t.Fatalf("LoadOrder after sibling callback: %v", err)
				}
				if afterSibling.Status != store.OrderInDoubt {
					t.Fatalf("order status after sibling delivered = %q, want %q", afterSibling.Status, store.OrderInDoubt)
				}

				allEvents := notifier.snapshot()
				if len(allEvents) != 2 {
					t.Fatalf("got %d notifications after order became in_doubt (%+v), want 2", len(allEvents), allEvents)
				}
				if allEvents[0].Kind != notify.KindDispatchPanic || allEvents[1].Kind != notify.KindOrderInDoubt {
					t.Fatalf("notification kinds = [%q, %q], want [%q, %q]", allEvents[0].Kind, allEvents[1].Kind, notify.KindDispatchPanic, notify.KindOrderInDoubt)
				}
				if allEvents[0].OrderID != placed.ID || allEvents[1].OrderID != placed.ID {
					t.Fatalf("alerts do not share OrderID %q: %+v", placed.ID, allEvents)
				}

				now = batLine.DeadlineAt
				if err := engine.SyncOverdueLines(context.Background(), 10); err != nil {
					t.Fatalf("SyncOverdueLines: %v", err)
				}
				reconciled, err := st.LoadOrder(context.Background(), placed.ID)
				if err != nil {
					t.Fatalf("LoadOrder after reconcile: %v", err)
				}
				if got := panicFindLine(t, reconciled, machine.Batidos).State; got != linestate.Delivered {
					t.Fatalf("reconciled batidos line state = %q, want %q", got, linestate.Delivered)
				}
				if got := len(notifier.snapshot()); got != 2 {
					t.Fatalf("notifications after reconcile = %d, want 2 (no duplicate dispatch_panic)", got)
				}
			}
		})
	}
}

func TestEngineSyncPaymentRecoversBackgroundDispatchPanic(t *testing.T) {
	t.Run("dispenser panic inside background dispatch", func(t *testing.T) {
		logBuf := panicCaptureDefaultSlog(t)
		now := time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC)
		st := store.NewMemory()
		notifier := &panicFakeNotifier{}
		gw := newPanicFakeGateway()

		var bgMu sync.Mutex
		var bgOrderID string
		var bgErr error

		engine := order.New(
			st,
			map[machine.Kind]dispenser.Dispenser{
				machine.Batidos: &panicDispenser{panicVal: "async batidos panic"},
				machine.Snacks:  &panicDispenser{result: dispenser.DispatchResult{Accepted: true}},
			},
			order.WithClock(func() time.Time { return now }),
			order.WithIDGenerator(panicSeqIDs()),
			order.WithNotifier(notifier),
			order.WithQRPayments("mercadopago", gw),
			order.WithBackgroundErrors(func(orderID string, err error) {
				bgMu.Lock()
				defer bgMu.Unlock()
				bgOrderID = orderID
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

		if err := engine.SyncPayment(context.Background(), intent.ProviderID); err != nil {
			t.Fatalf("SyncPayment: %v", err)
		}
		engine.Wait()

		bgMu.Lock()
		gotOrderID, gotErr := bgOrderID, bgErr
		bgMu.Unlock()
		if gotErr == nil || gotOrderID != placed.ID {
			t.Fatalf("backgroundErrors got (%q, %v), want (%q, non-nil error)", gotOrderID, gotErr, placed.ID)
		}

		loaded, err := st.LoadOrder(context.Background(), placed.ID)
		if err != nil {
			t.Fatalf("LoadOrder: %v", err)
		}
		batLine := panicFindLine(t, loaded, machine.Batidos)
		if batLine.State != linestate.InDoubt {
			t.Fatalf("batidos line state = %q, want %q", batLine.State, linestate.InDoubt)
		}

		events := notifier.snapshot()
		if len(events) != 1 || events[0].Kind != notify.KindDispatchPanic || events[0].OrderID != placed.ID {
			t.Fatalf("notifications = %+v, want 1 %q with OrderID %q", events, notify.KindDispatchPanic, placed.ID)
		}

		records := panicParseLogRecords(t, logBuf.String())
		found := false
		for _, rec := range records {
			if rec["panic"] == "async batidos panic" &&
				rec["order_id"] == placed.ID &&
				rec["line_id"] == batLine.ID &&
				strings.Contains(fmt.Sprint(rec["stack"]), "goroutine") {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing structured panic log; raw:\n%s", logBuf.String())
		}
	})

	t.Run("panic inside spawn outside line goroutine", func(t *testing.T) {
		logBuf := panicCaptureDefaultSlog(t)
		now := time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC)
		st := &panicOnLoadOrderStore{Memory: store.NewMemory()}
		gw := newPanicFakeGateway()

		var bgMu sync.Mutex
		var bgOrderID string
		var bgErr error

		engine := order.New(
			st,
			map[machine.Kind]dispenser.Dispenser{
				machine.Batidos: &panicDispenser{result: dispenser.DispatchResult{Accepted: true}},
				machine.Snacks:  &panicDispenser{result: dispenser.DispatchResult{Accepted: true}},
			},
			order.WithClock(func() time.Time { return now }),
			order.WithIDGenerator(panicSeqIDs()),
			order.WithQRPayments("mercadopago", gw),
			order.WithBackgroundErrors(func(orderID string, err error) {
				bgMu.Lock()
				defer bgMu.Unlock()
				bgOrderID = orderID
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
		// LoadOrder #1 = claimPaid, LoadOrder #2 = settleOrder inside spawn.
		st.arm(2, errors.New("spawn store boom"))

		if err := engine.SyncPayment(context.Background(), intent.ProviderID); err != nil {
			t.Fatalf("SyncPayment: %v", err)
		}
		engine.Wait()

		bgMu.Lock()
		gotOrderID, gotErr := bgOrderID, bgErr
		bgMu.Unlock()
		if gotErr == nil || gotOrderID != placed.ID {
			t.Fatalf("backgroundErrors got (%q, %v), want (%q, non-nil error)", gotOrderID, gotErr, placed.ID)
		}

		records := panicParseLogRecords(t, logBuf.String())
		found := false
		for _, rec := range records {
			if strings.Contains(fmt.Sprint(rec["panic"]), "spawn store boom") &&
				rec["order_id"] == placed.ID &&
				strings.Contains(fmt.Sprint(rec["stack"]), "goroutine") {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing structured panic log for spawn; raw:\n%s", logBuf.String())
		}
	})

	t.Run("uses injected WithLogger for panic logs", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		now := time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC)
		st := store.NewMemory()

		engine := order.New(
			st,
			map[machine.Kind]dispenser.Dispenser{
				machine.Batidos: &panicDispenser{panicVal: "custom logger boom"},
				machine.Snacks:  &panicDispenser{result: dispenser.DispatchResult{Accepted: true}},
			},
			order.WithClock(func() time.Time { return now }),
			order.WithIDGenerator(panicSeqIDs()),
			order.WithLogger(logger),
		)

		placed, err := engine.CreateOrder(context.Background(), panicTestDraft())
		if err != nil {
			t.Fatalf("CreateOrder: %v", err)
		}

		if err := engine.MarkPaid(context.Background(), placed.ID, now); err == nil {
			t.Fatal("expected MarkPaid to return error on panic")
		}

		records := panicParseLogRecords(t, buf.String())
		if len(records) != 1 || records[0]["panic"] != "custom logger boom" {
			t.Fatalf("unexpected injected logger output: %s", buf.String())
		}
	})
}
