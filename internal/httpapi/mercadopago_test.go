package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"compras-remotas/internal/catalog"
	"compras-remotas/internal/config"
	"compras-remotas/internal/logging"
	"compras-remotas/internal/machine"
	"compras-remotas/internal/order"
	"compras-remotas/internal/payments"
	"compras-remotas/internal/payments/mercadopago"
	"compras-remotas/internal/store"
)

var mpClock = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

const (
	mpCallbackSecret = "mp-callback"
	mpWebhookSecret  = "whsec"
	mpProviderID     = "ORD01JQ4S4KY8HWQ6NA5PXB65B3D3"
)

type recordingConfirmer struct {
	calls []string
	err   error
}

func (c *recordingConfirmer) SyncPayment(_ context.Context, providerID string) error {
	c.calls = append(c.calls, providerID)
	return c.err
}

func newMPMux(confirmer PaymentSyncer) *http.ServeMux {
	mux := http.NewServeMux()
	NewMercadoPagoWebhook(confirmer, MercadoPagoSecrets{Callback: mpCallbackSecret, Webhook: mpWebhookSecret},
		WithMercadoPagoClock(func() time.Time { return mpClock }),
	).Register(mux)

	return mux
}

type webhookRequest struct {
	pathSecret string
	dataID     string
	topic      string
	signWith   string
	signedAt   time.Time
}

func defaultWebhook() webhookRequest {
	return webhookRequest{
		pathSecret: mpCallbackSecret,
		dataID:     mpProviderID,
		topic:      "order",
		signWith:   mpWebhookSecret,
		signedAt:   mpClock,
	}
}

func sendWebhook(t *testing.T, mux http.Handler, req webhookRequest) *httptest.ResponseRecorder {
	t.Helper()

	target := fmt.Sprintf("/callbacks/mercadopago/%s?data.id=%s&type=%s", req.pathSecret, req.dataID, req.topic)
	httpReq := httptest.NewRequest(http.MethodPost, target, strings.NewReader(`{"action":"order.processed"}`))

	const requestID = "2066ca19-c6f1-498a-be75-1923005edd06"
	httpReq.Header.Set("X-Request-Id", requestID)
	httpReq.Header.Set("X-Signature", mercadopago.SignatureHeader(req.signWith, requestID, req.dataID, req.signedAt))

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httpReq)

	return recorder
}

func TestMercadoPagoWebhookResponses(t *testing.T) {
	cases := []struct {
		name       string
		mutate     func(*webhookRequest)
		confirmErr error
		wantStatus int
		wantCalls  int
	}{
		{"aviso válido", nil, nil, http.StatusOK, 1},
		{"secreto de ruta equivocado", func(r *webhookRequest) { r.pathSecret = "otro" }, nil, http.StatusNotFound, 0},
		{"sin data.id", func(r *webhookRequest) { r.dataID = "" }, nil, http.StatusBadRequest, 0},
		{"firmado con otro secreto", func(r *webhookRequest) { r.signWith = "otro" }, nil, http.StatusUnauthorized, 0},
		{"firma vieja", func(r *webhookRequest) { r.signedAt = mpClock.Add(-6 * time.Minute) }, nil, http.StatusUnauthorized, 0},
		{"otro tópico", func(r *webhookRequest) { r.topic = "payment" }, nil, http.StatusOK, 0},
		{"cobro ajeno", nil, fmt.Errorf("%w: x", order.ErrUnknownCharge), http.StatusOK, 1},
		{"falla transitoria", nil, errors.New("mercado pago no responde"), http.StatusInternalServerError, 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			confirmer := &recordingConfirmer{err: tc.confirmErr}
			req := defaultWebhook()
			if tc.mutate != nil {
				tc.mutate(&req)
			}

			recorder := sendWebhook(t, newMPMux(confirmer), req)

			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, quería %d (cuerpo %s)", recorder.Code, tc.wantStatus, recorder.Body.String())
			}

			if len(confirmer.calls) != tc.wantCalls {
				t.Fatalf("SyncPayment se llamó %d veces, quería %d", len(confirmer.calls), tc.wantCalls)
			}
		})
	}
}

