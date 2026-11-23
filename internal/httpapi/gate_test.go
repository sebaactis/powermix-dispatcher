package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"compras-remotas/internal/catalog"
	"compras-remotas/internal/config"
	"compras-remotas/internal/dispenser/batidos"
	"compras-remotas/internal/dispenser/snacks"
	"compras-remotas/internal/gate"
	"compras-remotas/internal/machine"
	"compras-remotas/internal/order"
	"compras-remotas/internal/preflight"
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

func newGatedFixtureWithLogger(t *testing.T, g order.AvailabilityChecker, logger *slog.Logger) *fixture {
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
		order.WithAvailabilityChecker(g),
	)

	confirmer := &fakeConfirmer{orders: memory}

	handler, err := New(
		Deps{
			Catalog:      comboCatalog,
			OrderCreator: engine,
			PaidMarker:   confirmer,
			Orders:       memory,
			Kiosks:       []config.Kiosk{testKiosk("kiosk-1")},
			Env:          config.EnvSandbox,
		},
		WithClock(func() time.Time { return fixedClock }),
		WithLogger(logger),
	)
	if err != nil {
		t.Fatalf("no se pudo construir el handler: %v", err)
	}

	mux := http.NewServeMux()
	handler.Register(mux)

	return &fixture{handler: handler, mux: mux, orders: memory, confirmer: confirmer}
}

func newGatedFixture(t *testing.T, g order.AvailabilityChecker) *fixture {
	t.Helper()
	return newGatedFixtureWithLogger(t, g, slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil)))
}

type fakeBatidosPreflight struct {
	device   batidos.Device
	goods    []batidos.DeviceGoods
	err      error
	panicVal any
	block    bool
}

func (f *fakeBatidosPreflight) Device(ctx context.Context, deviceID string) (batidos.Device, error) {
	if f.panicVal != nil {
		panic(f.panicVal)
	}
	if f.block {
		<-ctx.Done()
		return batidos.Device{}, ctx.Err()
	}
	if f.err != nil {
		return batidos.Device{}, f.err
	}
	return f.device, nil
}

func (f *fakeBatidosPreflight) DeviceGoods(ctx context.Context, deviceID string) ([]batidos.DeviceGoods, error) {
	if f.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.goods, nil
}

type fakeSnacksPreflight struct {
	page  snacks.DevicesPage
	slots []snacks.Slot
	err   error
	block bool
}

func (f *fakeSnacksPreflight) Devices(ctx context.Context, query snacks.PageQuery) (snacks.DevicesPage, error) {
	if f.block {
		<-ctx.Done()
		return snacks.DevicesPage{}, ctx.Err()
	}
	if f.err != nil {
		return snacks.DevicesPage{}, f.err
	}
	return f.page, nil
}

func (f *fakeSnacksPreflight) Slots(ctx context.Context, deviceCode string) ([]snacks.Slot, error) {
	if f.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.slots, nil
}

func healthyBatidosFake() *fakeBatidosPreflight {
	return &fakeBatidosPreflight{
		device: batidos.Device{
			ID:              "batidos-device-kiosk-1",
			Status:          batidos.DeviceStatusOnline,
			DeviceRunStatus: batidos.DeviceRunStatusNormal,
			ScarceStatus:    batidos.ScarceStatusNormal,
		},
		goods: []batidos.DeviceGoods{{ID: "batido-frutilla", Listing: true, SoldOut: 0}},
	}
}

func healthySnacksFake() *fakeSnacksPreflight {
	return &fakeSnacksPreflight{
		page: snacks.DevicesPage{
			Items: []snacks.Device{{
				DeviceCode:    "snacks-mid-kiosk-1",
				OnlineStatus:  snacks.OnlineStatusOnline,
				SellStatus:    snacks.SellStatusNormal,
				FaultState:    snacks.FaultStateNormal,
				LockType:      snacks.LockTypeNormal,
				StockoutState: snacks.StockoutStateNormal,
			}},
		},
		slots: []snacks.Slot{{ProductID: "snack-alfajor", Stock: 5, SlotStatus: snacks.SlotStatusNormal}},
	}
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

func TestCreateOrderReturnsBadGatewayWhenPreflightProviderFailsOrTimesOut(t *testing.T) {
	cases := []struct {
		name    string
		batidos func() *fakeBatidosPreflight
		snacks  func() *fakeSnacksPreflight
		opts    []preflight.Option
	}{
		{
			name: "error del proveedor de batidos",
			batidos: func() *fakeBatidosPreflight {
				b := healthyBatidosFake()
				b.err = errors.New("batidos caído")
				return b
			},
			snacks: healthySnacksFake,
		},
		{
			name:    "error del proveedor de snacks",
			batidos: healthyBatidosFake,
			snacks: func() *fakeSnacksPreflight {
				s := healthySnacksFake()
				s.err = errors.New("snacks caído")
				return s
			},
		},
		{
			name: "timeout del presupuesto de preflight",
			batidos: func() *fakeBatidosPreflight {
				return &fakeBatidosPreflight{block: true}
			},
			snacks: healthySnacksFake,
			opts:   []preflight.Option{preflight.WithBudget(20 * time.Millisecond)},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logBuf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelError}))
			checker := preflight.New(tc.batidos(), tc.snacks(), append(tc.opts, preflight.WithLogger(logger))...)
			env := newGatedFixtureWithLogger(t, gate.New(checker), logger)

			recorder := call(t, env.mux, http.MethodPost, "/orders", `{"combo_id":"combo-mixto"}`)

			if recorder.Code != http.StatusBadGateway {
				t.Fatalf("codigo=%d, se esperaba %d: %s", recorder.Code, http.StatusBadGateway, recorder.Body.String())
			}

			const wantBody = `{"error":"no se pudo verificar la disponibilidad de las máquinas"}`
			if strings.TrimSpace(recorder.Body.String()) != wantBody {
				t.Fatalf("body=%q, se esperaba %q", strings.TrimSpace(recorder.Body.String()), wantBody)
			}

			if _, err := env.orders.LoadOrder(context.Background(), "id-1"); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("en un 502 no se debe crear ninguna orden, LoadOrder(id-1)=%v", err)
			}

			var rec map[string]any
			if err := json.Unmarshal([]byte(strings.Split(strings.TrimSpace(logBuf.String()), "\n")[0]), &rec); err != nil {
				t.Fatalf("se esperaba log JSON de error, llegó %q (%v)", logBuf.String(), err)
			}
			if rec["level"] != "ERROR" {
				t.Fatalf("nivel de log=%v, se esperaba ERROR", rec["level"])
			}
		})
	}
}

