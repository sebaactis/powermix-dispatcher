package fakeapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"compras-remotas/internal/dispenser/snacks"
)

type snacksEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Result  json.RawMessage `json:"result"`
}

type snacksDevicePage struct {
	Page        int  `json:"page"`
	PageSize    int  `json:"pageSize"`
	Total       int  `json:"total"`
	TotalPages  int  `json:"totalPages"`
	HasPrevPage bool `json:"hasPrevPage"`
	HasNextPage bool `json:"hasNextPage"`
	Items       []struct {
		DeviceCode          string `json:"deviceCode"`
		DeviceName          string `json:"deviceName"`
		DeviceTypeName      string `json:"deviceTypeName"`
		OnlineStatus        int    `json:"onlineStatus"`
		SellStatus          int    `json:"sellStatus"`
		FaultState          int    `json:"faultState"`
		LockType            int    `json:"lockType"`
		StockoutState       int    `json:"stockoutState"`
		ShortageAlarmNumber int    `json:"shortageAlarmNumber"`
	} `json:"items"`
}

type snacksSlotList []struct {
	SlotID      string  `json:"slotId"`
	SlotNo      string  `json:"slotNo"`
	SlotStatus  int     `json:"slotStatus"`
	Stock       int     `json:"stock"`
	Capacity    int     `json:"capacity"`
	ProductID   string  `json:"productId"`
	ProductName string  `json:"productName"`
	Price       float64 `json:"price"`
}

func signedSnacksGet(t *testing.T, baseURL, signPath, rawQuery string, now time.Time) (*http.Response, snacksEnvelope) {
	t.Helper()

	headers, err := snacks.SignatureHeaders("ak_test", "testsecret", http.MethodGet, signPath, now)
	if err != nil {
		t.Fatalf("SignatureHeaders: %v", err)
	}

	requestURL := baseURL + signPath
	if rawQuery != "" {
		requestURL += "?" + rawQuery
	}

	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("leer cuerpo: %v", err)
	}

	var env snacksEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("el cuerpo no es el sobre esperado: %v (%s)", err, body)
	}
	return resp, env
}

func TestSnacksDevicePageRequiresPageAndPageSize(t *testing.T) {
	srv := httptest.NewServer(NewSnacks("ak_test", "testsecret"))
	defer srv.Close()

	resp, env := signedSnacksGet(t, srv.URL, snacksDevicesPath, "", time.Now())
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 sin Page ni PageSize", resp.StatusCode)
	}
	if env.Code != 400 {
		t.Fatalf("code = %d, want 400", env.Code)
	}
}

func TestSnacksDevicePageReturnsThePaginatedDevice(t *testing.T) {
	fake := NewSnacks("ak_test", "testsecret")
	srv := httptest.NewServer(fake)
	defer srv.Close()

	resp, env := signedSnacksGet(t, srv.URL, snacksDevicesPath, "Page=1&PageSize=10", time.Now())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, env.Message)
	}
	if env.Code != 200 {
		t.Fatalf("code = %d, want 200", env.Code)
	}

	var page snacksDevicePage
	if err := json.Unmarshal(env.Result, &page); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if page.Page != 1 || page.PageSize != 10 {
		t.Fatalf("page/pageSize = %d/%d, want 1/10", page.Page, page.PageSize)
	}
	if page.Total != 1 || page.TotalPages != 1 {
		t.Fatalf("total/totalPages = %d/%d, want 1/1", page.Total, page.TotalPages)
	}
	if page.HasPrevPage || page.HasNextPage {
		t.Fatalf("hasPrevPage/hasNextPage = %v/%v, want false/false", page.HasPrevPage, page.HasNextPage)
	}
	if len(page.Items) != 1 {
		t.Fatalf("items tiene %d elementos, want 1", len(page.Items))
	}

	device := page.Items[0]
	if device.DeviceCode == "" || device.DeviceName == "" || device.DeviceTypeName == "" {
		t.Fatalf("el item llegó sin identificadores: %+v", device)
	}
	if device.OnlineStatus != 1 || device.SellStatus != 1 || device.FaultState != 0 || device.LockType != 0 || device.StockoutState != 0 {
		t.Fatalf("estados por defecto = %+v, want 1/1/0/0/0", device)
	}
}

func TestSnacksDevicePageRejectsASignatureOverTheQueryString(t *testing.T) {
	srv := httptest.NewServer(NewSnacks("ak_test", "testsecret"))
	defer srv.Close()

	resp, env := signedSnacksGet(t, srv.URL, snacksDevicesPath+"?Page=1&PageSize=10", "Page=1&PageSize=10", time.Now())
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: la firma cubre sólo la ruta", resp.StatusCode)
	}
	if env.Code != 40004 {
		t.Fatalf("code = %d, want 40004", env.Code)
	}
}

