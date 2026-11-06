package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"compras-remotas/internal/catalog"
	"compras-remotas/internal/config"
	"compras-remotas/internal/order"
	"compras-remotas/internal/store"
)

type stubGate struct {
	err   error
	calls int
}

func (g *stubGate) Check(ctx context.Context, draft order.Draft) error {
	g.calls++

	return g.err
}

func newGatedFixture(t *testing.T, g order.Gate) *fixture {
	t.Helper()

	comboCatalog, err := catalog.NewMemory(testCombos())
	if err != nil {
		t.Fatalf("no se pudo construir el catálogo de prueba: %v", err)
	}

	memory := store.NewMemory()

	counter := 0
	engine := order.New(
		memory,
		testMachines(),
		order.WithClock(func() time.Time { return fixedClock }),
		order.WithIDGenerator(func() string {
			counter++
			return "id-" + itoa(counter)
		}),
		order.WithGate(g),
	)

	confirmer := &fakeConfirmer{orders: memory}

	handler, err := New(
		Deps{
			Catalog:   comboCatalog,
			Placer:    engine,
			Confirmer: confirmer,
			Orders:    memory,
			Kiosks:    []config.Kiosk{testKiosk("kiosk-1")},
			Env:       config.EnvSandbox,
		},
		WithClock(func() time.Time { return fixedClock }),
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
	if err != nil {
		t.Fatalf("no se pudo construir el handler: %v", err)
	}

	mux := http.NewServeMux()
	handler.Register(mux)

	return &fixture{handler: handler, mux: mux, orders: memory, confirmer: confirmer}
}

func TestCreateOrderReturnsConflictWhenTheComboIsNotSellable(t *testing.T) {
	gate := &stubGate{err: &order.NotSellableError{Reasons: []string{"batidos_device_offline", "snacks_device_out_of_stock"}}}
	env := newGatedFixture(t, gate)

	recorder := call(t, env.mux, http.MethodPost, "/orders", `{"combo_id":"combo-mixto"}`)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("codigo=%d, se esperaba %d: %s", recorder.Code, http.StatusConflict, recorder.Body.String())
	}

	body := decode(t, recorder)

	if message, _ := body["error"].(string); message != "el combo no se puede vender ahora" {
		t.Fatalf("error=%q, se esperaba el mensaje del combo no vendible", message)
	}

	rawReasons, ok := body["reasons"].([]any)
	if !ok {
		t.Fatalf("se esperaba una lista de razones en el cuerpo y llegó %#v", body["reasons"])
	}

	want := []string{"batidos_device_offline", "snacks_device_out_of_stock"}
	if len(rawReasons) != len(want) {
		t.Fatalf("reasons=%v, se esperaba %v", rawReasons, want)
	}
	for i, reason := range want {
		if rawReasons[i] != reason {
			t.Fatalf("reasons=%v, se esperaba %v", rawReasons, want)
		}
	}

	if gate.calls != 1 {
		t.Fatalf("la puerta se consultó %d veces, se esperaba 1", gate.calls)
	}

	if _, err := env.orders.LoadOrder(context.Background(), "id-1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("la orden bloqueada no debía existir y LoadOrder devolvió %v", err)
	}
}

func TestCreateOrderReturnsInternalErrorWhenTheProviderFailsDuringTheCheck(t *testing.T) {
	env := newGatedFixture(t, &stubGate{err: errors.New("el proveedor de batidos no responde")})

	recorder := call(t, env.mux, http.MethodPost, "/orders", `{"combo_id":"combo-mixto"}`)

	if recorder.Code == http.StatusConflict {
		t.Fatalf("un proveedor caído no es un conflicto de disponibilidad: %s", recorder.Body.String())
	}

	if recorder.Code != http.StatusInternalServerError && recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("codigo=%d, se esperaba %d o %d: %s",
			recorder.Code, http.StatusInternalServerError, http.StatusServiceUnavailable, recorder.Body.String())
	}

	body := decode(t, recorder)

	if message, _ := body["error"].(string); message != "no se pudo completar la operación" {
		t.Fatalf("error=%q, se esperaba el mensaje genérico de fallo", message)
	}

	if _, hasReasons := body["reasons"]; hasReasons {
		t.Fatalf("un fallo del proveedor no expone razones de disponibilidad: %v", body)
	}

	if _, err := env.orders.LoadOrder(context.Background(), "id-1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("la orden no debía existir y LoadOrder devolvió %v", err)
	}
}
