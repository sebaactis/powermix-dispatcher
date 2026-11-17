package mercadopago

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"compras-remotas/internal/payments"
)

type recorded struct {
	method      string
	path        string
	auth        string
	idempotency string
	body        map[string]any
}

func fakeServer(t *testing.T, status int, response string) (*httptest.Server, *recorded) {
	t.Helper()
	rec := &recorded{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		rec.path = r.URL.Path
		rec.auth = r.Header.Get("Authorization")
		rec.idempotency = r.Header.Get("X-Idempotency-Key")
		raw, _ := io.ReadAll(r.Body)
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &rec.body); err != nil {
				t.Errorf("cuerpo no es JSON: %v", err)
			}
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func qrRequest() payments.QRRequest {
	return payments.QRRequest{
		OrderID:     "0a1b2c3d4e5f",
		PosID:       "MUEBLE01POS01",
		AmountCents: 150050,
		Title:       "Combo batido + snack",
		ExpiresIn:   180 * time.Second,
	}
}

const createdOrder = `{
  "id": "ORD01JQ4S4KY8HWQ6NA5PXB65B3D3",
  "type": "qr",
  "external_reference": "0a1b2c3d4e5f",
  "total_amount": "1500.50",
  "status": "created",
  "status_detail": "created",
  "type_response": {"qr_data": "00020101021243650016COM.MERCADOLIBRE"},
  "transactions": {"payments": [{"id": "PAY1", "amount": "1500.50", "status": "created"}]}
}`

func TestCreateQRSendsDynamicOrder(t *testing.T) {
	srv, rec := fakeServer(t, http.StatusCreated, createdOrder)
	charge, err := NewClient(srv.URL, "TOKEN").CreateQR(context.Background(), qrRequest())
	if err != nil {
		t.Fatalf("CreateQR: %v", err)
	}

	if rec.method != http.MethodPost || rec.path != "/v1/orders" {
		t.Fatalf("pedido = %s %s", rec.method, rec.path)
	}
	if rec.auth != "Bearer TOKEN" {
		t.Fatalf("Authorization = %q", rec.auth)
	}
	if rec.idempotency != "0a1b2c3d4e5f" {
		t.Fatalf("X-Idempotency-Key = %q, se esperaba el id de la orden", rec.idempotency)
	}

	b := rec.body
	qr := b["config"].(map[string]any)["qr"].(map[string]any)
	payment := b["transactions"].(map[string]any)["payments"].([]any)[0].(map[string]any)
	checks := map[string][2]any{
		"type":               {b["type"], "qr"},
		"total_amount":       {b["total_amount"], "1500.50"},
		"external_reference": {b["external_reference"], "0a1b2c3d4e5f"},
		"expiration_time":    {b["expiration_time"], "PT3M"},
		"mode":               {qr["mode"], "dynamic"},
		"external_pos_id":    {qr["external_pos_id"], "MUEBLE01POS01"},
		"payments.amount":    {payment["amount"], "1500.50"},
	}
	for field, pair := range checks {
		if pair[0] != pair[1] {
			t.Errorf("%s = %v, se esperaba %v", field, pair[0], pair[1])
		}
	}

	want := payments.Charge{
		ProviderID:           "ORD01JQ4S4KY8HWQ6NA5PXB65B3D3",
		ExternalRef:          "0a1b2c3d4e5f",
		QRData:               "00020101021243650016COM.MERCADOLIBRE",
		Status:               payments.ChargePending,
		ProviderStatus:       "created",
		ProviderStatusDetail: "created",
		AmountCents:          150050,
	}
	if charge != want {
		t.Fatalf("charge = %+v\nse esperaba %+v", charge, want)
	}
}

func TestCreateQRRejectsInvalidRequestWithoutCalling(t *testing.T) {
	mutations := map[string]func(*payments.QRRequest){
		"sin orden":         func(r *payments.QRRequest) { r.OrderID = "" },
		"orden con espacio": func(r *payments.QRRequest) { r.OrderID = "a b" },
		"orden de 65":       func(r *payments.QRRequest) { r.OrderID = strings.Repeat("a", 65) },
		"sin caja":          func(r *payments.QRRequest) { r.PosID = "" },
		"monto cero":        func(r *payments.QRRequest) { r.AmountCents = 0 },
		"sin título":        func(r *payments.QRRequest) { r.Title = "" },
		"sin vencimiento":   func(r *payments.QRRequest) { r.ExpiresIn = 0 },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			srv, rec := fakeServer(t, http.StatusCreated, createdOrder)
			req := qrRequest()
			mutate(&req)
			if _, err := NewClient(srv.URL, "TOKEN").CreateQR(context.Background(), req); err == nil {
				t.Fatal("se aceptó un pedido inválido")
			}
			if rec.method != "" {
				t.Fatal("se llamó a Mercado Pago con un pedido inválido")
			}
		})
	}
}

