package fakeapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type adminFixture struct {
	batidos *Batidos
	snacks  *Snacks
	handler http.Handler
}

func newAdminFixture(t *testing.T) *adminFixture {
	t.Helper()

	batidosFake := NewBatidos("mykey", "mysecret")
	snacksFake := NewSnacks("ak_test", "testsecret")

	return &adminFixture{batidos: batidosFake, snacks: snacksFake, handler: NewAdmin(batidosFake, snacksFake)}
}

func (f *adminFixture) call(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(method, path, strings.NewReader(body))
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, request)

	return recorder
}

func (f *adminFixture) post(t *testing.T, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	return f.call(t, http.MethodPost, path, body)
}

func adminPayload(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("se esperaba JSON y llegó %q", contentType)
	}

	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("la respuesta no es un objeto JSON válido (%v): %s", err, recorder.Body.String())
	}

	return payload
}

func expectAdminOK(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()

	if recorder.Code != http.StatusOK {
		t.Fatalf("se esperaba 200 y llegó %d: %s", recorder.Code, recorder.Body.String())
	}

	if payload := adminPayload(t, recorder); payload["ok"] != true {
		t.Fatalf("la respuesta debía confirmar la operación: %v", payload)
	}
}

func expectAdminError(t *testing.T, recorder *httptest.ResponseRecorder, status int) {
	t.Helper()

	if recorder.Code != status {
		t.Fatalf("se esperaba %d y llegó %d: %s", status, recorder.Code, recorder.Body.String())
	}

	payload := adminPayload(t, recorder)
	if payload["ok"] != false {
		t.Fatalf("la respuesta debía declarar el fallo: %v", payload)
	}

	if message, _ := payload["error"].(string); message == "" {
		t.Fatalf("el fallo debía explicar el motivo: %v", payload)
	}
}

func TestAdminSetsTheBatidosGoods(t *testing.T) {
	fx := newAdminFixture(t)

	recorder := fx.post(t, adminBatidosGoodsPath, `{"device_id":"dev-1","goods":[{"id":"goods-1","listing":true,"sold_out":0},{"id":"goods-2","listing":false,"sold_out":3}]}`)
	expectAdminOK(t, recorder)

	goods := fx.batidos.goods["dev-1"]
	if len(goods) != 2 {
		t.Fatalf("el dispositivo quedó con %d productos, se esperaban 2: %+v", len(goods), goods)
	}

	if goods[0].ID != "goods-1" || !goods[0].Listing || goods[0].SoldOut != 0 {
		t.Fatalf("el primer producto se cargó mal: %+v", goods[0])
	}

	if goods[1].ID != "goods-2" || goods[1].Listing || goods[1].SoldOut != 3 {
		t.Fatalf("el segundo producto se cargó mal: %+v", goods[1])
	}
}

func TestAdminLeavesTheBatidosDeviceWithoutGoods(t *testing.T) {
	fx := newAdminFixture(t)
	fx.batidos.SetDeviceGoods("dev-1", nil)

	recorder := fx.post(t, adminBatidosGoodsPath, `{"device_id":"dev-1","goods":[]}`)
	expectAdminOK(t, recorder)

	if goods := fx.batidos.goods["dev-1"]; len(goods) != 0 {
		t.Fatalf("el array vacío debía dejar el dispositivo sin productos y quedó %+v", goods)
	}
}

func TestAdminRequiresTheDeviceID(t *testing.T) {
	fx := newAdminFixture(t)

	expectAdminError(t, fx.post(t, adminBatidosGoodsPath, `{"goods":[]}`), http.StatusBadRequest)
}

func TestAdminSetsTheBatidosDeviceOffline(t *testing.T) {
	fx := newAdminFixture(t)

	expectAdminOK(t, fx.post(t, adminBatidosOfflinePath, `{"device_id":"dev-1","offline":true}`))

	if state := fx.batidos.devices["dev-1"]; state == nil || !state.offline {
		t.Fatalf("el dispositivo debía quedar fuera de línea: %+v", state)
	}
}

