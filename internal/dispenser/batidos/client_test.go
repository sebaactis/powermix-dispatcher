package batidos_test

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

	"compras-remotas/internal/dispenser/batidos"
	"compras-remotas/internal/fakeapi"
)

const (
	testKey    = "mykey"
	testSecret = "mysecret"

	campaignPath = "/coffee/newThird/active/v2/submit"
)

var fixedNow = time.Date(2026, 3, 25, 8, 30, 42, 0, time.UTC)

func newTestClient(t *testing.T, baseURL string) *batidos.Client {
	t.Helper()

	return batidos.NewClient(baseURL, testKey, testSecret, batidos.WithClock(func() time.Time {
		return fixedNow
	}))
}

func TestClientCreatesCampaignAndFollowsTheDrinkUntilCompleted(t *testing.T) {
	fake := fakeapi.NewBatidos(testKey, testSecret)
	srv := httptest.NewServer(fake)
	defer srv.Close()

	client := newTestClient(t, srv.URL)
	ctx := context.Background()

	campaign, err := client.CreateCampaign(ctx, batidos.CampaignRequest{
		ActiveName:   "combo test",
		Start:        fixedNow,
		End:          fixedNow.Add(5 * time.Minute),
		DeviceTypeID: "dt-1",
		DeviceIDs:    []string{"dev-1"},
		ExchangeType: 1,
		GoodsIDs:     []string{"goods-1"},
		NotifyURL:    "https://example.test/callback",
	})
	if err != nil {
		t.Fatalf("CreateCampaign: %v", err)
	}
	if len(campaign.ExchangeCodes) != 1 {
		t.Fatalf("códigos de canje = %v, se esperaba exactamente uno", campaign.ExchangeCodes)
	}
	if campaign.ActiveNo == "" {
		t.Fatalf("la campaña volvió sin activeNo: %+v", campaign)
	}
	code := campaign.ExchangeCodes[0]

	attempt, err := client.ProduceCoffee(ctx, batidos.ProduceRequest{
		Code: code, DeviceID: "dev-1", GoodsID: "goods-1", DeviceNo: "devno-1",
	})
	if err != nil {
		t.Fatalf("ProduceCoffee: %v", err)
	}
	if !attempt.Accepted {
		t.Fatalf("el pedido no fue aceptado: %+v", attempt)
	}

	info, err := client.OrderInfo(ctx, code, "dev-1")
	if err != nil {
		t.Fatalf("OrderInfo (in progress): %v", err)
	}
	if info.Status != batidos.OrderStatusInProgress {
		t.Fatalf("status = %d, se esperaba %d (en preparación)", info.Status, batidos.OrderStatusInProgress)
	}
	if info.OrderNo == "" {
		t.Fatalf("getOrderInfo volvió sin orderNo: %+v", info)
	}

	fake.Advance(code, 2)

	info, err = client.OrderInfo(ctx, code, "dev-1")
	if err != nil {
		t.Fatalf("OrderInfo (completed): %v", err)
	}
	if info.Status != batidos.OrderStatusCompleted {
		t.Fatalf("status = %d, se esperaba %d (completado)", info.Status, batidos.OrderStatusCompleted)
	}
	if info.DeviceNo != "devno-1" {
		t.Fatalf("deviceNo = %q, se esperaba %q", info.DeviceNo, "devno-1")
	}
}

func TestClientSurfacesReusedExchangeCodeAsAPIError(t *testing.T) {
	fake := fakeapi.NewBatidos(testKey, testSecret)
	srv := httptest.NewServer(fake)
	defer srv.Close()

	client := newTestClient(t, srv.URL)
	ctx := context.Background()

	campaign, err := client.CreateCampaign(ctx, batidos.CampaignRequest{
		ActiveName:   "combo test",
		Start:        fixedNow,
		End:          fixedNow.Add(5 * time.Minute),
		DeviceTypeID: "dt-1",
		DeviceIDs:    []string{"dev-1"},
		ExchangeType: 1,
		GoodsIDs:     []string{"goods-1"},
	})
	if err != nil {
		t.Fatalf("CreateCampaign: %v", err)
	}
	req := batidos.ProduceRequest{
		Code: campaign.ExchangeCodes[0], DeviceID: "dev-1", GoodsID: "goods-1", DeviceNo: "devno-1",
	}

	if _, err := client.ProduceCoffee(ctx, req); err != nil {
		t.Fatalf("primer ProduceCoffee: %v", err)
	}

	attempt, err := client.ProduceCoffee(ctx, req)
	if err == nil {
		t.Fatalf("el segundo canje con el mismo código fue aceptado: %+v", attempt)
	}

	var apiErr *batidos.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("el error %v (%T) no es un *batidos.APIError", err, err)
	}
	if !strings.Contains(apiErr.Message, "ya fue utilizado") {
		t.Fatalf("APIError.Message = %q, se esperaba el mensaje del proveedor", apiErr.Message)
	}
	if !strings.Contains(err.Error(), "ya fue utilizado") {
		t.Fatalf("el mensaje del proveedor no llega al texto del error: %q", err.Error())
	}
}