func TestCreateQRRejectsResponseWithoutQRData(t *testing.T) {
	srv, _ := fakeServer(t, http.StatusCreated, `{"id":"ORD1","external_reference":"0a1b2c3d4e5f","total_amount":"1500.50","status":"created"}`)
	if _, err := NewClient(srv.URL, "TOKEN").CreateQR(context.Background(), qrRequest()); err == nil {
		t.Fatal("se aceptó una orden sin qr_data")
	}
}

func TestCreateQRRejectsForeignExternalReference(t *testing.T) {
	srv, _ := fakeServer(t, http.StatusCreated, strings.Replace(createdOrder, `"external_reference": "0a1b2c3d4e5f"`, `"external_reference": "otra"`, 1))
	if _, err := NewClient(srv.URL, "TOKEN").CreateQR(context.Background(), qrRequest()); err == nil {
		t.Fatal("se aceptó una orden de otro external_reference")
	}
}

func TestLookupMapsStatusAndPaidAmount(t *testing.T) {
	cases := []struct {
		name     string
		response string
		status   payments.ChargeStatus
		paid     int64
	}{
		{"pagada con total_paid_amount", `{"id":"ORD1","total_amount":"10.00","total_paid_amount":"10.00","status":"processed","status_detail":"accredited"}`, payments.ChargeApproved, 1000},
		{"pagada sin total_paid_amount", `{"id":"ORD1","total_amount":"10.00","status":"processed","transactions":{"payments":[{"amount":"10.00","status":"processed"},{"amount":"3.00","status":"created"}]}}`, payments.ChargeApproved, 1000},
		{"paid_amount gana sobre amount", `{"id":"ORD1","total_amount":"10.00","status":"processed","transactions":{"payments":[{"amount":"10.00","paid_amount":"9.50","status":"processed"}]}}`, payments.ChargeApproved, 950},
		{"pendiente", `{"id":"ORD1","total_amount":"10.00","status":"created"}`, payments.ChargePending, 0},
		{"vencida", `{"id":"ORD1","total_amount":"10.00","status":"expired"}`, payments.ChargeExpired, 0},
		{"cancelada", `{"id":"ORD1","total_amount":"10.00","status":"canceled"}`, payments.ChargeCancelled, 0},
		{"reembolsada", `{"id":"ORD1","total_amount":"10.00","total_paid_amount":"10.00","status":"refunded"}`, payments.ChargeUnknown, 1000},
		{"fallida", `{"id":"ORD1","total_amount":"10.00","status":"failed"}`, payments.ChargeUnknown, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, rec := fakeServer(t, http.StatusOK, tc.response)
			charge, err := NewClient(srv.URL, "TOKEN").Lookup(context.Background(), "ORD1")
			if err != nil {
				t.Fatalf("Lookup: %v", err)
			}
			if rec.method != http.MethodGet || rec.path != "/v1/orders/ORD1" {
				t.Fatalf("pedido = %s %s", rec.method, rec.path)
			}
			if charge.Status != tc.status || charge.PaidAmountCents != tc.paid {
				t.Fatalf("status=%s paid=%d, se esperaba status=%s paid=%d", charge.Status, charge.PaidAmountCents, tc.status, tc.paid)
			}
		})
	}
}