func TestAdminAdvancesABatidosCode(t *testing.T) {
	fx := newAdminFixture(t)
	fx.batidos.codes["canje-1"] = &batidosCode{code: "canje-1", status: 1}

	expectAdminOK(t, fx.post(t, adminBatidosAdvancePath, `{"code":"canje-1","status":2}`))

	if code := fx.batidos.codes["canje-1"]; code.status != 2 {
		t.Fatalf("el canje quedó en el estado %d, se esperaba 2", code.status)
	}
}

func TestAdminSetsTheSnacksDevice(t *testing.T) {
	fx := newAdminFixture(t)

	expectAdminOK(t, fx.post(t, adminSnacksDevicePath, `{"online":false,"sell_status":0,"fault_state":1,"lock_type":2,"stockout_state":1}`))

	device := fx.snacks.device
	if device.onlineStatus != 0 || device.sellStatus != 0 || device.faultState != 1 || device.lockType != 2 || device.stockoutState != 1 {
		t.Fatalf("el dispositivo de snacks quedó %+v", device)
	}
}

func TestAdminSetsTheSnacksSlot(t *testing.T) {
	fx := newAdminFixture(t)

	expectAdminOK(t, fx.post(t, adminSnacksSlotPath, `{"slot_no":"2","stock":0,"slot_status":0}`))

	slot := fx.snacks.slots[1]
	if slot.slotNo != "2" {
		t.Fatalf("se tocó la ranura %q", slot.slotNo)
	}

	if slot.stock != 0 || slot.slotStatus != 0 {
		t.Fatalf("la ranura quedó con stock %d y estado %d", slot.stock, slot.slotStatus)
	}
}

func TestAdminSetsTheSnacksOrder(t *testing.T) {
	fx := newAdminFixture(t)

	expectAdminOK(t, fx.post(t, adminSnacksOrderPath, `{"status":0,"name":"falló la entrega"}`))

	if fx.snacks.orderStatus != 0 || fx.snacks.orderStatusName != "falló la entrega" {
		t.Fatalf("la orden de snacks quedó en %d con el nombre %q", fx.snacks.orderStatus, fx.snacks.orderStatusName)
	}
}

func TestAdminEmptiesTheSnacksOrderList(t *testing.T) {
	fx := newAdminFixture(t)

	expectAdminOK(t, fx.post(t, adminSnacksOrderPath, `{"empty":true}`))

	if !fx.snacks.ordersEmpty {
		t.Fatal("la lista de órdenes debía quedar vacía")
	}
}

func TestAdminRejectsAnUnknownField(t *testing.T) {
	fx := newAdminFixture(t)

	expectAdminError(t, fx.post(t, adminSnacksSlotPath, `{"slot_no":"1","stock":0,"atajo":true}`), http.StatusBadRequest)
}

func TestAdminRejectsAMethodThatIsNotPost(t *testing.T) {
	fx := newAdminFixture(t)

	expectAdminError(t, fx.call(t, http.MethodGet, adminBatidosGoodsPath, ""), http.StatusMethodNotAllowed)
}

func TestAdminRejectsAnUnknownRoute(t *testing.T) {
	fx := newAdminFixture(t)

	expectAdminError(t, fx.post(t, "/admin/nada", `{}`), http.StatusNotFound)
}

func TestAdminRefusesTheSetterThatDoesNotExist(t *testing.T) {
	cases := []struct {
		name string
		path string
		body string
	}{
		{name: "volver a poner en línea", path: adminBatidosOfflinePath, body: `{"device_id":"dev-1","offline":false}`},
		{name: "volver a marcar normal", path: adminBatidosAbnormalPath, body: `{"device_id":"dev-1","abnormal":false}`},
		{name: "volver a marcar con insumos", path: adminBatidosOutOfStockPath, body: `{"device_id":"dev-1","out_of_stock":false}`},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fx := newAdminFixture(t)

			expectAdminError(t, fx.post(t, testCase.path, testCase.body), http.StatusNotImplemented)
		})
	}
}
