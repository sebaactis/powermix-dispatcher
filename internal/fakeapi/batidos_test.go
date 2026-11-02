package fakeapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"compras-remotas/internal/dispenser/batidos"
)

type batidosDeviceData struct {
	ID              string `json:"id"`
	DeviceNo        string `json:"deviceNo"`
	Name            string `json:"name"`
	DeviceTypeID    string `json:"deviceTypeId"`
	Status          int    `json:"status"`
	DeviceRunStatus int    `json:"deviceRunStatus"`
	ScarceStatus    int    `json:"scarceStatus"`
}

type batidosGoodsData struct {
	ID      string  `json:"id"`
	No      string  `json:"no"`
	Name    string  `json:"name"`
	Price   float64 `json:"price"`
	SoldOut int     `json:"soldOut"`
	Listing bool    `json:"listing"`
}

func TestBatidosDeviceDetailDefaultsToOnlineAndNormal(t *testing.T) {
	fake := NewBatidos("mykey", "mysecret")
	srv := httptest.NewServer(fake)
	defer srv.Close()

	device := getBatidosDevice(t, srv.URL, "dev-1", time.Now())
	if device.ID != "dev-1" {
		t.Fatalf("id = %q, want dev-1", device.ID)
	}
	if device.DeviceNo == "" || device.Name == "" || device.DeviceTypeID == "" {
		t.Fatalf("device identity is incomplete: %+v", device)
	}
	if device.Status != 1 || device.DeviceRunStatus != 1 || device.ScarceStatus != 1 {
		t.Fatalf("device = %+v, want status 1, deviceRunStatus 1, scarceStatus 1", device)
	}
}

func TestBatidosDeviceDetailReportsForcedStates(t *testing.T) {
	fake := NewBatidos("mykey", "mysecret")
	fake.SetDeviceOffline("dev-1")
	fake.SetDeviceAbnormal("dev-1")
	fake.SetDeviceOutOfStock("dev-1")
	srv := httptest.NewServer(fake)
	defer srv.Close()

	device := getBatidosDevice(t, srv.URL, "dev-1", time.Now())
	if device.Status != 2 {
		t.Fatalf("status = %d, want 2 (offline)", device.Status)
	}
	if device.DeviceRunStatus != 2 {
		t.Fatalf("deviceRunStatus = %d, want 2 (abnormal)", device.DeviceRunStatus)
	}
	if device.ScarceStatus != 3 {
		t.Fatalf("scarceStatus = %d, want 3 (out of stock)", device.ScarceStatus)
	}
}

func TestBatidosDeviceDetailKeepsStatesPerDevice(t *testing.T) {
	fake := NewBatidos("mykey", "mysecret")
	fake.SetDeviceOffline("dev-1")
	srv := httptest.NewServer(fake)
	defer srv.Close()

	now := time.Now()
	if device := getBatidosDevice(t, srv.URL, "dev-1", now); device.Status != 2 {
		t.Fatalf("dev-1 status = %d, want 2 (offline)", device.Status)
	}
	if device := getBatidosDevice(t, srv.URL, "dev-2", now); device.Status != 1 {
		t.Fatalf("dev-2 status = %d, want 1 (online): the fake must not share state between devices", device.Status)
	}
}

func TestBatidosDeviceDetailRejectsUnsignedGet(t *testing.T) {
	srv := httptest.NewServer(NewBatidos("mykey", "mysecret"))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/coffee/newThird/device/detail/dev-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestBatidosDeviceDetailRejectsPost(t *testing.T) {
	fake := NewBatidos("mykey", "mysecret")
	srv := httptest.NewServer(fake)
	defer srv.Close()

	env := postJSON(t, srv.URL+"/coffee/newThird/device/detail/dev-1", time.Now(), map[string]string{})
	if env.Code != 405 {
		t.Fatalf("code = %d, want 405: the provider documents this endpoint as GET", env.Code)
	}
}

func TestBatidosDeviceGoodsDetailReturnsEmptyCatalogueByDefault(t *testing.T) {
	srv := httptest.NewServer(NewBatidos("mykey", "mysecret"))
	defer srv.Close()

	if goods := getBatidosDeviceGoods(t, srv.URL, "dev-1", time.Now()); len(goods) != 0 {
		t.Fatalf("goods = %+v, want an empty catalogue", goods)
	}
}

func TestBatidosDeviceGoodsDetailReturnsConfiguredCatalogue(t *testing.T) {
	fake := NewBatidos("mykey", "mysecret")
	fake.SetDeviceGoods("dev-1", []batidos.DeviceGoods{
		{ID: "goods-1", No: "P202509301915100581", Name: "frutilla", Price: 4.9, SoldOut: 0, Listing: true},
		{ID: "goods-2", No: "P202603121557531449", Name: "chocolate", Price: 5.9, SoldOut: 1, Listing: false},
	})
	srv := httptest.NewServer(fake)
	defer srv.Close()

	goods := getBatidosDeviceGoods(t, srv.URL, "dev-1", time.Now())
	if len(goods) != 2 {
		t.Fatalf("goods = %+v, want 2 products", goods)
	}
	if goods[0].ID != "goods-1" || goods[0].No != "P202509301915100581" || goods[0].Price != 4.9 {
		t.Fatalf("first product = %+v, want the configured goods-1", goods[0])
	}
	if goods[1].SoldOut != 1 || goods[1].Listing {
		t.Fatalf("second product = %+v, want soldOut 1 and listing false", goods[1])
	}
}

func TestBatidosDeviceGoodsDetailIsNotSharedBetweenDevices(t *testing.T) {
	fake := NewBatidos("mykey", "mysecret")
	fake.SetDeviceGoods("dev-1", []batidos.DeviceGoods{
		{ID: "goods-1", No: "P1", Name: "frutilla", Price: 4.9, Listing: true},
	})
	srv := httptest.NewServer(fake)
	defer srv.Close()

	now := time.Now()
	if goods := getBatidosDeviceGoods(t, srv.URL, "dev-1", now); len(goods) != 1 {
		t.Fatalf("dev-1 goods = %+v, want 1 product", goods)
	}
	if goods := getBatidosDeviceGoods(t, srv.URL, "dev-2", now); len(goods) != 0 {
		t.Fatalf("dev-2 goods = %+v, want an empty catalogue", goods)
	}
}

func TestBatidosDeviceGoodsDetailRejectsUnsignedGet(t *testing.T) {
	srv := httptest.NewServer(NewBatidos("mykey", "mysecret"))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/coffee/newThird/goods/deviceGoodsDetail/dev-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func getBatidosDevice(t *testing.T, baseURL, deviceID string, now time.Time) batidosDeviceData {
	t.Helper()

	env := signedBatidosGet(t, baseURL+"/coffee/newThird/device/detail/"+deviceID, now)
	if env.Code != 200 {
		t.Fatalf("device detail code = %d, want 200 (%s)", env.Code, env.Msg)
	}

	var data batidosDeviceData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("decode device data: %v", err)
	}
	return data
}

func getBatidosDeviceGoods(t *testing.T, baseURL, deviceID string, now time.Time) []batidosGoodsData {
	t.Helper()

	env := signedBatidosGet(t, baseURL+"/coffee/newThird/goods/deviceGoodsDetail/"+deviceID, now)
	if env.Code != 200 {
		t.Fatalf("device goods detail code = %d, want 200 (%s)", env.Code, env.Msg)
	}

	var data []batidosGoodsData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("decode device goods data: %v", err)
	}
	return data
}

func signedBatidosGet(t *testing.T, url string, now time.Time) batidosEnvelope {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
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
