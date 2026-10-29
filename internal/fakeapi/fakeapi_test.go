package fakeapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"compras-remotas/internal/dispenser/batidos"
	"compras-remotas/internal/dispenser/snacks"
)

type batidosEnvelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

func TestBatidosRejectsMissingSignature(t *testing.T) {
	srv := httptest.NewServer(NewBatidos("mykey", "mysecret"))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/coffee/newThird/active/v2/submit", "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestBatidosHappyPathFromCampaignToBrewedDrink(t *testing.T) {
	fake := NewBatidos("mykey", "mysecret")
	srv := httptest.NewServer(fake)
	defer srv.Close()

	now := time.Now()

	// 1. Crear la campaña devuelve un código de canje.
	code := submitCampaign(t, srv.URL, now)

	// 2. Canjear el código acepta el pedido.
	brew := postJSON(t, srv.URL+"/coffee/newThird/order/produceCoffee", now, map[string]string{
		"code": code, "deviceId": "dev-1", "goodsId": "goods-1", "deviceNo": "devno-1",
	})
	if brew.Code != 200 {
		t.Fatalf("produceCoffee code = %d, want 200", brew.Code)
	}

	// 3. Consultar reporta "preparando".
	if status := orderStatus(t, srv.URL, now, code); status != 1 {
		t.Fatalf("status = %d, want 1 (in progress)", status)
	}

	// 4. El callback avanza a "completado".
	fake.Advance(code, 2)

	if status := orderStatus(t, srv.URL, now, code); status != 2 {
		t.Fatalf("status = %d, want 2 (completed)", status)
	}
}

func TestBatidosRejectsReusedExchangeCode(t *testing.T) {
	srv := httptest.NewServer(NewBatidos("mykey", "mysecret"))
	defer srv.Close()

	now := time.Now()
	code := submitCampaign(t, srv.URL, now)

	body := map[string]string{"code": code, "deviceId": "dev-1", "goodsId": "goods-1", "deviceNo": "devno-1"}
	if resp := postJSON(t, srv.URL+"/coffee/newThird/order/produceCoffee", now, body); resp.Code != 200 {
		t.Fatalf("first brew code = %d, want 200", resp.Code)
	}
	if resp := postJSON(t, srv.URL+"/coffee/newThird/order/produceCoffee", now, body); resp.Code == 200 {
		t.Fatalf("second brew with the same code was accepted; a code must be single use")
	}
}

func TestSnacksRejectsBadSignature(t *testing.T) {
	srv := httptest.NewServer(NewSnacks("ak_test", "testsecret"))
	defer srv.Close()

	now := time.Now()
	headers, err := snacks.SignatureHeaders("ak_test", "wrongsecret", "POST", "/open/v7/device/remote/shipment", now)
	if err != nil {
		t.Fatalf("SignatureHeaders: %v", err)
	}

	req := shipmentRequest(t, srv.URL, headers)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestSnacksRejectsReplayedNonce(t *testing.T) {
	srv := httptest.NewServer(NewSnacks("ak_test", "testsecret"))
	defer srv.Close()

	now := time.Now()
	headers, err := snacks.SignatureHeaders("ak_test", "testsecret", "POST", "/open/v7/device/remote/shipment", now)
	if err != nil {
		t.Fatalf("SignatureHeaders: %v", err)
	}

	first, err := http.DefaultClient.Do(shipmentRequest(t, srv.URL, headers))
	if err != nil {
		t.Fatalf("first do: %v", err)
	}
	defer first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first status = %d, want 200", first.StatusCode)
	}

	second, err := http.DefaultClient.Do(shipmentRequest(t, srv.URL, headers))
	if err != nil {
		t.Fatalf("second do: %v", err)
	}
	defer second.Body.Close()

	if second.StatusCode != http.StatusBadRequest {
		t.Fatalf("replayed nonce status = %d, want 400", second.StatusCode)
	}
}

func TestSnacksRecordsShipment(t *testing.T) {
	fake := NewSnacks("ak_test", "testsecret")
	srv := httptest.NewServer(fake)
	defer srv.Close()

	now := time.Now()
	headers, err := snacks.SignatureHeaders("ak_test", "testsecret", "POST", "/open/v7/device/remote/shipment", now)
	if err != nil {
		t.Fatalf("SignatureHeaders: %v", err)
	}

	resp, err := http.DefaultClient.Do(shipmentRequest(t, srv.URL, headers))
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := fake.ShipmentCount("po_1"); got != 1 {
		t.Fatalf("recorded shipments = %d, want 1", got)
	}
}

func submitCampaign(t *testing.T, baseURL string, now time.Time) string {
	t.Helper()

	body := map[string]any{
		"activeName":    "combo test",
		"startTime":     batidos.CampaignTime(now),
		"endTime":       batidos.CampaignTime(now.Add(5 * time.Minute)),
		"exchangeType":  1,
		"deviceTypeId":  "dt-1",
		"goodsItemDtos": []map[string]string{{"goodsId": "goods-1"}},
	}

	resp := postJSON(t, baseURL+"/coffee/newThird/active/v2/submit", now, body)
	if resp.Code != 200 {
		t.Fatalf("campaign code = %d, want 200 (%s)", resp.Code, resp.Msg)
	}

	var data struct {
		ExchangeCodes []string `json:"exchangeCodes"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("decode campaign data: %v", err)
	}
	if len(data.ExchangeCodes) != 1 {
		t.Fatalf("exchangeCodes = %v, want exactly one", data.ExchangeCodes)
	}
	return data.ExchangeCodes[0]
}

func orderStatus(t *testing.T, baseURL string, now time.Time, code string) int {
	t.Helper()

	resp := postJSON(t, baseURL+"/coffee/newThird/order/getOrderInfo", now, map[string]string{
		"code": code, "deviceId": "dev-1",
	})
	if resp.Code != 200 {
		t.Fatalf("getOrderInfo code = %d, want 200", resp.Code)
	}

	var data struct {
		Status int `json:"status"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("decode order data: %v", err)
	}
	return data.Status
}

func postJSON(t *testing.T, url string, now time.Time, payload any) batidosEnvelope {
	t.Helper()

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range batidos.SignatureHeaders("mykey", "mysecret", now) {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	var env batidosEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope (status %d): %v", resp.StatusCode, err)
	}
	return env
}

func shipmentRequest(t *testing.T, baseURL string, headers map[string]string) *http.Request {
	t.Helper()

	body := map[string]any{
		"PaymentOrderId": "po_1",
		"Mid":            "mid-1",
		"DeviceRemoteShipmentProducts": []map[string]any{
			{"OrderProductId": "line-1", "ProductId": "prod-1", "ItemPricing": map[string]float64{"retailPrice": 0.01}},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, baseURL+snacksShipmentPath, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}
