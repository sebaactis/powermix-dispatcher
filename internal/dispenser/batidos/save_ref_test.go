package batidos_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/dispenser/batidos"
	"compras-remotas/internal/linestate"
)

func newSaveRefServer(t *testing.T, events *[]string, mu *sync.Mutex, brews *atomic.Int32) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc(campaignPath, func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(w, `{"exchangeCodes":["000777"],"activeInfo":{"activeNo":"AD000777","activeName":"combo","exchangeType":1}}`)
	})
	mux.HandleFunc(produceCoffeePath, func(w http.ResponseWriter, _ *http.Request) {
		brews.Add(1)
		mu.Lock()
		*events = append(*events, "produceCoffee")
		mu.Unlock()
		writeEnvelope(w, `true`)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv
}

func TestDispenserDispatchSavesTheCodeBeforeRedeemingIt(t *testing.T) {
	var (
		mu     sync.Mutex
		events []string
		brews  atomic.Int32
	)
	srv := newSaveRefServer(t, &events, &mu, &brews)
	d := batidos.NewDispenser(batidos.NewClient(srv.URL, testKey, testSecret))

	req := newRequest()
	req.SaveRef = func(_ context.Context, ref string) error {
		mu.Lock()
		events = append(events, "saveRef:"+ref)
		mu.Unlock()
		return nil
	}

	attempt, err := d.Dispatch(context.Background(), req)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if !attempt.Accepted || attempt.ProviderRef != "000777" {
		t.Fatalf("attempt = %+v, se esperaba aceptado con el código 000777", attempt)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 || events[0] != "saveRef:000777" || events[1] != "produceCoffee" {
		t.Fatalf("eventos = %v, se esperaba guardar el código antes de canjearlo", events)
	}
}

func TestDispenserDispatchDoesNotRedeemACodeThatCouldNotBeSaved(t *testing.T) {
	var (
		mu     sync.Mutex
		events []string
		brews  atomic.Int32
	)
	srv := newSaveRefServer(t, &events, &mu, &brews)
	d := batidos.NewDispenser(batidos.NewClient(srv.URL, testKey, testSecret))

	storeDown := errors.New("la base no responde")
	req := newRequest()
	req.SaveRef = func(context.Context, string) error { return storeDown }

	attempt, err := d.Dispatch(context.Background(), req)
	if err == nil {
		t.Fatalf("se esperaba un error, attempt = %+v", attempt)
	}
	if !errors.Is(err, dispenser.ErrNotDispatched) {
		t.Fatalf("err = %v, se esperaba dispenser.ErrNotDispatched", err)
	}
	if !errors.Is(err, storeDown) {
		t.Fatalf("err = %v, se esperaba que conserve la causa", err)
	}
	if got := brews.Load(); got != 0 {
		t.Fatalf("se canjeó %d veces un código que no quedó guardado", got)
	}

	var zero dispenser.DispatchResult
	if attempt != zero {
		t.Fatalf("no debe devolver un intento con código que no se guardó: %+v", attempt)
	}
}

func TestDispenserReconcileWithoutCodeFailsWithoutAskingTheProvider(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeEnvelope(w, `{"orderNo":"A1","status":2}`)
	}))
	defer srv.Close()

	d := batidos.NewDispenser(batidos.NewClient(srv.URL, testKey, testSecret))

	status, err := d.Reconcile(context.Background(), requestWithRef(""))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if status.State != linestate.Failed {
		t.Fatalf("State = %q, se esperaba %q", status.State, linestate.Failed)
	}
	if status.Reason == "" {
		t.Fatalf("se esperaba un motivo legible para soporte")
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("se consultó al proveedor %d veces con código vacío", got)
	}
}