func TestMercadoPagoWebhookPassesProviderIDUnchanged(t *testing.T) {
	confirmer := &recordingConfirmer{}

	sendWebhook(t, newMPMux(confirmer), defaultWebhook())

	if len(confirmer.calls) != 1 || confirmer.calls[0] != mpProviderID {
		t.Fatalf("llamadas = %v; quería el id tal cual llegó (%s)", confirmer.calls, mpProviderID)
	}
}

func TestMercadoPagoWebhookNotRegisteredWithoutConfirmer(t *testing.T) {
	mux := http.NewServeMux()
	NewMercadoPagoWebhook(nil, MercadoPagoSecrets{Callback: mpCallbackSecret, Webhook: mpWebhookSecret}).Register(mux)

	recorder := sendWebhook(t, mux, defaultWebhook())
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d; sin confirmador la ruta no debería existir", recorder.Code)
	}
}

type payCatalog struct{}

func (payCatalog) Combo(_ context.Context, id string) (catalog.Combo, error) {
	if id != "combo-1" {
		return catalog.Combo{}, catalog.ErrNotFound
	}

	return catalog.Combo{
		ID: "combo-1", Name: "Combo", PriceCents: 450000, Currency: "ARS", Active: true,
		Items: []catalog.Item{{MachineType: machine.Snacks, SKU: "snack-1", PriceCents: 450000}},
	}, nil
}

func (c payCatalog) Combos(ctx context.Context) ([]catalog.Combo, error) {
	combo, _ := c.Combo(ctx, "combo-1")
	return []catalog.Combo{combo}, nil
}

type payOrders struct {
	order store.Order
}

func (p *payOrders) CreateOrder(_ context.Context, draft order.Draft) (store.Order, error) {
	p.order = store.Order{
		ID:          "ord-1",
		KioskID:     draft.KioskID,
		ComboID:     draft.ComboID,
		AmountCents: draft.AmountCents,
		Currency:    draft.Currency,
		Status:      store.OrderPendingPayment,
	}

	return p.order, nil
}

func (p *payOrders) LoadOrder(_ context.Context, id string) (store.Order, error) {
	if id != p.order.ID {
		return store.Order{}, store.ErrNotFound
	}

	return p.order, nil
}

func (p *payOrders) MarkPaid(context.Context, string, time.Time) error {
	return errors.New("el pago simulado no debería usarse con Mercado Pago")
}

type payCharger struct {
	intent  store.PaymentIntent
	err     error
	posIDs  []string
	found   bool
	readErr error
}

func (c *payCharger) RequestQR(_ context.Context, orderID, posID string) (store.PaymentIntent, error) {
	c.posIDs = append(c.posIDs, posID)
	if c.err != nil {
		return store.PaymentIntent{}, c.err
	}

	c.intent.OrderID = orderID
	c.found = true

	return c.intent, nil
}

func (c *payCharger) PaymentIntentByOrder(_ context.Context, orderID string) (store.PaymentIntent, error) {
	if c.readErr != nil {
		return store.PaymentIntent{}, c.readErr
	}

	if !c.found || orderID != c.intent.OrderID {
		return store.PaymentIntent{}, store.ErrNotFound
	}

	return c.intent, nil
}

func payKiosk() config.Kiosk {
	return config.Kiosk{
		ID:      "kiosk-1",
		MPPosID: "POS-KIOSK-1",
		Machines: []config.Machine{
			{Type: machine.Batidos, DeviceID: "d", DeviceNo: "n", DeviceTypeID: "t"},
			{Type: machine.Snacks, MID: "m"},
		},
	}
}

func newPayHandler(t *testing.T, env string, charger *payCharger) (*http.ServeMux, *payOrders) {
	t.Helper()

	orders := &payOrders{}
	deps := Deps{
		Catalog:      payCatalog{},
		OrderCreator: orders,
		PaidMarker:   orders,
		Orders:       orders,
		Kiosks:       []config.Kiosk{payKiosk()},
		Env:          env,
	}

	if charger != nil {
		deps.QRRequester = charger
		deps.Payments = charger
	}

	handler, err := New(deps)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	mux := http.NewServeMux()
	handler.Register(mux)

	return mux, orders
}