func TestSnacksDevicePageRejectsAReplayedNonce(t *testing.T) {
	srv := httptest.NewServer(NewSnacks("ak_test", "testsecret"))
	defer srv.Close()

	now := time.Now()
	headers, err := snacks.SignatureHeaders("ak_test", "testsecret", http.MethodGet, snacksDevicesPath, now)
	if err != nil {
		t.Fatalf("SignatureHeaders: %v", err)
	}

	send := func() (*http.Response, snacksEnvelope) {
		req, err := http.NewRequest(http.MethodGet, srv.URL+snacksDevicesPath+"?Page=1&PageSize=10", nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do: %v", err)
		}
		defer resp.Body.Close()

		var env snacksEnvelope
		if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return resp, env
	}

	if resp, env := send(); resp.StatusCode != http.StatusOK {
		t.Fatalf("primer GET status = %d, want 200 (%s)", resp.StatusCode, env.Message)
	}

	resp, env := send()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("GET repetido status = %d, want 400", resp.StatusCode)
	}
	if env.Code != 40005 {
		t.Fatalf("code = %d, want 40005 (nonce repetido)", env.Code)
	}
}

func TestSnacksDevicePageReflectsTheForcedStates(t *testing.T) {
	fake := NewSnacks("ak_test", "testsecret")
	srv := httptest.NewServer(fake)
	defer srv.Close()

	fake.SetDeviceOnline(false)
	fake.SetSellStatus(snacks.SellStatusSuspended)
	fake.SetFaultState(snacks.FaultStateFault)
	fake.SetLockType(2)
	fake.SetStockoutState(snacks.StockoutStateOutOfStock)

	resp, env := signedSnacksGet(t, srv.URL, snacksDevicesPath, "Page=1&PageSize=10", time.Now())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, env.Message)
	}

	var page snacksDevicePage
	if err := json.Unmarshal(env.Result, &page); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("items tiene %d elementos, want 1", len(page.Items))
	}

	device := page.Items[0]
	if device.OnlineStatus != 0 || device.SellStatus != 2 || device.FaultState != 1 || device.LockType != 2 || device.StockoutState != 1 {
		t.Fatalf("estados = %+v, want 0/2/1/2/1", device)
	}
}

func TestSnacksDevicePageFiltersByDeviceCode(t *testing.T) {
	fake := NewSnacks("ak_test", "testsecret")
	srv := httptest.NewServer(fake)
	defer srv.Close()

	resp, env := signedSnacksGet(t, srv.URL, snacksDevicesPath, "Page=1&PageSize=10&DeviceCode=otra-maquina", time.Now())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, env.Message)
	}

	var page snacksDevicePage
	if err := json.Unmarshal(env.Result, &page); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("total/items = %d/%d, want 0/0 para un DeviceCode que no existe", page.Total, len(page.Items))
	}
}

func TestSnacksSlotListReturnsAnArrayWithStock(t *testing.T) {
	srv := httptest.NewServer(NewSnacks("ak_test", "testsecret"))
	defer srv.Close()

	resp, env := signedSnacksGet(t, srv.URL, snacksSlotsPath, "", time.Now())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, env.Message)
	}

	var slots snacksSlotList
	if err := json.Unmarshal(env.Result, &slots); err != nil {
		t.Fatalf("result no es una lista: %v", err)
	}
	if len(slots) < 2 {
		t.Fatalf("la lista tiene %d canales, want al menos 2", len(slots))
	}
	for _, slot := range slots {
		if slot.SlotNo == "" || slot.ProductID == "" {
			t.Fatalf("canal sin identificadores: %+v", slot)
		}
		if slot.Stock <= 0 {
			t.Fatalf("canal %q con stock %d, want stock", slot.SlotNo, slot.Stock)
		}
		if slot.SlotStatus != 1 {
			t.Fatalf("canal %q con slotStatus %d, want 1", slot.SlotNo, slot.SlotStatus)
		}
	}
}

