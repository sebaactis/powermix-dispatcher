package order_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/dispenser/batidos"
	"compras-remotas/internal/linestate"
	"compras-remotas/internal/machine"
	"compras-remotas/internal/order"
	"compras-remotas/internal/store"
)

// crashingMachine guarda el código con SaveRef y después se queda colgada,
// como si el proceso se hubiera cortado durante el canje.
type crashingMachine struct {
	saved   chan struct{}
	release chan struct{}
}

func (m *crashingMachine) Check(context.Context, machine.Ref) (dispenser.Availability, error) {
	return dispenser.Availability{Available: true}, nil
}

func (m *crashingMachine) Dispatch(ctx context.Context, req dispenser.Request) (dispenser.DispatchResult, error) {
	if err := req.SaveRef(ctx, "code-crash"); err != nil {
		return dispenser.DispatchResult{}, err
	}
	close(m.saved)
	<-m.release

	return dispenser.DispatchResult{Accepted: true, ProviderRef: "code-crash"}, nil
}

func (m *crashingMachine) Reconcile(context.Context, dispenser.Request) (dispenser.Status, error) {
	return dispenser.Status{}, errors.New("no debería reconciliar")
}

func TestEngineRecoversTheCodeOfADispatchCutAfterRedeeming(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	clock := manualClockAt(engineNow)

	crashing := &crashingMachine{saved: make(chan struct{}), release: make(chan struct{})}
	first := order.New(
		st,
		map[machine.Kind]dispenser.Dispenser{"batidos": crashing},
		order.WithClock(clock.clock()),
		order.WithIDGenerator(sequentialIDs()),
	)

	placed := place(t, first, drinkOnlyDraft())

	done := make(chan error, 1)
	go func() { done <- first.MarkPaid(ctx, placed.ID, engineNow) }()

	select {
	case <-crashing.saved:
	case <-time.After(5 * time.Second):
		t.Fatal("la máquina nunca llamó a SaveRef")
	}

	line := lineFor(t, mustOrder(t, st, placed.ID), "batidos")
	if line.Ref != "code-crash" {
		t.Fatalf("el código no quedó guardado antes del canje: Ref=%q", line.Ref)
	}

	// Otro proceso (el que arranca después del corte) reconcilia con el código guardado.
	survivor := &reconcileMachine{status: dispenser.Status{State: linestate.Delivered}}
	second := order.New(
		st,
		map[machine.Kind]dispenser.Dispenser{"batidos": survivor},
		order.WithClock(clock.clock()),
		order.WithIDGenerator(sequentialIDs()),
	)

	clock.set(engineNow.Add(3 * time.Minute))
	if err := second.SyncOverdueLines(ctx, 10); err != nil {
		t.Fatalf("SyncOverdueLines: %v", err)
	}

	if ref := survivor.onlyReconcile(t).KnownRef; ref != "code-crash" {
		t.Fatalf("la reconciliación consultó %q, se esperaba el código guardado antes del corte", ref)
	}

	recovered := lineFor(t, mustOrder(t, st, placed.ID), "batidos")
	if recovered.State != linestate.Delivered {
		t.Fatalf("la línea quedó en %q, se esperaba %q", recovered.State, linestate.Delivered)
	}

	close(crashing.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("el despacho colgado no terminó")
	}

	if final := lineFor(t, mustOrder(t, st, placed.ID), "batidos"); final.State != linestate.Delivered {
		t.Fatalf("el despacho viejo pisó la línea reconciliada: %q", final.State)
	}
}

func TestEngineRetriesADispatchThatNeverReachedTheMachine(t *testing.T) {
	ctx := context.Background()
	st := newRecordingStore()

	driver := &scriptedMachine{steps: []machineStep{
		{err: fmt.Errorf("batidos: %w: la base no responde", dispenser.ErrNotDispatched)},
		{result: dispenser.DispatchResult{Accepted: true, ProviderRef: "code-2"}},
	}}

	engine := order.New(
		st,
		map[machine.Kind]dispenser.Dispenser{"batidos": driver},
		order.WithClock(frozenClock()),
		order.WithIDGenerator(sequentialIDs()),
	)

	placed := place(t, engine, drinkOnlyDraft())
	if err := engine.MarkPaid(ctx, placed.ID, engineNow); err != nil {
		t.Fatalf("MarkPaid: %v", err)
	}

	if calls := driver.callCount(); calls != 2 {
		t.Fatalf("la máquina recibió %d despachos, se esperaba el intento no enviado y su reintento", calls)
	}

	line := lineFor(t, mustOrder(t, st, placed.ID), "batidos")
	if line.State != linestate.Sent || line.Ref != "code-2" {
		t.Fatalf("línea = %+v, se esperaba Sent con code-2", line)
	}

	st.mu.Lock()
	closed := make([]store.Attempt, 0, 2)
	for _, attempt := range st.saved {
		if attempt.LineID == line.ID {
			closed = append(closed, attempt)
		}
	}
	st.mu.Unlock()

	if len(closed) != 2 {
		t.Fatalf("se cerraron %d intentos, se esperaban 2", len(closed))
	}
	if closed[0].Outcome != store.OutcomeRejected {
		t.Fatalf("el intento no enviado quedó %q, se esperaba %q", closed[0].Outcome, store.OutcomeRejected)
	}
	if closed[1].Outcome != store.OutcomeAccepted {
		t.Fatalf("el reintento quedó %q, se esperaba %q", closed[1].Outcome, store.OutcomeAccepted)
	}
}

