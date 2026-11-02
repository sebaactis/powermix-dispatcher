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

	campaignPath     = "/coffee/newThird/active/v2/submit"
	deviceDetailPath = "/coffee/newThird/device/detail/"
	deviceGoodsPath  = "/coffee/newThird/goods/deviceGoodsDetail/"
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

func TestClientReadsHealthyDevice(t *testing.T) {
	fake := fakeapi.NewBatidos(testKey, testSecret)
	srv := httptest.NewServer(fake)
	defer srv.Close()

	device, err := newTestClient(t, srv.URL).Device(context.Background(), "dev-1")
	if err != nil {
		t.Fatalf("Device: %v", err)
	}

	if device.ID != "dev-1" {
		t.Fatalf("id = %q, se esperaba %q", device.ID, "dev-1")
	}
	if device.Status != batidos.DeviceStatusOnline {
		t.Fatalf("status = %d, se esperaba %d (en línea)", device.Status, batidos.DeviceStatusOnline)
	}
	if device.DeviceRunStatus != batidos.DeviceRunStatusNormal {
		t.Fatalf("deviceRunStatus = %d, se esperaba %d (normal)", device.DeviceRunStatus, batidos.DeviceRunStatusNormal)
	}
	if device.ScarceStatus != batidos.ScarceStatusNormal {
		t.Fatalf("scarceStatus = %d, se esperaba %d (normal)", device.ScarceStatus, batidos.ScarceStatusNormal)
	}
	if device.Name == "" || device.DeviceNo == "" || device.DeviceTypeID == "" {
		t.Fatalf("la identidad del dispositivo quedó incompleta: %+v", device)
	}
}

func TestClientSurfacesOfflineAndFaultedDevice(t *testing.T) {
	fake := fakeapi.NewBatidos(testKey, testSecret)
	fake.SetDeviceOffline("dev-1")
	fake.SetDeviceAbnormal("dev-1")
	srv := httptest.NewServer(fake)
	defer srv.Close()

	device, err := newTestClient(t, srv.URL).Device(context.Background(), "dev-1")
	if err != nil {
		t.Fatalf("Device: %v", err)
	}

	if device.Status != batidos.DeviceStatusOffline {
		t.Fatalf("status = %d, se esperaba %d (fuera de línea)", device.Status, batidos.DeviceStatusOffline)
	}
	if device.DeviceRunStatus != batidos.DeviceRunStatusAbnormal {
		t.Fatalf("deviceRunStatus = %d, se esperaba %d (avería)", device.DeviceRunStatus, batidos.DeviceRunStatusAbnormal)
	}
}

func TestClientSurfacesOutOfStockDevice(t *testing.T) {
	fake := fakeapi.NewBatidos(testKey, testSecret)
	fake.SetDeviceOutOfStock("dev-1")
	srv := httptest.NewServer(fake)
	defer srv.Close()

	device, err := newTestClient(t, srv.URL).Device(context.Background(), "dev-1")
	if err != nil {
		t.Fatalf("Device: %v", err)
	}

	if device.ScarceStatus != batidos.ScarceStatusOutOfStock {
		t.Fatalf("scarceStatus = %d, se esperaba %d (sin stock)", device.ScarceStatus, batidos.ScarceStatusOutOfStock)
	}
}