func TestLookupNotFound(t *testing.T) {
	srv, _ := fakeServer(t, http.StatusNotFound, `{"errors":[{"code":"order_not_found","message":"not found"}]}`)
	_, err := NewClient(srv.URL, "TOKEN").Lookup(context.Background(), "ORD1")
	if !errors.Is(err, payments.ErrNotFound) {
		t.Fatalf("err = %v, se esperaba ErrNotFound", err)
	}
}

func TestLookupRejectsMalformedAmounts(t *testing.T) {
	for _, response := range []string{
		`{"id":"ORD1","total_amount":"10,00","status":"created"}`,
		`{"id":"ORD1","total_amount":"10.00","total_paid_amount":"x","status":"processed"}`,
		`{"total_amount":"10.00","status":"created"}`,
		`no es json`,
	} {
		srv, _ := fakeServer(t, http.StatusOK, response)
		if _, err := NewClient(srv.URL, "TOKEN").Lookup(context.Background(), "ORD1"); err == nil {
			t.Errorf("se aceptó la respuesta %s", response)
		}
	}
}

func TestAPIErrorCarriesProviderCode(t *testing.T) {
	srv, _ := fakeServer(t, http.StatusBadRequest, `{"errors":[{"code":"invalid_pos","message":"pos not found"}]}`)
	_, err := NewClient(srv.URL, "TOKEN").CreateQR(context.Background(), qrRequest())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.HTTPStatus != 400 || apiErr.Code != "invalid_pos" {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "TOKEN") {
		t.Fatal("el error expone el access token")
	}
}

func TestAPIErrorTruncatesLongBodies(t *testing.T) {
	srv, _ := fakeServer(t, http.StatusBadGateway, strings.Repeat("x", 5000))
	_, err := NewClient(srv.URL, "TOKEN").Lookup(context.Background(), "ORD1")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || len(apiErr.Message) > maxErrorMessage+len("…") {
		t.Fatalf("err = %v", err)
	}
}

func TestCancel(t *testing.T) {
	srv, rec := fakeServer(t, http.StatusOK, `{"id":"ORD1","total_amount":"10.00","status":"canceled","status_detail":"canceled"}`)
	charge, err := NewClient(srv.URL, "TOKEN").Cancel(context.Background(), "ORD1")
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if rec.method != http.MethodPost || rec.path != "/v1/orders/ORD1/cancel" {
		t.Fatalf("pedido = %s %s", rec.method, rec.path)
	}
	if rec.idempotency != "cancel-ORD1" {
		t.Fatalf("X-Idempotency-Key = %q", rec.idempotency)
	}
	if charge.Status != payments.ChargeCancelled {
		t.Fatalf("status = %s", charge.Status)
	}
}

func TestCancelConflictIsNotCancelable(t *testing.T) {
	srv, _ := fakeServer(t, http.StatusConflict, `{"errors":[{"code":"order_already_processed","message":"cannot cancel"}]}`)
	_, err := NewClient(srv.URL, "TOKEN").Cancel(context.Background(), "ORD1")
	if !errors.Is(err, payments.ErrNotCancelable) {
		t.Fatalf("err = %v, se esperaba ErrNotCancelable", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "order_already_processed" {
		t.Fatalf("se perdió el error del proveedor: %v", err)
	}
}

func TestLookupEscapesProviderID(t *testing.T) {
	srv, rec := fakeServer(t, http.StatusOK, `{"id":"ORD1","total_amount":"1.00","status":"created"}`)
	_, _ = NewClient(srv.URL, "TOKEN").Lookup(context.Background(), "../payments")
	if rec.path == "/v1/payments" {
		t.Fatal("el id escapó de /v1/orders")
	}
}

func TestDefaultBaseURL(t *testing.T) {
	if c := NewClient("", "TOKEN"); c.baseURL != DefaultBaseURL {
		t.Fatalf("baseURL = %q", c.baseURL)
	}
}
