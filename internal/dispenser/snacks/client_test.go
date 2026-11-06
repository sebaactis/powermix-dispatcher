package snacks_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"compras-remotas/internal/dispenser/snacks"
	"compras-remotas/internal/fakeapi"
)

const (
	testAccessKey   = "ak_test"
	testAppSecret   = "testsecret"
	testMID         = "P50*******20001"
	testPath        = "/open/v7/device/remote/shipment"
	testDevicesPath = "/open/v7/device/page"
	testSlotsPath   = "/open/v7/device/slotlist"
	testOrdersPath  = "/open/v7/order/page"
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

func TestDevicesReturnsThePaginatedPage(t *testing.T) {
	_, baseURL := newFake(t)

	client := snacks.NewClient(baseURL, testAccessKey, testAppSecret)

	page, err := client.Devices(context.Background(), snacks.PageQuery{Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("Devices: %v", err)
	}
	if page.Page != 1 {
		t.Fatalf("page.Page = %d, want 1", page.Page)
	}
	if page.PageSize != 10 {
		t.Fatalf("page.PageSize = %d, want 10", page.PageSize)
	}
	if page.Total != 1 {
		t.Fatalf("page.Total = %d, want 1", page.Total)
	}
	if page.TotalPages != 1 {
		t.Fatalf("page.TotalPages = %d, want 1", page.TotalPages)
	}
	if page.HasPrevPage {
		t.Fatalf("page.HasPrevPage = true, want false en la primera página")
	}
	if page.HasNextPage {
		t.Fatalf("page.HasNextPage = true, want false con una sola página")
	}
	if len(page.Items) != 1 {
		t.Fatalf("page.Items tiene %d elementos, want 1", len(page.Items))
	}

	device := page.Items[0]
	if device.DeviceCode == "" {
		t.Fatalf("device.DeviceCode vacío, want el código de la máquina")
	}
	if device.OnlineStatus != snacks.OnlineStatusOnline {
		t.Fatalf("device.OnlineStatus = %d, want %d", device.OnlineStatus, snacks.OnlineStatusOnline)
	}
	if device.SellStatus != snacks.SellStatusNormal {
		t.Fatalf("device.SellStatus = %d, want %d", device.SellStatus, snacks.SellStatusNormal)
	}
	if device.FaultState != snacks.FaultStateNormal {
		t.Fatalf("device.FaultState = %d, want %d", device.FaultState, snacks.FaultStateNormal)
	}
	if device.LockType != snacks.LockTypeNormal {
		t.Fatalf("device.LockType = %d, want %d", device.LockType, snacks.LockTypeNormal)
	}
	if device.StockoutState != snacks.StockoutStateNormal {
		t.Fatalf("device.StockoutState = %d, want %d", device.StockoutState, snacks.StockoutStateNormal)
	}
}

func TestDevicesFiltersByDeviceCode(t *testing.T) {
	_, baseURL := newFake(t)

	client := snacks.NewClient(baseURL, testAccessKey, testAppSecret)

	page, err := client.Devices(context.Background(), snacks.PageQuery{Page: 1, Size: 10, DeviceCode: "otra-maquina"})
	if err != nil {
		t.Fatalf("Devices: %v", err)
	}
	if page.Total != 0 {
		t.Fatalf("page.Total = %d, want 0 para un DeviceCode que no existe", page.Total)
	}
	if len(page.Items) != 0 {
		t.Fatalf("page.Items tiene %d elementos, want 0", len(page.Items))
	}
}

func TestDevicesSurfacesTheMachineStates(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*fakeapi.Snacks)
		check func(*testing.T, snacks.Device)
	}{
		{
			name:  "offline",
			setup: func(f *fakeapi.Snacks) { f.SetDeviceOnline(false) },
			check: func(t *testing.T, device snacks.Device) {
				if device.OnlineStatus != snacks.OnlineStatusOffline {
					t.Fatalf("OnlineStatus = %d, want %d", device.OnlineStatus, snacks.OnlineStatusOffline)
				}
			},
		},
		{
			name:  "sale suspended",
			setup: func(f *fakeapi.Snacks) { f.SetSellStatus(snacks.SellStatusSuspended) },
			check: func(t *testing.T, device snacks.Device) {
				if device.SellStatus != snacks.SellStatusSuspended {
					t.Fatalf("SellStatus = %d, want %d", device.SellStatus, snacks.SellStatusSuspended)
				}
			},
		},
		{
			name:  "fault",
			setup: func(f *fakeapi.Snacks) { f.SetFaultState(snacks.FaultStateFault) },
			check: func(t *testing.T, device snacks.Device) {
				if device.FaultState != snacks.FaultStateFault {
					t.Fatalf("FaultState = %d, want %d", device.FaultState, snacks.FaultStateFault)
				}
			},
		},
		{
			name:  "locked",
			setup: func(f *fakeapi.Snacks) { f.SetLockType(2) },
			check: func(t *testing.T, device snacks.Device) {
				if device.LockType == snacks.LockTypeNormal {
					t.Fatalf("LockType = %d, want un valor que bloquee la máquina", device.LockType)
				}
			},
		},
		{
			name:  "out of stock",
			setup: func(f *fakeapi.Snacks) { f.SetStockoutState(snacks.StockoutStateOutOfStock) },
			check: func(t *testing.T, device snacks.Device) {
				if device.StockoutState != snacks.StockoutStateOutOfStock {
					t.Fatalf("StockoutState = %d, want %d", device.StockoutState, snacks.StockoutStateOutOfStock)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake, baseURL := newFake(t)
			tc.setup(fake)

			client := snacks.NewClient(baseURL, testAccessKey, testAppSecret)

			page, err := client.Devices(context.Background(), snacks.PageQuery{Page: 1, Size: 10})
			if err != nil {
				t.Fatalf("Devices: %v", err)
			}
			if len(page.Items) != 1 {
				t.Fatalf("page.Items tiene %d elementos, want 1", len(page.Items))
			}

			tc.check(t, page.Items[0])
		})
	}
}

func TestDevicesSignsOnlyThePathAndNotTheQueryString(t *testing.T) {
	_, baseURL := newFake(t)

	signedWithQuery, err := snacks.SignatureHeaders(testAccessKey, testAppSecret, http.MethodGet, testDevicesPath+"?Page=1&PageSize=10", time.Now())
	if err != nil {
		t.Fatalf("SignatureHeaders: %v", err)
	}

	req, err := http.NewRequest(http.MethodGet, baseURL+testDevicesPath+"?Page=1&PageSize=10", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	for name, value := range signedWithQuery {
		req.Header.Set(name, value)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	var rejected struct {
		Code int `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rejected); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized || rejected.Code != 40004 {
		t.Fatalf("firmar la ruta con la query dio http=%d code=%d, want http=401 code=40004", resp.StatusCode, rejected.Code)
	}

	client := snacks.NewClient(baseURL, testAccessKey, testAppSecret)

	page, err := client.Devices(context.Background(), snacks.PageQuery{Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("Devices: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("page.Items tiene %d elementos, want 1: el cliente tiene que firmar sólo la ruta", len(page.Items))
	}
}

func TestSlotsReturnsChannelsWithStock(t *testing.T) {
	fake, baseURL := newFake(t)

	client := snacks.NewClient(baseURL, testAccessKey, testAppSecret)

	slots, err := client.Slots(context.Background(), "")
	if err != nil {
		t.Fatalf("Slots: %v", err)
	}
	if len(slots) < 2 {
		t.Fatalf("Slots devolvió %d canales, want al menos 2", len(slots))
	}
	for _, slot := range slots {
		if slot.SlotNo == "" {
			t.Fatalf("slot %+v sin slotNo", slot)
		}
		if slot.ProductID == "" {
			t.Fatalf("slot %q sin productId", slot.SlotNo)
		}
		if slot.Stock <= 0 {
			t.Fatalf("slot %q con stock %d, want stock", slot.SlotNo, slot.Stock)
		}
		if slot.SlotStatus != snacks.SlotStatusNormal {
			t.Fatalf("slot %q con slotStatus %d, want %d", slot.SlotNo, slot.SlotStatus, snacks.SlotStatusNormal)
		}
	}

	fake.SetSlotStock("1", 0)
	fake.SetSlotStatus("2", snacks.SlotStatusDisabled)

	slots, err = client.Slots(context.Background(), "")
	if err != nil {
		t.Fatalf("Slots: %v", err)
	}

	if slots[0].Stock != 0 {
		t.Fatalf("slots[0].Stock = %d, want 0 (sin stock)", slots[0].Stock)
	}
	if slots[1].SlotStatus != snacks.SlotStatusDisabled {
		t.Fatalf("slots[1].SlotStatus = %d, want %d (deshabilitado)", slots[1].SlotStatus, snacks.SlotStatusDisabled)
	}
}

func TestSlotsFiltersByDeviceCode(t *testing.T) {
	_, baseURL := newFake(t)

	client := snacks.NewClient(baseURL, testAccessKey, testAppSecret)

	slots, err := client.Slots(context.Background(), "otra-maquina")
	if err != nil {
		t.Fatalf("Slots: %v", err)
	}
	if len(slots) != 0 {
		t.Fatalf("Slots devolvió %d canales para un DeviceCode que no existe, want 0", len(slots))
	}
	if slots == nil {
		t.Fatalf("Slots devolvió nil, want una lista vacía")
	}
}

func TestSlotsRejectsAMalformedEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"code":    200,
			"message": "ok",
			"result":  map[string]any{"unexpected": true},
		})
	}))
	defer srv.Close()

	client := snacks.NewClient(srv.URL, testAccessKey, testAppSecret)

	if _, err := client.Slots(context.Background(), ""); err == nil {
		t.Fatalf("Slots con result que no es una lista devolvió nil, want error")
	}
}

func TestOrdersSendsPageAndPageSizeAndDecodesTheItems(t *testing.T) {
	var (
		gotPath  string
		gotQuery url.Values
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"code":    200,
			"message": "ok",
			"result": map[string]any{
				"page":        1,
				"pageSize":    10,
				"total":       2,
				"totalPages":  1,
				"hasPrevPage": false,
				"hasNextPage": false,
				"items": []any{
					map[string]any{
						"orderNo":            "20260717827745116217477_6",
						"transactionOrderNo": "po_1",
						"deviceCode":         testMID,
						"orderStatus":        10,
						"orderStatusName":    "pagado",
						"orderAmount":        15.5,
						"payAmount":          15.5,
						"payTime":            "2026-07-22T03:54:46.203Z",
						"orderCreatedTime":   "2026-07-22T03:54:46.203Z",
					},
					map[string]any{
						"orderNo":            "20260717827745116217477_7",
						"transactionOrderNo": "po_2",
						"deviceCode":         testMID,
						"orderStatus":        7,
						"orderStatusName":    "",
						"orderAmount":        0,
						"payAmount":          0,
						"payTime":            "",
						"orderCreatedTime":   "",
					},
				},
			},
		})
	}))
	defer srv.Close()

	client := snacks.NewClient(srv.URL, testAccessKey, testAppSecret)

	page, err := client.Orders(context.Background(), snacks.PageQuery{Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("Orders: %v", err)
	}

	if gotPath != testOrdersPath {
		t.Fatalf("ruta = %q, want %q", gotPath, testOrdersPath)
	}
	if got := gotQuery.Get("Page"); got != "1" {
		t.Fatalf("Page = %q, want 1", got)
	}
	if got := gotQuery.Get("PageSize"); got != "10" {
		t.Fatalf("PageSize = %q, want 10", got)
	}
	if gotQuery.Has("OrderNo") {
		t.Fatalf("OrderNo vacío no debe viajar: %v", gotQuery)
	}

	if page.Page != 1 || page.PageSize != 10 || page.Total != 2 || page.TotalPages != 1 {
		t.Fatalf("paginación = %d/%d/%d/%d, want 1/10/2/1", page.Page, page.PageSize, page.Total, page.TotalPages)
	}
	if page.HasPrevPage || page.HasNextPage {
		t.Fatalf("hasPrevPage/hasNextPage = %v/%v, want false/false", page.HasPrevPage, page.HasNextPage)
	}
	if len(page.Items) != 2 {
		t.Fatalf("Items tiene %d elementos, want 2", len(page.Items))
	}

	order := page.Items[0]
	if order.OrderNo != "20260717827745116217477_6" {
		t.Fatalf("OrderNo = %q", order.OrderNo)
	}
	if order.TransactionOrderNo != "po_1" {
		t.Fatalf("TransactionOrderNo = %q, want po_1", order.TransactionOrderNo)
	}
	if order.DeviceCode != testMID {
		t.Fatalf("DeviceCode = %q, want %q", order.DeviceCode, testMID)
	}
	if order.OrderStatus != 10 {
		t.Fatalf("OrderStatus = %d, want 10 crudo", order.OrderStatus)
	}
	if order.OrderStatusName != "pagado" {
		t.Fatalf("OrderStatusName = %q, want pagado", order.OrderStatusName)
	}
	if order.OrderAmount != 15.5 || order.PayAmount != 15.5 {
		t.Fatalf("orderAmount/payAmount = %v/%v, want 15.5/15.5", order.OrderAmount, order.PayAmount)
	}
	if order.PayTime != "2026-07-22T03:54:46.203Z" {
		t.Fatalf("PayTime = %q, want el string del proveedor sin reinterpretar", order.PayTime)
	}
	if order.OrderCreatedTime != "2026-07-22T03:54:46.203Z" {
		t.Fatalf("OrderCreatedTime = %q, want el string del proveedor sin reinterpretar", order.OrderCreatedTime)
	}

	if page.Items[1].OrderStatus != 7 || page.Items[1].OrderStatusName != "" {
		t.Fatalf("el segundo pedido no se leyó crudo: %+v", page.Items[1])
	}
}

func TestOrdersSendsOrderNoOnlyWhenItIsSet(t *testing.T) {
	var queries []url.Values

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Query())

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"code":    200,
			"message": "ok",
			"result":  map[string]any{"page": 2, "pageSize": 5, "items": []any{}},
		})
	}))
	defer srv.Close()

	client := snacks.NewClient(srv.URL, testAccessKey, testAppSecret)
	ctx := context.Background()

	page, err := client.Orders(ctx, snacks.PageQuery{Page: 2, Size: 5})
	if err != nil {
		t.Fatalf("Orders sin OrderNo: %v", err)
	}
	if page.Items == nil {
		t.Fatalf("Items volvió nil, want una lista vacía")
	}

	if _, err := client.Orders(ctx, snacks.PageQuery{Page: 2, Size: 5, OrderNo: "20260717827745116217477_6"}); err != nil {
		t.Fatalf("Orders con OrderNo: %v", err)
	}

	if len(queries) != 2 {
		t.Fatalf("se hicieron %d llamadas, want 2", len(queries))
	}
	if queries[0].Has("OrderNo") {
		t.Fatalf("OrderNo vacío no debe viajar: %v", queries[0])
	}
	if got := queries[1].Get("OrderNo"); got != "20260717827745116217477_6" {
		t.Fatalf("OrderNo = %q, want el pedido pedido", got)
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