func doJSON(t *testing.T, mux *http.ServeMux, method, target, body string) (int, map[string]any) {
	t.Helper()

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(method, target, strings.NewReader(body)))

	var decoded map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)

	return recorder.Code, decoded
}

func pendingIntent() store.PaymentIntent {
	return store.PaymentIntent{
		ProviderID: mpProviderID,
		Status:     store.PaymentPending,
		QRData:     "00020101021226...",
		ExpiresAt:  mpClock.Add(3 * time.Minute),
	}
}

func TestCreateOrderWithMercadoPagoReturnsQR(t *testing.T) {
	charger := &payCharger{intent: pendingIntent()}
	mux, _ := newPayHandler(t, config.EnvSandbox, charger)

	status, body := doJSON(t, mux, http.MethodPost, "/orders", `{"combo_id":"combo-1","client_key":"k1"}`)
	if status != http.StatusCreated {
		t.Fatalf("status = %d, cuerpo %v", status, body)
	}

	payment, ok := body["payment"].(map[string]any)
	if !ok {
		t.Fatalf("la respuesta no trae el bloque payment: %v", body)
	}

	if payment["status"] != "pending" || payment["qr_data"] != "00020101021226..." || payment["expires_at"] == nil {
		t.Fatalf("payment = %v", payment)
	}

	if len(charger.posIDs) != 1 || charger.posIDs[0] != "POS-KIOSK-1" {
		t.Fatalf("RequestQR recibió las cajas %v; quería la del mueble", charger.posIDs)
	}
}

func TestCreateOrderWithoutMercadoPagoHasNoPaymentBlock(t *testing.T) {
	mux, _ := newPayHandler(t, config.EnvSandbox, nil)

	status, body := doJSON(t, mux, http.MethodPost, "/orders", `{"combo_id":"combo-1"}`)
	if status != http.StatusCreated {
		t.Fatalf("status = %d, cuerpo %v", status, body)
	}

	if _, present := body["payment"]; present {
		t.Fatalf("sin Mercado Pago no debería haber bloque payment: %v", body)
	}
}

func TestCreateOrderMapsChargeErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"Mercado Pago no crea el QR", fmt.Errorf("%w: orden ord-1: timeout", order.ErrQRUnavailable), http.StatusBadGateway},
		{"la orden ya no espera el pago", fmt.Errorf("%w: cancelada", order.ErrNotPayable), http.StatusConflict},
		{"falla del store", errors.New("se cayó postgres"), http.StatusInternalServerError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux, _ := newPayHandler(t, config.EnvSandbox, &payCharger{err: tc.err})

			status, body := doJSON(t, mux, http.MethodPost, "/orders", `{"combo_id":"combo-1"}`)
			if status != tc.want {
				t.Fatalf("status = %d, quería %d (cuerpo %v)", status, tc.want, body)
			}
		})
	}
}