func TestSnacksSlotListReflectsStockAndStatusSetters(t *testing.T) {
	fake := NewSnacks("ak_test", "testsecret")
	srv := httptest.NewServer(fake)
	defer srv.Close()

	fake.SetSlotStock("1", 0)
	fake.SetSlotStatus("2", snacks.SlotStatusDisabled)

	resp, env := signedSnacksGet(t, srv.URL, snacksSlotsPath, "", time.Now())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, env.Message)
	}

	var slots snacksSlotList
	if err := json.Unmarshal(env.Result, &slots); err != nil {
		t.Fatalf("result no es una lista: %v", err)
	}
	if len(slots) < 2 {
		t.Fatalf("la lista tiene %d canales, want al menos 2", len(slots))
	}
	if slots[0].Stock != 0 {
		t.Fatalf("slots[0].Stock = %d, want 0", slots[0].Stock)
	}
	if slots[1].SlotStatus != 3 {
		t.Fatalf("slots[1].SlotStatus = %d, want 3", slots[1].SlotStatus)
	}
}

func TestSnacksSlotListFiltersByDeviceCode(t *testing.T) {
	srv := httptest.NewServer(NewSnacks("ak_test", "testsecret"))
	defer srv.Close()

	resp, env := signedSnacksGet(t, srv.URL, snacksSlotsPath, "DeviceCode=otra-maquina", time.Now())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, env.Message)
	}

	var slots snacksSlotList
	if err := json.Unmarshal(env.Result, &slots); err != nil {
		t.Fatalf("result no es una lista: %v", err)
	}
	if len(slots) != 0 {
		t.Fatalf("la lista tiene %d canales, want 0", len(slots))
	}
}

type snacksOrderPage struct {
	Page        int  `json:"page"`
	PageSize    int  `json:"pageSize"`
	Total       int  `json:"total"`
	TotalPages  int  `json:"totalPages"`
	HasPrevPage bool `json:"hasPrevPage"`
	HasNextPage bool `json:"hasNextPage"`
	Items       []struct {
		OrderNo            string  `json:"orderNo"`
		TransactionOrderNo string  `json:"transactionOrderNo"`
		DeviceCode         string  `json:"deviceCode"`
		OrderStatus        int     `json:"orderStatus"`
		OrderStatusName    string  `json:"orderStatusName"`
		OrderAmount        float64 `json:"orderAmount"`
		PayAmount          float64 `json:"payAmount"`
		PayTime            string  `json:"payTime"`
		OrderCreatedTime   string  `json:"orderCreatedTime"`
	} `json:"items"`
}

func shipOneSnack(t *testing.T, baseURL, paymentOrderID, mid string) {
	t.Helper()

	client := snacks.NewClient(baseURL, "ak_test", "testsecret")

	if _, err := client.Ship(context.Background(), snacks.ShipmentRequest{
		PaymentOrderID: paymentOrderID,
		MID:            mid,
		Products: []snacks.ShipmentProduct{
			{OrderProductID: "line_1", ProductID: "prod_snack_1", RetailPrice: 15.5},
		},
	}); err != nil {
		t.Fatalf("Ship: %v", err)
	}
}

func readOrderPage(t *testing.T, baseURL, rawQuery string) snacksOrderPage {
	t.Helper()

	resp, env := signedSnacksGet(t, baseURL, snacksOrdersPath, rawQuery, time.Now())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, env.Message)
	}
	if env.Code != 200 {
		t.Fatalf("code = %d, want 200", env.Code)
	}

	var page snacksOrderPage
	if err := json.Unmarshal(env.Result, &page); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	return page
}

func TestSnacksOrderPageRequiresPageAndPageSize(t *testing.T) {
	srv := httptest.NewServer(NewSnacks("ak_test", "testsecret"))
	defer srv.Close()

	resp, env := signedSnacksGet(t, srv.URL, snacksOrdersPath, "", time.Now())
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 sin Page ni PageSize", resp.StatusCode)
	}
	if env.Code != 400 {
		t.Fatalf("code = %d, want 400", env.Code)
	}
}