// refRefusingStore rechaza guardar líneas con código, como si la base se cayera
// justo entre la emisión de la campaña y el canje.
type refRefusingStore struct {
	*store.Memory
}

func (s *refRefusingStore) SaveLine(ctx context.Context, line store.Line) (store.Line, error) {
	if line.Ref != "" {
		return store.Line{}, errors.New("la base no responde")
	}
	return s.Memory.SaveLine(ctx, line)
}

func TestEngineNeverRedeemsABatidosCodeThatCouldNotBeSaved(t *testing.T) {
	var campaigns, brews atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/coffee/newThird/active/v2/submit", func(w http.ResponseWriter, _ *http.Request) {
		n := campaigns.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"code":200,"success":true,"msg":"ok","data":{"exchangeCodes":["%06d"],"activeInfo":{"activeNo":"AD%06d","activeName":"combo","exchangeType":1}}}`, n, n)
	})
	mux.HandleFunc("/coffee/newThird/order/produceCoffee", func(w http.ResponseWriter, _ *http.Request) {
		brews.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"success":true,"msg":"ok","data":true}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx := context.Background()
	st := &refRefusingStore{Memory: store.NewMemory()}
	engine := order.New(
		st,
		map[machine.Kind]dispenser.Dispenser{"batidos": batidos.NewDispenser(batidos.NewClient(srv.URL, "k", "s"))},
		order.WithClock(frozenClock()),
		order.WithIDGenerator(sequentialIDs()),
	)

	placed := place(t, engine, drinkOnlyDraft())
	_ = engine.MarkPaid(ctx, placed.ID, engineNow)

	if got := brews.Load(); got != 0 {
		t.Fatalf("se canjearon %d códigos que no quedaron guardados", got)
	}
	if got := campaigns.Load(); got != 2 {
		t.Fatalf("se emitieron %d campañas, se esperaban 2 (intento y reintento)", got)
	}

	line := lineFor(t, mustOrder(t, st, placed.ID), "batidos")
	if line.State != linestate.Failed {
		t.Fatalf("la línea quedó en %q, se esperaba %q", line.State, linestate.Failed)
	}
	if !strings.Contains(line.LastError, "no llegó a activarse") {
		t.Fatalf("LastError = %q, se esperaba que explique que la máquina no se activó", line.LastError)
	}
}

func TestEngineFailsABatidosLineWithoutCodeWhenReconciling(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/coffee/newThird/active/v2/submit", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":500,"success":false,"msg":"error interno","data":null}`))
	})
	var lookups atomic.Int32
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		lookups.Add(1)
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx := context.Background()
	st := store.NewMemory()
	clock := manualClockAt(engineNow)
	engine := order.New(
		st,
		map[machine.Kind]dispenser.Dispenser{"batidos": batidos.NewDispenser(batidos.NewClient(srv.URL, "k", "s"))},
		order.WithClock(clock.clock()),
		order.WithIDGenerator(sequentialIDs()),
	)

	placed := place(t, engine, drinkOnlyDraft())
	if err := engine.MarkPaid(ctx, placed.ID, engineNow); err != nil {
		t.Fatalf("MarkPaid: %v", err)
	}

	line := lineFor(t, mustOrder(t, st, placed.ID), "batidos")
	if line.State != linestate.InDoubt || line.Ref != "" {
		t.Fatalf("línea = %+v, se esperaba in_doubt sin código tras el 500 de la campaña", line)
	}

	clock.set(engineNow.Add(time.Minute))
	if err := engine.SyncOverdueLines(ctx, 10); err != nil {
		t.Fatalf("SyncOverdueLines: %v", err)
	}

	line = lineFor(t, mustOrder(t, st, placed.ID), "batidos")
	if line.State != linestate.Failed {
		t.Fatalf("la línea sin código quedó en %q, se esperaba %q", line.State, linestate.Failed)
	}
	if got := lookups.Load(); got != 0 {
		t.Fatalf("se consultó al proveedor %d veces con código vacío", got)
	}
}