func TestReadOrderPaymentBlock(t *testing.T) {
	t.Run("pendiente trae el QR", func(t *testing.T) {
		charger := &payCharger{intent: pendingIntent()}
		mux, _ := newPayHandler(t, config.EnvSandbox, charger)
		doJSON(t, mux, http.MethodPost, "/orders", `{"combo_id":"combo-1"}`)

		_, body := doJSON(t, mux, http.MethodGet, "/orders/ord-1", "")

		payment, _ := body["payment"].(map[string]any)
		if payment["status"] != "pending" || payment["qr_data"] == nil {
			t.Fatalf("payment = %v", payment)
		}
	})

	t.Run("cerrado no trae el QR", func(t *testing.T) {
		intent := pendingIntent()
		intent.Status = store.PaymentExpired
		charger := &payCharger{intent: intent}
		mux, _ := newPayHandler(t, config.EnvSandbox, charger)
		doJSON(t, mux, http.MethodPost, "/orders", `{"combo_id":"combo-1"}`)

		_, body := doJSON(t, mux, http.MethodGet, "/orders/ord-1", "")

		payment, _ := body["payment"].(map[string]any)
		if payment["status"] != "expired" {
			t.Fatalf("payment = %v", payment)
		}

		if _, present := payment["qr_data"]; present {
			t.Fatalf("un cobro vencido no debería exponer el QR: %v", payment)
		}
	})

	t.Run("sin cobro no hay bloque", func(t *testing.T) {
		charger := &payCharger{intent: pendingIntent()}
		mux, orders := newPayHandler(t, config.EnvSandbox, charger)
		orders.order = store.Order{ID: "ord-1", Status: store.OrderPendingPayment}

		status, body := doJSON(t, mux, http.MethodGet, "/orders/ord-1", "")
		if status != http.StatusOK {
			t.Fatalf("status = %d", status)
		}

		if _, present := body["payment"]; present {
			t.Fatalf("sin cobro guardado no debería haber bloque payment: %v", body)
		}
	})

	t.Run("falla al leer el cobro", func(t *testing.T) {
		charger := &payCharger{readErr: errors.New("se cayó postgres")}
		mux, orders := newPayHandler(t, config.EnvSandbox, charger)
		orders.order = store.Order{ID: "ord-1", Status: store.OrderPendingPayment}

		status, _ := doJSON(t, mux, http.MethodGet, "/orders/ord-1", "")
		if status != http.StatusInternalServerError {
			t.Fatalf("status = %d, quería 500", status)
		}
	})
}

func TestSimulatedPaymentIsOffWithMercadoPago(t *testing.T) {
	mux, _ := newPayHandler(t, config.EnvSandbox, &payCharger{intent: pendingIntent()})
	doJSON(t, mux, http.MethodPost, "/orders", `{"combo_id":"combo-1"}`)

	status, _ := doJSON(t, mux, http.MethodPost, "/orders/ord-1/pay", "")
	if status != http.StatusForbidden {
		t.Fatalf("status = %d; con Mercado Pago el pago simulado debería estar apagado", status)
	}
}

func TestHandlerRejectsHalfPaymentDeps(t *testing.T) {
	orders := &payOrders{}
	_, err := New(Deps{
		Catalog:      payCatalog{},
		OrderCreator: orders,
		PaidMarker:   orders,
		Orders:       orders,
		Kiosks:       []config.Kiosk{payKiosk()},
		Env:          config.EnvSandbox,
		QRRequester:  &payCharger{},
	})
	if err == nil {
		t.Fatal("New debería rechazar QRRequester sin Payments")
	}
}

type mpFakeGateway struct {
	charges map[string]payments.Charge
}

func (g *mpFakeGateway) CreateQR(_ context.Context, req payments.QRRequest) (payments.Charge, error) {
	c := payments.Charge{
		ProviderID:  mpProviderID,
		ExternalRef: req.OrderID,
		QRData:      "qr-data",
		Status:      payments.ChargePending,
		AmountCents: req.AmountCents,
	}
	g.charges[c.ProviderID] = c
	return c, nil
}

func (g *mpFakeGateway) Lookup(_ context.Context, providerID string) (payments.Charge, error) {
	c, ok := g.charges[providerID]
	if !ok {
		return payments.Charge{}, payments.ErrNotFound
	}
	return c, nil
}

func (g *mpFakeGateway) Cancel(_ context.Context, providerID string) (payments.Charge, error) {
	return g.charges[providerID], nil
}

type failingSaveOrderStore struct {
	*store.Memory
	saveOrderErr error
}

func (s *failingSaveOrderStore) SaveOrder(ctx context.Context, o store.Order) (store.Order, error) {
	if s.saveOrderErr != nil {
		return store.Order{}, s.saveOrderErr
	}
	return s.Memory.SaveOrder(ctx, o)
}