func TestCreateOrderReturnsInternalServerErrorForPreflightPanicAndGateInternalErrors(t *testing.T) {
	t.Run("panic dentro de preflight", func(t *testing.T) {
		var logBuf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelError}))
		b := healthyBatidosFake()
		b.panicVal = "boom"
		checker := preflight.New(b, healthySnacksFake(), preflight.WithLogger(logger))
		env := newGatedFixtureWithLogger(t, gate.New(checker), logger)

		recorder := call(t, env.mux, http.MethodPost, "/orders", `{"combo_id":"combo-mixto"}`)

		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("codigo=%d, se esperaba %d: %s", recorder.Code, http.StatusInternalServerError, recorder.Body.String())
		}

		const wantBody = `{"error":"no se pudo completar la operación"}`
		if strings.TrimSpace(recorder.Body.String()) != wantBody {
			t.Fatalf("body=%q, se esperaba %q", strings.TrimSpace(recorder.Body.String()), wantBody)
		}

		if _, err := env.orders.LoadOrder(context.Background(), "id-1"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("en un 500 no se debe crear ninguna orden, LoadOrder(id-1)=%v", err)
		}
	})

	t.Run("gate sin verificador configurado", func(t *testing.T) {
		env := newGatedFixture(t, gate.New(nil))

		recorder := call(t, env.mux, http.MethodPost, "/orders", `{"combo_id":"combo-mixto"}`)

		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("codigo=%d, se esperaba %d: %s", recorder.Code, http.StatusInternalServerError, recorder.Body.String())
		}

		const wantBody = `{"error":"no se pudo completar la operación"}`
		if strings.TrimSpace(recorder.Body.String()) != wantBody {
			t.Fatalf("body=%q, se esperaba %q", strings.TrimSpace(recorder.Body.String()), wantBody)
		}
	})

	t.Run("combo no evaluable por gate.request", func(t *testing.T) {
		combos := []catalog.Combo{
			{
				ID:         "combo-solo-snacks",
				Name:       "Solo snacks",
				PriceCents: 150000,
				Currency:   "ARS",
				Active:     true,
				Items:      []catalog.Item{{MachineType: machine.Snacks, SKU: "snack-alfajor", Name: "Producto de prueba", PriceCents: 150000}},
			},
		}
		comboCatalog, err := catalog.NewMemory(combos)
		if err != nil {
			t.Fatalf("NewMemory: %v", err)
		}
		memory := store.NewMemory()
		checker := preflight.New(healthyBatidosFake(), healthySnacksFake())
		engine := order.New(memory, testMachines(), order.WithAvailabilityChecker(gate.New(checker)))
		handler, err := New(Deps{
			Catalog:      comboCatalog,
			OrderCreator: engine,
			PaidMarker:   &fakeConfirmer{orders: memory},
			Orders:       memory,
			Kiosks:       []config.Kiosk{testKiosk("kiosk-1")},
			Env:          config.EnvSandbox,
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		mux := http.NewServeMux()
		handler.Register(mux)

		recorder := call(t, mux, http.MethodPost, "/orders", `{"combo_id":"combo-solo-snacks"}`)

		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("codigo=%d, se esperaba %d: %s", recorder.Code, http.StatusInternalServerError, recorder.Body.String())
		}

		const wantBody = `{"error":"no se pudo completar la operación"}`
		if strings.TrimSpace(recorder.Body.String()) != wantBody {
			t.Fatalf("body=%q, se esperaba %q", strings.TrimSpace(recorder.Body.String()), wantBody)
		}
	})
}