func TestSnacksOrderPageListsTheRecordedShipment(t *testing.T) {
	fake := NewSnacks("ak_test", "testsecret")
	srv := httptest.NewServer(fake)
	defer srv.Close()

	shipOneSnack(t, srv.URL, "po_1", "mid-1")

	page := readOrderPage(t, srv.URL, "Page=1&PageSize=10")
	if page.Page != 1 || page.PageSize != 10 {
		t.Fatalf("page/pageSize = %d/%d, want 1/10", page.Page, page.PageSize)
	}
	if page.Total != 1 || page.TotalPages != 1 {
		t.Fatalf("total/totalPages = %d/%d, want 1/1", page.Total, page.TotalPages)
	}
	if len(page.Items) != 1 {
		t.Fatalf("items tiene %d elementos, want 1", len(page.Items))
	}

	order := page.Items[0]
	if order.OrderNo == "" {
		t.Fatalf("el pedido llegó sin orderNo: %+v", order)
	}
	if order.TransactionOrderNo != "po_1" {
		t.Fatalf("transactionOrderNo = %q, want po_1 (el PaymentOrderId despachado)", order.TransactionOrderNo)
	}
	if order.DeviceCode != "mid-1" {
		t.Fatalf("deviceCode = %q, want mid-1 (el Mid despachado)", order.DeviceCode)
	}
	if order.OrderAmount != 15.5 || order.PayAmount != 15.5 {
		t.Fatalf("orderAmount/payAmount = %v/%v, want 15.5/15.5", order.OrderAmount, order.PayAmount)
	}
	if order.PayTime == "" || order.OrderCreatedTime == "" {
		t.Fatalf("el pedido llegó sin fechas: %+v", order)
	}
}

func TestSnacksOrderPageFiltersByDeviceCodeAndOrderNo(t *testing.T) {
	fake := NewSnacks("ak_test", "testsecret")
	srv := httptest.NewServer(fake)
	defer srv.Close()

	shipOneSnack(t, srv.URL, "po_1", "mid-1")

	if page := readOrderPage(t, srv.URL, "Page=1&PageSize=10&DeviceCode=mid-1"); len(page.Items) != 1 {
		t.Fatalf("items con el DeviceCode correcto = %d, want 1", len(page.Items))
	}
	if page := readOrderPage(t, srv.URL, "Page=1&PageSize=10&DeviceCode=otra-maquina"); page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("items con otro DeviceCode = %d, want 0", len(page.Items))
	}
	if page := readOrderPage(t, srv.URL, "Page=1&PageSize=10&OrderNo=no-existe"); page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("items con otro OrderNo = %d, want 0", len(page.Items))
	}
}

func TestSnacksOrderPageReflectsTheForcedStatus(t *testing.T) {
	fake := NewSnacks("ak_test", "testsecret")
	srv := httptest.NewServer(fake)
	defer srv.Close()

	shipOneSnack(t, srv.URL, "po_1", "mid-1")
	fake.SetOrderStatus(10, "pagado")

	page := readOrderPage(t, srv.URL, "Page=1&PageSize=10")
	if len(page.Items) != 1 {
		t.Fatalf("items tiene %d elementos, want 1", len(page.Items))
	}
	if page.Items[0].OrderStatus != 10 || page.Items[0].OrderStatusName != "pagado" {
		t.Fatalf("orderStatus/orderStatusName = %d/%q, want 10/pagado", page.Items[0].OrderStatus, page.Items[0].OrderStatusName)
	}
}

func TestSnacksOrderPageCanBeForcedEmpty(t *testing.T) {
	fake := NewSnacks("ak_test", "testsecret")
	srv := httptest.NewServer(fake)
	defer srv.Close()

	shipOneSnack(t, srv.URL, "po_1", "mid-1")
	if page := readOrderPage(t, srv.URL, "Page=1&PageSize=10"); len(page.Items) != 1 {
		t.Fatalf("items antes de vaciar = %d, want 1", len(page.Items))
	}

	fake.SetOrdersEmpty(true)

	page := readOrderPage(t, srv.URL, "Page=1&PageSize=10")
	if page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("total/items = %d/%d, want 0/0 con la página forzada vacía", page.Total, len(page.Items))
	}
}

func TestSnacksOrderPageRejectsAReplayedNonce(t *testing.T) {
	srv := httptest.NewServer(NewSnacks("ak_test", "testsecret"))
	defer srv.Close()

	headers, err := snacks.SignatureHeaders("ak_test", "testsecret", http.MethodGet, snacksOrdersPath, time.Now())
	if err != nil {
		t.Fatalf("SignatureHeaders: %v", err)
	}

	send := func() (*http.Response, snacksEnvelope) {
		req, err := http.NewRequest(http.MethodGet, srv.URL+snacksOrdersPath+"?Page=1&PageSize=10", nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do: %v", err)
		}
		defer resp.Body.Close()

		var env snacksEnvelope
		if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return resp, env
	}

	if resp, env := send(); resp.StatusCode != http.StatusOK {
		t.Fatalf("primer GET status = %d, want 200 (%s)", resp.StatusCode, env.Message)
	}

	resp, env := send()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("GET repetido status = %d, want 400", resp.StatusCode)
	}
	if env.Code != 40005 {
		t.Fatalf("code = %d, want 40005 (nonce repetido)", env.Code)
	}
}
