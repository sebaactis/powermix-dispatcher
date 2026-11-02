package snacks_test

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

	"compras-remotas/internal/dispenser/snacks"
	"compras-remotas/internal/fakeapi"
)

const (
	testAccessKey = "ak_test"
	testAppSecret = "testsecret"
	testMID       = "P50*******20001"
	testPath      = "/open/v7/device/remote/shipment"
)

func newFake(t *testing.T) (*fakeapi.Snacks, string) {
	t.Helper()

	fake := fakeapi.NewSnacks(testAccessKey, testAppSecret)
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	return fake, srv.URL
}

func shipmentRequest(paymentOrderID string) snacks.ShipmentRequest {
	return snacks.ShipmentRequest{
		PaymentOrderID: paymentOrderID,
		MID:            testMID,
		Products: []snacks.ShipmentProduct{
			{OrderProductID: "line_1", ProductID: "prod_snack_1", RetailPrice: 0.01},
		},
	}
}

func TestShipAcceptedAndRecordedByTheFake(t *testing.T) {
	fake, baseURL := newFake(t)

	client := snacks.NewClient(baseURL, testAccessKey, testAppSecret)

	attempt, err := client.Ship(context.Background(), shipmentRequest("po_1"))
	if err != nil {
		t.Fatalf("Ship: %v", err)
	}
	if !attempt.Accepted {
		t.Fatalf("attempt.Accepted = false, want true")
	}
	if got := fake.ShipmentCount("po_1"); got != 1 {
		t.Fatalf("ShipmentCount(po_1) = %d, want 1", got)
	}
}

func TestShipTwiceWithTheSameOrderUsesFreshNonces(t *testing.T) {
	fake, baseURL := newFake(t)

	client := snacks.NewClient(baseURL, testAccessKey, testAppSecret)

	for i := 1; i <= 2; i++ {
		attempt, err := client.Ship(context.Background(), shipmentRequest("po_1"))
		if err != nil {
			t.Fatalf("Ship llamada %d: %v", i, err)
		}
		if !attempt.Accepted {
			t.Fatalf("Ship llamada %d: attempt.Accepted = false, want true", i)
		}
	}

	if got := fake.ShipmentCount("po_1"); got != 2 {
		t.Fatalf("ShipmentCount(po_1) = %d, want 2", got)
	}
}

func TestShipWithWrongSecretReturnsAPIError(t *testing.T) {
	_, baseURL := newFake(t)

	client := snacks.NewClient(baseURL, testAccessKey, "secreto-equivocado")

	_, err := client.Ship(context.Background(), shipmentRequest("po_1"))
	if err == nil {
		t.Fatalf("Ship con secreto equivocado devolvió nil, want *snacks.APIError")
	}

	var apiErr *snacks.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("errors.As(*snacks.APIError) = false para el error %v (%T)", err, err)
	}
	if apiErr.HTTPStatus != http.StatusUnauthorized {
		t.Fatalf("APIError.HTTPStatus = %d, want 401", apiErr.HTTPStatus)
	}
	if apiErr.Code != 40004 {
		t.Fatalf("APIError.Code = %d, want 40004 (firma inválida)", apiErr.Code)
	}
}