func TestClientSendsCampaignTimesInChinaTime(t *testing.T) {
	var (
		body        []byte
		contentType string
		timestamp   string
		keyMD5      string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		contentType = r.Header.Get("Content-Type")
		timestamp = r.Header.Get("timestamp")
		keyMD5 = r.Header.Get("key-md5")

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":200,"success":true,"msg":"操作成功","data":{"exchangeCodes":["000001"],"activeInfo":{"activeNo":"AD000001","activeName":"combo test","startTime":"2026-03-25 16:30:42","endTime":"2026-03-25 16:35:42","exchangeType":1}}}`)
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv.URL).CreateCampaign(context.Background(), batidos.CampaignRequest{
		ActiveName:   "combo test",
		Start:        fixedNow,
		End:          fixedNow.Add(5 * time.Minute),
		DeviceTypeID: "dt-1",
		DeviceIDs:    []string{"dev-1"},
		ExchangeType: 1,
		GoodsIDs:     []string{"goods-1"},
	})
	if err != nil {
		t.Fatalf("CreateCampaign: %v", err)
	}

	var sent struct {
		ActiveName    string   `json:"activeName"`
		StartTime     string   `json:"startTime"`
		EndTime       string   `json:"endTime"`
		ExchangeType  int      `json:"exchangeType"`
		DeviceTypeID  string   `json:"deviceTypeId"`
		DeviceIDs     []string `json:"deviceIds"`
		GoodsItemDtos []struct {
			GoodsID string `json:"goodsId"`
		} `json:"goodsItemDtos"`
	}
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("el cuerpo enviado no es JSON (%q): %v", body, err)
	}

	if sent.StartTime != "2026-03-25 16:30:42" {
		t.Fatalf("startTime = %q, se esperaba %q (UTC+8, no hora local)", sent.StartTime, "2026-03-25 16:30:42")
	}
	if sent.EndTime != "2026-03-25 16:35:42" {
		t.Fatalf("endTime = %q, se esperaba %q (UTC+8, no hora local)", sent.EndTime, "2026-03-25 16:35:42")
	}

	if sent.ActiveName != "combo test" || sent.DeviceTypeID != "dt-1" || sent.ExchangeType != 1 {
		t.Fatalf("cuerpo inesperado: %+v", sent)
	}
	if len(sent.DeviceIDs) != 1 || sent.DeviceIDs[0] != "dev-1" {
		t.Fatalf("deviceIds = %v, se esperaba [dev-1]", sent.DeviceIDs)
	}
	if len(sent.GoodsItemDtos) != 1 || sent.GoodsItemDtos[0].GoodsID != "goods-1" {
		t.Fatalf("goodsItemDtos = %+v, se esperaba un único goods-1", sent.GoodsItemDtos)
	}

	if contentType != "application/json" {
		t.Fatalf("Content-Type = %q, se esperaba application/json", contentType)
	}
	if timestamp != "1774427442000" {
		t.Fatalf("timestamp = %q, se esperaba el reloj inyectado 1774427442000", timestamp)
	}
	if want := batidos.KeyMD5(testKey, testSecret, "1774427442000"); keyMD5 != want {
		t.Fatalf("key-md5 = %q, se esperaba %q", keyMD5, want)
	}
}

func TestClientTreatsNon200EnvelopeCodeAsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":400,"success":false,"msg":"código de canje desconocido","data":null}`)
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv.URL).OrderInfo(context.Background(), "000001", "dev-1")
	if err == nil {
		t.Fatalf("un code distinto de 200 se devolvió como éxito")
	}

	var apiErr *batidos.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("el error %v (%T) no es un *batidos.APIError", err, err)
	}
	if apiErr.Code != 400 || apiErr.HTTPStatus != http.StatusOK {
		t.Fatalf("APIError = %+v, se esperaba code 400 sobre un http 200", apiErr)
	}
}

func TestClientReportsNon200HTTPStatusAsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"code":500,"success":false,"msg":"el proveedor está caído","data":null}`)
	}))
	defer srv.Close()

	info, err := newTestClient(t, srv.URL).OrderInfo(context.Background(), "000001", "dev-1")
	if err == nil {
		t.Fatalf("un HTTP 500 se devolvió como éxito: %+v", info)
	}

	var apiErr *batidos.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("el error %v (%T) no es un *batidos.APIError", err, err)
	}
	if apiErr.HTTPStatus != http.StatusInternalServerError {
		t.Fatalf("HTTPStatus = %d, se esperaba 500", apiErr.HTTPStatus)
	}
	if apiErr.Code != 500 {
		t.Fatalf("Code = %d, se esperaba 500", apiErr.Code)
	}
	if !strings.Contains(apiErr.Message, "caído") {
		t.Fatalf("APIError.Message = %q, se esperaba el mensaje del proveedor", apiErr.Message)
	}
}

func TestClientRespectsContextCancellation(t *testing.T) {

	unblock := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-unblock:
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if _, err := newTestClient(t, srv.URL).OrderInfo(ctx, "000001", "dev-1"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, se esperaba context.DeadlineExceeded", err)
	}
	close(unblock)
}