func TestClientReadsDeviceGoodsCatalogue(t *testing.T) {
	fake := fakeapi.NewBatidos(testKey, testSecret)
	fake.SetDeviceGoods("dev-1", []batidos.DeviceGoods{
		{ID: "goods-1", No: "P202509301915100581", Name: "frutilla", Price: 4.9, SoldOut: 0, Listing: true},
		{ID: "goods-2", No: "P202603121557531449", Name: "chocolate", Price: 5.9, SoldOut: 1, Listing: true},
		{ID: "goods-3", No: "P202601010000000000", Name: "discontinuado", Price: 3.5, SoldOut: 0, Listing: false},
	})
	srv := httptest.NewServer(fake)
	defer srv.Close()

	goods, err := newTestClient(t, srv.URL).DeviceGoods(context.Background(), "dev-1")
	if err != nil {
		t.Fatalf("DeviceGoods: %v", err)
	}
	if len(goods) != 3 {
		t.Fatalf("catálogo = %d productos (%+v), se esperaban 3", len(goods), goods)
	}

	byID := make(map[string]batidos.DeviceGoods, len(goods))
	for _, item := range goods {
		byID[item.ID] = item
	}

	available, ok := byID["goods-1"]
	if !ok {
		t.Fatalf("el catálogo no trae goods-1: %+v", goods)
	}
	if available.SoldOut != 0 || !available.Listing {
		t.Fatalf("goods-1 = %+v, se esperaba en stock y publicado", available)
	}
	if available.Name != "frutilla" || available.No != "P202509301915100581" || available.Price != 4.9 {
		t.Fatalf("goods-1 = %+v, se esperaba el producto tal como lo informa el proveedor", available)
	}

	soldOut, ok := byID["goods-2"]
	if !ok {
		t.Fatalf("el catálogo no trae goods-2: %+v", goods)
	}
	if soldOut.SoldOut != 1 {
		t.Fatalf("goods-2 soldOut = %d, se esperaba 1 (agotado)", soldOut.SoldOut)
	}

	delisted, ok := byID["goods-3"]
	if !ok {
		t.Fatalf("el catálogo no trae goods-3: %+v", goods)
	}
	if delisted.Listing {
		t.Fatalf("goods-3 listing = true, se esperaba false (dado de baja)")
	}
}

func TestClientSignsGetRequests(t *testing.T) {
	fake := fakeapi.NewBatidos(testKey, testSecret)
	srv := httptest.NewServer(fake)
	defer srv.Close()

	for _, path := range []string{deviceDetailPath + "dev-1", deviceGoodsPath + "dev-1"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET sin firma a %s: %v", path, err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("GET sin firma a %s = %d, se esperaba %d", path, resp.StatusCode, http.StatusUnauthorized)
		}
	}

	client := newTestClient(t, srv.URL)
	ctx := context.Background()

	if _, err := client.Device(ctx, "dev-1"); err != nil {
		t.Fatalf("Device firmado: %v", err)
	}
	if _, err := client.DeviceGoods(ctx, "dev-1"); err != nil {
		t.Fatalf("DeviceGoods firmado: %v", err)
	}
}

func TestClientToleratesDocumentedStringStates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":200,"success":true,"msg":"操作成功","data":{"id":"dev-1","deviceNo":"adbafc62","name":"G01469","deviceTypeId":"dt-1","status":"2","deviceRunStatus":"2","scarceStatus":"3"}}`)
	}))
	defer srv.Close()

	device, err := newTestClient(t, srv.URL).Device(context.Background(), "dev-1")
	if err != nil {
		t.Fatalf("Device: %v", err)
	}
	if device.Status != batidos.DeviceStatusOffline || device.DeviceRunStatus != batidos.DeviceRunStatusAbnormal || device.ScarceStatus != batidos.ScarceStatusOutOfStock {
		t.Fatalf("device = %+v, se esperaban los estados 2/2/3 informados como texto", device)
	}
}

func TestClientReportsDeviceRejectionAsAPIError(t *testing.T) {
	fake := fakeapi.NewBatidos(testKey, testSecret)
	srv := httptest.NewServer(fake)
	defer srv.Close()

	device, err := newTestClient(t, srv.URL).Device(context.Background(), "")
	if err == nil {
		t.Fatalf("un deviceId vacío se devolvió como éxito: %+v", device)
	}

	var apiErr *batidos.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("el error %v (%T) no es un *batidos.APIError", err, err)
	}
	if !strings.Contains(apiErr.Message, "deviceId") {
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