func TestShipSendsTheProviderFieldNames(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotBody   []byte
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("leer cuerpo: %v", err)
		}
		gotBody = body

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"code":    200,
			"message": "ok",
			"result":  map[string]any{},
		})
	}))
	defer srv.Close()

	fixed := time.Now()
	client := snacks.NewClient(srv.URL, testAccessKey, testAppSecret,
		snacks.WithHTTPClient(&http.Client{Timeout: 5 * time.Second}),
		snacks.WithClock(func() time.Time { return fixed }),
	)

	if _, err := client.Ship(context.Background(), shipmentRequest("po_42")); err != nil {
		t.Fatalf("Ship: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Fatalf("método = %q, want POST", gotMethod)
	}
	if gotPath != testPath {
		t.Fatalf("ruta = %q, want %q", gotPath, testPath)
	}

	var root map[string]any
	if err := json.Unmarshal(gotBody, &root); err != nil {
		t.Fatalf("el cuerpo no es JSON: %v (%s)", err, gotBody)
	}

	if got, want := stringField(t, root, "PaymentOrderId"), "po_42"; got != want {
		t.Fatalf("PaymentOrderId = %q, want %q", got, want)
	}
	if got, want := stringField(t, root, "Mid"), testMID; got != want {
		t.Fatalf("Mid = %q, want %q", got, want)
	}

	if got, want := stringField(t, root, "AccessKey"), testAccessKey; got != want {
		t.Fatalf("AccessKey = %q, want %q", got, want)
	}

	items, ok := root["DeviceRemoteShipmentProducts"].([]any)
	if !ok {
		t.Fatalf("DeviceRemoteShipmentProducts ausente o no es una lista: %s", gotBody)
	}
	if len(items) != 1 {
		t.Fatalf("DeviceRemoteShipmentProducts tiene %d elementos, want 1", len(items))
	}
	item, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("el elemento de DeviceRemoteShipmentProducts no es un objeto: %s", gotBody)
	}

	if got, want := stringField(t, item, "OrderProductId"), "line_1"; got != want {
		t.Fatalf("OrderProductId = %q, want %q", got, want)
	}
	if got, want := stringField(t, item, "ProductId"), "prod_snack_1"; got != want {
		t.Fatalf("ProductId = %q, want %q", got, want)
	}

	pricing, ok := item["ItemPricing"].(map[string]any)
	if !ok {
		t.Fatalf("ItemPricing ausente o no es un objeto: %s", gotBody)
	}
	if got, want := numberField(t, pricing, "retailPrice"), 0.01; got != want {
		t.Fatalf("ItemPricing.retailPrice = %v, want %v", got, want)
	}

	for _, forbidden := range []string{`"payment_order_id"`, `"device_remote_shipment_products"`, `"orderProductId"`, `"productId"`} {
		if strings.Contains(string(gotBody), forbidden) {
			t.Fatalf("el cuerpo contiene %s, que no es el nombre documentado: %s", forbidden, gotBody)
		}
	}
}

func TestShipWithSkewedClockReturnsAPIError(t *testing.T) {
	_, baseURL := newFake(t)

	stale := time.Now().Add(-10 * time.Minute)
	client := snacks.NewClient(baseURL, testAccessKey, testAppSecret,
		snacks.WithClock(func() time.Time { return stale }),
	)

	_, err := client.Ship(context.Background(), shipmentRequest("po_1"))
	var apiErr *snacks.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("errors.As(*snacks.APIError) = false para el error %v (%T)", err, err)
	}
	if apiErr.Code != 40003 {
		t.Fatalf("APIError.Code = %d, want 40003 (timestamp fuera de la ventana)", apiErr.Code)
	}
}

func TestShipTreatsHTTP200WithErrorCodeAsRejection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"code":    400,
			"message": "Parameter validation failed",
			"data":    nil,
		})
	}))
	defer srv.Close()

	client := snacks.NewClient(srv.URL, testAccessKey, testAppSecret)

	_, err := client.Ship(context.Background(), shipmentRequest("po_1"))
	var apiErr *snacks.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("errors.As(*snacks.APIError) = false para el error %v (%T)", err, err)
	}
	if apiErr.HTTPStatus != http.StatusOK {
		t.Fatalf("APIError.HTTPStatus = %d, want 200", apiErr.HTTPStatus)
	}
	if apiErr.Code != 400 {
		t.Fatalf("APIError.Code = %d, want 400", apiErr.Code)
	}
}

func TestShipRespectsCancelledContext(t *testing.T) {
	_, baseURL := newFake(t)

	client := snacks.NewClient(baseURL, testAccessKey, testAppSecret)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.Ship(ctx, shipmentRequest("po_1"))
	if err == nil {
		t.Fatalf("Ship con contexto cancelado devolvió nil, want error")
	}

	var apiErr *snacks.APIError
	if errors.As(err, &apiErr) {
		t.Fatalf("un contexto cancelado no es un error de la API: %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want que envuelva context.Canceled", err)
	}
}

func stringField(t *testing.T, obj map[string]any, key string) string {
	t.Helper()

	raw, ok := obj[key]
	if !ok {
		t.Fatalf("falta el campo %q en el JSON enviado: %v", key, obj)
	}
	s, ok := raw.(string)
	if !ok {
		t.Fatalf("el campo %q no es string: %T", key, raw)
	}
	return s
}

func numberField(t *testing.T, obj map[string]any, key string) float64 {
	t.Helper()

	raw, ok := obj[key]
	if !ok {
		t.Fatalf("falta el campo %q en el JSON enviado: %v", key, obj)
	}
	n, ok := raw.(float64)
	if !ok {
		t.Fatalf("el campo %q no es número: %T", key, raw)
	}
	return n
}
