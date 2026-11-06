package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"compras-remotas/internal/catalog"
	"compras-remotas/internal/config"
	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/order"
	"compras-remotas/internal/store"
)

var fixedClock = time.Date(2026, 3, 4, 10, 30, 0, 0, time.UTC)

func testCombos() []catalog.Combo {
	return []catalog.Combo{
		{
			ID:         "combo-mixto",
			Name:       "Batido + snack",
			PriceCents: 450000,
			Currency:   "ARS",
			Active:     true,
			Items: []catalog.Item{
				{MachineType: "batidos", SKU: "batido-frutilla"},
				{MachineType: "snacks", SKU: "snack-alfajor"},
			},
		},
		{
			ID:         "combo-inactivo",
			Name:       "Combo retirado",
			PriceCents: 100,
			Currency:   "ARS",
			Active:     false,
			Items: []catalog.Item{
				{MachineType: "snacks", SKU: "snack-viejo"},
			},
		},
		{
			ID:         "combo-con-maquina-ausente",
			Name:       "Combo de otra máquina",
			PriceCents: 200,
			Currency:   "ARS",
			Active:     true,
			Items: []catalog.Item{
				{MachineType: "cafetera", SKU: "cafe-solo"},
			},
		},
	}
}

func testKiosk(id string) config.Kiosk {
	return config.Kiosk{
		ID:   id,
		Name: "Mueble " + id,
		Machines: []config.Machine{
			{
				Type:         config.MachineBatidos,
				Name:         "Batidos Powermix",
				DeviceID:     "batidos-device-" + id,
				DeviceNo:     "batidos-devno-" + id,
				DeviceTypeID: "batidos-type-" + id,
			},
			{
				Type: config.MachineSnacks,
				Name: "Snacks Powermix",
				MID:  "snacks-mid-" + id,
			},
		},
	}
}

type fakeDispenser struct{}

func (fakeDispenser) Check(ctx context.Context, machine dispenser.MachineRef) (dispenser.Availability, error) {
	return dispenser.Availability{Available: true}, nil
}

func (fakeDispenser) Dispatch(ctx context.Context, req dispenser.Request) (dispenser.DispatchResult, error) {
	return dispenser.DispatchResult{Accepted: true, ProviderRef: "ref-" + req.LineID}, nil
}

func (fakeDispenser) Reconcile(ctx context.Context, req dispenser.Request) (dispenser.Status, error) {
	return dispenser.Status{State: dispenser.LineDelivered, ProviderOrderNo: "ref-" + req.LineID}, nil
}

func testMachines() map[string]dispenser.Dispenser {
	return map[string]dispenser.Dispenser{
		"batidos": fakeDispenser{},
		"snacks":  fakeDispenser{},
	}
}

type fakeConfirmer struct {
	orders     *store.Memory
	calls      []string
	err        error
	saveAnyway bool
}

func (f *fakeConfirmer) ConfirmPayment(ctx context.Context, orderID string, at time.Time) error {
	f.calls = append(f.calls, orderID)

	if f.err != nil && !f.saveAnyway {
		return f.err
	}

	loaded, err := f.orders.LoadOrder(ctx, orderID)
	if err != nil {
		return err
	}

	if loaded.Status == store.OrderPendingPayment {
		loaded.Status = store.OrderPaid
		loaded.PaidAt = at

		if err := f.orders.SaveOrder(ctx, loaded); err != nil {
			return err
		}
	}

	return f.err
}

type fixture struct {
	handler   *Handler
	mux       *http.ServeMux
	orders    *store.Memory
	confirmer *fakeConfirmer
}

func newFixture(t *testing.T, env string, kiosks []config.Kiosk, combos []catalog.Combo) *fixture {
	t.Helper()

	comboCatalog, err := catalog.NewMemory(combos)
	if err != nil {
		t.Fatalf("no se pudo construir el catálogo de prueba: %v", err)
	}

	memory := store.NewMemory()

	counter := 0
	engine := order.New(memory, testMachines(), order.WithClock(func() time.Time { return fixedClock }), order.WithIDGenerator(func() string {
		counter++
		return "id-" + itoa(counter)
	}))

	confirmer := &fakeConfirmer{orders: memory}

	handler, err := New(
		Deps{
			Catalog:   comboCatalog,
			Placer:    engine,
			Confirmer: confirmer,
			Orders:    memory,
			Kiosks:    kiosks,
			Env:       env,
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

func itoa(n int) string {
	if n == 0 {
		return "0"
	}

	digits := make([]byte, 0, 8)
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}

	return string(digits)
}

func call(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	request := httptest.NewRequest(method, path, reader)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)

	return recorder
}

func decode(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("se esperaba JSON y llegó %q", contentType)
	}

	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("la respuesta no es un objeto JSON válido (%v): %s", err, recorder.Body.String())
	}

	return payload
}