func TestMercadoPagoWebhookLogsOrderIDAndRequestID(t *testing.T) {
	setupEngine := func(t *testing.T) (*order.Engine, *mpFakeGateway, *failingSaveOrderStore, store.Order) {
		t.Helper()
		mem := &failingSaveOrderStore{Memory: store.NewMemory()}
		gw := &mpFakeGateway{charges: make(map[string]payments.Charge)}
		engine := order.New(
			mem,
			testMachines(),
			order.WithClock(func() time.Time { return mpClock }),
			order.WithQRPayments("mercadopago", gw),
		)
		placed, err := engine.CreateOrder(context.Background(), order.Draft{
			KioskID:     "kiosk-1",
			ComboID:     "combo-1",
			AmountCents: 450000,
			Currency:    "ARS",
			Items: []order.Item{{
				SKU:         "snack-1",
				AmountCents: 450000,
				Machine:     machine.Ref{KioskID: "kiosk-1", Kind: machine.Snacks, MID: "m"},
			}},
		})
		if err != nil {
			t.Fatalf("CreateOrder: %v", err)
		}
		if _, err := engine.RequestQR(context.Background(), placed.ID, "POS-1"); err != nil {
			t.Fatalf("RequestQR: %v", err)
		}
		return engine, gw, mem, placed
	}

	t.Run("cobro duplicado para orden conocida incluye order_id y request_id", func(t *testing.T) {
		engine, gw, _, placed := setupEngine(t)
		dupID := "ORD_DUP_01"
		gw.charges[dupID] = payments.Charge{
			ProviderID:      dupID,
			ExternalRef:     placed.ID,
			Status:          payments.ChargeApproved,
			PaidAmountCents: 450000,
			AmountCents:     450000,
		}

		var buf bytes.Buffer
		logger := logging.New(&buf, slog.LevelDebug)
		mux := http.NewServeMux()
		NewMercadoPagoWebhook(engine, MercadoPagoSecrets{Callback: mpCallbackSecret, Webhook: mpWebhookSecret},
			WithMercadoPagoClock(func() time.Time { return mpClock }),
			WithMercadoPagoLogger(logger),
		).Register(mux)
		wrapped := Wrap(mux, logger)

		req := defaultWebhook()
		req.dataID = dupID
		rec := sendWebhook(t, wrapped, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, quería 200", rec.Code)
		}

		reqID := rec.Header().Get("X-Request-Id")
		records := parseJSONLogLines(t, buf.String())
		if len(records) != 2 {
			t.Fatalf("se esperaban 2 logs (warn + access), llegaron %d: %s", len(records), buf.String())
		}
		if records[0]["order_id"] != placed.ID || records[0]["request_id"] != reqID {
			t.Fatalf("log sin order_id=%q o request_id=%q: %v", placed.ID, reqID, records[0])
		}
	})

	t.Run("falla transitoria tras resolver la orden incluye order_id y request_id", func(t *testing.T) {
		engine, gw, mem, placed := setupEngine(t)
		gw.charges[mpProviderID] = payments.Charge{
			ProviderID:      mpProviderID,
			ExternalRef:     placed.ID,
			Status:          payments.ChargeApproved,
			PaidAmountCents: 450000,
			AmountCents:     450000,
		}
		mem.saveOrderErr = errors.New("se cayó postgres")

		var buf bytes.Buffer
		logger := logging.New(&buf, slog.LevelDebug)
		mux := http.NewServeMux()
		NewMercadoPagoWebhook(engine, MercadoPagoSecrets{Callback: mpCallbackSecret, Webhook: mpWebhookSecret},
			WithMercadoPagoClock(func() time.Time { return mpClock }),
			WithMercadoPagoLogger(logger),
		).Register(mux)
		wrapped := Wrap(mux, logger)

		rec := sendWebhook(t, wrapped, defaultWebhook())
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, quería 500", rec.Code)
		}

		reqID := rec.Header().Get("X-Request-Id")
		records := parseJSONLogLines(t, buf.String())
		if len(records) != 2 {
			t.Fatalf("se esperaban 2 logs (error + access), llegaron %d: %s", len(records), buf.String())
		}
		if records[0]["order_id"] != placed.ID || records[0]["request_id"] != reqID {
			t.Fatalf("log sin order_id=%q o request_id=%q: %v", placed.ID, reqID, records[0])
		}
	})

	t.Run("firma inválida, tópico ignorado y cobro inexistente no inventan order_id", func(t *testing.T) {
		cases := []struct {
			name   string
			mutate func(*webhookRequest)
			status int
		}{
			{"firma inválida", func(r *webhookRequest) { r.signWith = "otro" }, http.StatusUnauthorized},
			{"tópico ignorado", func(r *webhookRequest) { r.topic = "payment" }, http.StatusOK},
			{"cobro inexistente", func(r *webhookRequest) { r.dataID = "ORDNOEXISTE" }, http.StatusOK},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				engine, _, _, _ := setupEngine(t)
				var buf bytes.Buffer
				logger := logging.New(&buf, slog.LevelDebug)
				mux := http.NewServeMux()
				NewMercadoPagoWebhook(engine, MercadoPagoSecrets{Callback: mpCallbackSecret, Webhook: mpWebhookSecret},
					WithMercadoPagoClock(func() time.Time { return mpClock }),
					WithMercadoPagoLogger(logger),
				).Register(mux)
				wrapped := Wrap(mux, logger)

				req := defaultWebhook()
				tc.mutate(&req)
				rec := sendWebhook(t, wrapped, req)
				if rec.Code != tc.status {
					t.Fatalf("status = %d, quería %d", rec.Code, tc.status)
				}

				reqID := rec.Header().Get("X-Request-Id")
				records := parseJSONLogLines(t, buf.String())
				if len(records) != 2 {
					t.Fatalf("se esperaban 2 logs (handler + access), llegaron %d: %s", len(records), buf.String())
				}
				if _, ok := records[0]["order_id"]; ok {
					t.Fatalf("no debía incluir order_id: %v", records[0])
				}
				if records[0]["request_id"] != reqID {
					t.Fatalf("falta request_id=%q: %v", reqID, records[0])
				}
			})
		}
	})
}

func sendBodyOnlyWebhook(t *testing.T, mux http.Handler, body string, signedID string) *httptest.ResponseRecorder {
	t.Helper()

	target := "/callbacks/mercadopago/" + mpCallbackSecret
	httpReq := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))

	const requestID = "2066ca19-c6f1-498a-be75-1923005edd06"
	httpReq.Header.Set("X-Request-Id", requestID)
	httpReq.Header.Set("X-Signature", mercadopago.SignatureHeader(mpWebhookSecret, requestID, signedID, mpClock))

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httpReq)

	return recorder
}

func TestMercadoPagoWebhookFallsBackToBodyDataID(t *testing.T) {
	body := `{"action":"order.processed","type":"order","data":{"id":"` + mpProviderID + `"}}`

	cases := []struct {
		name       string
		body       string
		signedID   string
		wantStatus int
		wantCalls  int
	}{
		{"data.id sólo en el cuerpo", body, "", http.StatusOK, 1},
		{"firma que incluye un id que no vino en la query", body, mpProviderID, http.StatusUnauthorized, 0},
		{"cuerpo de otro tópico", `{"type":"payment","data":{"id":"123"}}`, "", http.StatusOK, 0},
		{"sin data.id ni en query ni en cuerpo", `{"type":"order"}`, "", http.StatusBadRequest, 0},
		{"cuerpo que no es JSON", `no-json`, "", http.StatusBadRequest, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			confirmer := &recordingConfirmer{}

			recorder := sendBodyOnlyWebhook(t, newMPMux(confirmer), tc.body, tc.signedID)

			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, quería %d (cuerpo %s)", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
			if len(confirmer.calls) != tc.wantCalls {
				t.Fatalf("SyncPayment se llamó %d veces, quería %d", len(confirmer.calls), tc.wantCalls)
			}
			if tc.wantCalls == 1 && confirmer.calls[0] != mpProviderID {
				t.Fatalf("llamadas = %v; quería %s", confirmer.calls, mpProviderID)
			}
		})
	}
}