func object(t *testing.T, parent map[string]any, key string) map[string]any {
	t.Helper()

	value, ok := parent[key].(map[string]any)
	if !ok {
		t.Fatalf("se esperaba un objeto en %q y llegó %#v en %v", key, parent[key], parent)
	}

	return value
}

func array(t *testing.T, parent map[string]any, key string) []any {
	t.Helper()

	value, ok := parent[key].([]any)
	if !ok {
		t.Fatalf("se esperaba una lista en %q y llegó %#v en %v", key, parent[key], parent)
	}

	return value
}

type fakeReceiver struct {
	closed    []order.Callback
	confirmed []order.Callback

	closeErr   error
	confirmErr error
}

func (f *fakeReceiver) CloseFromCallback(ctx context.Context, cb order.Callback) (bool, error) {
	f.closed = append(f.closed, cb)

	return false, f.closeErr
}

func (f *fakeReceiver) ConfirmFromCallback(ctx context.Context, cb order.Callback) (bool, error) {
	f.confirmed = append(f.confirmed, cb)

	return false, f.confirmErr
}

func newCallbackFixture(t *testing.T, secrets CallbackSecrets) (*http.ServeMux, *fakeReceiver) {
	t.Helper()

	receiver := &fakeReceiver{}
	callbacks := NewCallbacks(receiver, secrets, WithCallbacksLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))

	mux := http.NewServeMux()
	callbacks.Register(mux)

	return mux, receiver
}

type silentRefDispenser struct{}

func (silentRefDispenser) Check(ctx context.Context, machine dispenser.MachineRef) (dispenser.Availability, error) {
	return dispenser.Availability{Available: true}, nil
}

func (silentRefDispenser) Dispatch(ctx context.Context, req dispenser.Request) (dispenser.DispatchResult, error) {
	return dispenser.DispatchResult{Accepted: true, Detail: "despacho encolado: el resultado real llega por callback"}, nil
}

func (silentRefDispenser) Reconcile(ctx context.Context, req dispenser.Request) (dispenser.Status, error) {
	return dispenser.Status{State: dispenser.LineInDoubt}, nil
}

type liveCallbackFixture struct {
	mux    *http.ServeMux
	orders *store.Memory
}

func newLiveCallbackFixture(t *testing.T, combos []catalog.Combo, machines map[string]dispenser.Dispenser) *liveCallbackFixture {
	t.Helper()

	comboCatalog, err := catalog.NewMemory(combos)
	if err != nil {
		t.Fatalf("no se pudo construir el catálogo: %v", err)
	}

	memory := store.NewMemory()
	engine := order.New(memory, machines, order.WithClock(func() time.Time { return fixedClock }))

	handler, err := New(
		Deps{
			Catalog:   comboCatalog,
			Placer:    engine,
			Confirmer: engine,
			Orders:    memory,
			Kiosks:    []config.Kiosk{testKiosk("kiosco-centro")},
			Env:       config.EnvSandbox,
		},
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
	if err != nil {
		t.Fatalf("no se pudo construir el handler: %v", err)
	}

	mux := http.NewServeMux()
	handler.Register(mux)

	NewCallbacks(
		engine,
		CallbackSecrets{Snacks: "ruta-secreta"},
		WithCallbacksLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	).Register(mux)

	return &liveCallbackFixture{mux: mux, orders: memory}
}

func twoSnackItemsCombos() []catalog.Combo {
	return []catalog.Combo{
		{
			ID:         "combo-doble-snack",
			Name:       "Dos snacks",
			PriceCents: 300,
			Currency:   "ARS",
			Active:     true,
			Items: []catalog.Item{
				{MachineType: "snacks", SKU: "snack-uno"},
				{MachineType: "snacks", SKU: "snack-dos"},
			},
		},
	}
}

func paidOrder(t *testing.T, fx *liveCallbackFixture, comboID string) store.Order {
	t.Helper()

	created := call(t, fx.mux, http.MethodPost, "/orders", `{"combo_id":"`+comboID+`"}`)
	if created.Code != http.StatusOK {
		t.Fatalf("no se pudo crear la orden: %d %s", created.Code, created.Body.String())
	}

	orderID := decode(t, created)["id"].(string)

	if paid := call(t, fx.mux, http.MethodPost, "/orders/"+orderID+"/pay", ""); paid.Code != http.StatusOK {
		t.Fatalf("no se pudo pagar la orden: %d %s", paid.Code, paid.Body.String())
	}

	loaded, err := fx.orders.LoadOrder(context.Background(), orderID)
	if err != nil {
		t.Fatalf("la orden no se pudo releer: %v", err)
	}

	return loaded
}

func newLoggingCallbackFixture(t *testing.T, secret string, logged *bytes.Buffer) (*http.ServeMux, *fakeReceiver) {
	t.Helper()

	receiver := &fakeReceiver{}
	callbacks := NewCallbacks(receiver, CallbackSecrets{Batidos: secret}, WithCallbacksLogger(slog.New(slog.NewTextHandler(logged, nil))))

	mux := http.NewServeMux()
	callbacks.Register(mux)

	return mux, receiver
}
