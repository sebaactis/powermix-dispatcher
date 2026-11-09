package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"compras-remotas/internal/config"
	"compras-remotas/internal/store"
)

func TestCreateOrderBuildsTheDraftFromTheComboAndTheKiosk(t *testing.T) {
	fx := newFixture(t, config.EnvSandbox, []config.Kiosk{testKiosk("kiosco-centro")}, testCombos())

	recorder := call(t, fx.mux, http.MethodPost, "/orders", `{"kiosk_id":"kiosco-centro","combo_id":"combo-mixto","client_key":"clave-1"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("se esperaba 200 y llegó %d: %s", recorder.Code, recorder.Body.String())
	}

	payload := decode(t, recorder)

	if payload["kiosk_id"] != "kiosco-centro" || payload["combo_id"] != "combo-mixto" {
		t.Fatalf("la orden debe llevar el mueble y el combo pedidos: %v", payload)
	}

	if payload["status"] != string(store.OrderPendingPayment) {
		t.Fatalf("la orden nace pendiente de pago y llegó %v", payload["status"])
	}

	if payload["amount_cents"] != float64(450000) {
		t.Fatalf("el importe debe salir del combo (450000) y llegó %v", payload["amount_cents"])
	}

	if payload["currency"] != "ARS" {
		t.Fatalf("la moneda debe salir del combo y llegó %v", payload["currency"])
	}

	lines := array(t, payload, "lines")
	if len(lines) != 2 {
		t.Fatalf("el combo tiene dos ítems y llegaron %d líneas: %v", len(lines), lines)
	}

	first := lines[0].(map[string]any)
	second := lines[1].(map[string]any)

	if first["machine_type"] != "batidos" || first["sku"] != "batido-frutilla" {
		t.Fatalf("la primera línea debe ser el batido: %v", first)
	}

	if second["machine_type"] != "snacks" || second["sku"] != "snack-alfajor" {
		t.Fatalf("la segunda línea debe ser el snack: %v", second)
	}

	if first["state"] != "pending" || second["state"] != "pending" {
		t.Fatalf("las líneas nacen pendientes: %v", lines)
	}

	stored, err := fx.orders.LoadOrder(context.Background(), payload["id"].(string))
	if err != nil {
		t.Fatalf("la orden no quedó guardada: %v", err)
	}

	batidos := stored.Lines[0].Machine
	if batidos.KioskID != "kiosco-centro" || batidos.Kind != "batidos" || batidos.DeviceID != "batidos-device-kiosco-centro" || batidos.DeviceNo != "batidos-devno-kiosco-centro" {
		t.Fatalf("la línea de batidos debe llevar la identidad de la máquina del mueble: %+v", batidos)
	}

	snacks := stored.Lines[1].Machine
	if snacks.KioskID != "kiosco-centro" || snacks.Kind != "snacks" || snacks.MID != "snacks-mid-kiosco-centro" {
		t.Fatalf("la línea de snacks debe llevar el mid de la máquina del mueble: %+v", snacks)
	}
}

func TestCreateOrderUsesTheOnlyKioskWhenTheBodyOmitsIt(t *testing.T) {
	fx := newFixture(t, config.EnvSandbox, []config.Kiosk{testKiosk("kiosco-unico")}, testCombos())

	recorder := call(t, fx.mux, http.MethodPost, "/orders", `{"combo_id":"combo-mixto"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("se esperaba 200 y llegó %d: %s", recorder.Code, recorder.Body.String())
	}

	payload := decode(t, recorder)
	if payload["kiosk_id"] != "kiosco-unico" {
		t.Fatalf("con un solo mueble configurado se debe usar ese y llegó %v", payload["kiosk_id"])
	}
}

func TestCreateOrderRequiresAKioskWhenSeveralAreConfigured(t *testing.T) {
	fx := newFixture(t, config.EnvSandbox, []config.Kiosk{testKiosk("kiosco-uno"), testKiosk("kiosco-dos")}, testCombos())

	recorder := call(t, fx.mux, http.MethodPost, "/orders", `{"combo_id":"combo-mixto"}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("se esperaba 400 y llegó %d: %s", recorder.Code, recorder.Body.String())
	}

	payload := decode(t, recorder)
	if payload["error"] == "" {
		t.Fatalf("el rechazo debe explicar qué falta: %v", payload)
	}

	if _, err := fx.orders.LoadOrder(context.Background(), "id-1"); err == nil {
		t.Fatalf("un pedido ambiguo no debe crear ninguna orden")
	}
}

func TestCreateOrderRejectsAnUnknownKiosk(t *testing.T) {
	fx := newFixture(t, config.EnvSandbox, []config.Kiosk{testKiosk("kiosco-centro")}, testCombos())

	recorder := call(t, fx.mux, http.MethodPost, "/orders", `{"kiosk_id":"kiosco-fantasma","combo_id":"combo-mixto"}`)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("se esperaba 404 y llegó %d: %s", recorder.Code, recorder.Body.String())
	}

	if _, err := fx.orders.LoadOrder(context.Background(), "id-1"); err == nil {
		t.Fatalf("un mueble inexistente no debe crear ninguna orden")
	}
}

func TestCreateOrderIsIdempotentByClientKey(t *testing.T) {
	fx := newFixture(t, config.EnvSandbox, []config.Kiosk{testKiosk("kiosco-centro")}, testCombos())

	first := call(t, fx.mux, http.MethodPost, "/orders", `{"combo_id":"combo-mixto","client_key":"clave-repetida"}`)
	if first.Code != http.StatusOK {
		t.Fatalf("se esperaba 200 y llegó %d: %s", first.Code, first.Body.String())
	}

	second := call(t, fx.mux, http.MethodPost, "/orders", `{"combo_id":"combo-mixto","client_key":"clave-repetida"}`)
	if second.Code != first.Code {
		t.Fatalf("el reintento debe responder igual (%d) y llegó %d: %s", first.Code, second.Code, second.Body.String())
	}

	firstPayload := decode(t, first)
	secondPayload := decode(t, second)

	if firstPayload["id"] != secondPayload["id"] {
		t.Fatalf("el reintento con la misma clave debe devolver la misma orden (%v y %v)", firstPayload["id"], secondPayload["id"])
	}

	stored, err := fx.orders.OrderByClientKey(context.Background(), "clave-repetida")
	if err != nil {
		t.Fatalf("la orden con cliente debe quedar guardada: %v", err)
	}

	if stored.ID != firstPayload["id"] {
		t.Fatalf("la orden guardada no coincide con la respondida: %v y %v", stored.ID, firstPayload["id"])
	}

	if _, err := fx.orders.LoadOrder(context.Background(), "id-1"); err != nil {
		t.Fatalf("la primera orden debe seguir guardada: %v", err)
	}

	third := call(t, fx.mux, http.MethodPost, "/orders", `{"combo_id":"combo-mixto","client_key":"otra-clave"}`)
	if third.Code != http.StatusOK {
		t.Fatalf("se esperaba 200 y llegó %d: %s", third.Code, third.Body.String())
	}

	if id := decode(t, third)["id"]; id != "id-4" {
		t.Fatalf("el reintento no debe consumir identificadores: la orden siguiente debía ser id-4 y fue %v", id)
	}
}

func TestCreateOrderRejectsAnUnknownCombo(t *testing.T) {
	fx := newFixture(t, config.EnvSandbox, []config.Kiosk{testKiosk("kiosco-centro")}, testCombos())

	recorder := call(t, fx.mux, http.MethodPost, "/orders", `{"combo_id":"combo-inactivo"}`)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("un combo inactivo no existe para la venta: se esperaba 404 y llegó %d: %s", recorder.Code, recorder.Body.String())
	}

	payload := decode(t, recorder)
	if payload["error"] == "" {
		t.Fatalf("el rechazo debe explicar el motivo: %v", payload)
	}
}

func TestCreateOrderRejectsAMalformedBody(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "sin cuerpo", body: ""},
		{name: "no es json", body: "combo_id=combo-mixto"},
		{name: "campo desconocido", body: `{"combo_id":"combo-mixto","atajo":true}`},
		{name: "sin combo", body: `{"kiosk_id":"kiosco-centro"}`},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fx := newFixture(t, config.EnvSandbox, []config.Kiosk{testKiosk("kiosco-centro")}, testCombos())

			recorder := call(t, fx.mux, http.MethodPost, "/orders", testCase.body)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("se esperaba 400 y llegó %d: %s", recorder.Code, recorder.Body.String())
			}

			if _, err := fx.orders.LoadOrder(context.Background(), "id-1"); err == nil {
				t.Fatalf("un cuerpo inválido no debe crear ninguna orden")
			}
		})
	}
}

func TestCreateOrderRejectsAComboTheKioskCannotServe(t *testing.T) {
	fx := newFixture(t, config.EnvSandbox, []config.Kiosk{testKiosk("kiosco-centro")}, testCombos())

	recorder := call(t, fx.mux, http.MethodPost, "/orders", `{"combo_id":"combo-con-maquina-ausente"}`)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("un combo que pide una máquina ausente se esperaba 409 y llegó %d: %s", recorder.Code, recorder.Body.String())
	}

	payload := decode(t, recorder)
	if !strings.Contains(payload["error"].(string), "cafetera") {
		t.Fatalf("el rechazo debe nombrar la máquina que falta: %v", payload["error"])
	}

	if _, err := fx.orders.LoadOrder(context.Background(), "id-1"); err == nil {
		t.Fatalf("un combo no vendible en el mueble no debe crear ninguna orden")
	}
}

func TestReadOrderReturnsTheLiveState(t *testing.T) {
	fx := newFixture(t, config.EnvSandbox, []config.Kiosk{testKiosk("kiosco-centro")}, testCombos())

	created := call(t, fx.mux, http.MethodPost, "/orders", `{"combo_id":"combo-mixto"}`)
	if created.Code != http.StatusOK {
		t.Fatalf("se esperaba 200 al crear y llegó %d: %s", created.Code, created.Body.String())
	}

	orderID := decode(t, created)["id"].(string)

	recorder := call(t, fx.mux, http.MethodGet, "/orders/"+orderID, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("se esperaba 200 y llegó %d: %s", recorder.Code, recorder.Body.String())
	}

	payload := decode(t, recorder)
	if payload["id"] != orderID {
		t.Fatalf("se pidió la orden %v y llegó %v", orderID, payload["id"])
	}

	if payload["status"] != string(store.OrderPendingPayment) {
		t.Fatalf("el estado debe venir del store y llegó %v", payload["status"])
	}

	if payload["created_at"] == nil {
		t.Fatalf("el estado en vivo debe informar cuándo se creó: %v", payload)
	}

	lines := array(t, payload, "lines")
	if len(lines) != 2 {
		t.Fatalf("se esperaban 2 líneas y llegaron %d", len(lines))
	}

	line := lines[0].(map[string]any)
	if line["machine_type"] != "batidos" || line["sku"] != "batido-frutilla" || line["state"] != "pending" {
		t.Fatalf("cada línea debe informar su máquina, su producto y su estado: %v", line)
	}

	if line["position"] != float64(1) {
		t.Fatalf("la línea debe informar su posición: %v", line["position"])
	}
}

func TestReadOrderReturnsNotFoundForAnUnknownOrder(t *testing.T) {
	fx := newFixture(t, config.EnvSandbox, []config.Kiosk{testKiosk("kiosco-centro")}, testCombos())

	recorder := call(t, fx.mux, http.MethodGet, "/orders/no-existe", "")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("se esperaba 404 y llegó %d: %s", recorder.Code, recorder.Body.String())
	}

	if decode(t, recorder)["error"] == "" {
		t.Fatalf("el 404 debe explicar el motivo")
	}
}

func TestPayConfirmsThePaymentInSandbox(t *testing.T) {
	fx := newFixture(t, config.EnvSandbox, []config.Kiosk{testKiosk("kiosco-centro")}, testCombos())

	created := call(t, fx.mux, http.MethodPost, "/orders", `{"combo_id":"combo-mixto"}`)
	orderID := decode(t, created)["id"].(string)

	recorder := call(t, fx.mux, http.MethodPost, "/orders/"+orderID+"/pay", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("se esperaba 200 y llegó %d: %s", recorder.Code, recorder.Body.String())
	}

	if len(fx.confirmer.calls) != 1 || fx.confirmer.calls[0] != orderID {
		t.Fatalf("el pago debe confirmarse una sola vez para %q y se llamó %v", orderID, fx.confirmer.calls)
	}

	payload := decode(t, recorder)
	if payload["status"] != string(store.OrderPaid) {
		t.Fatalf("la respuesta debe reflejar el estado ya confirmado y llegó %v", payload["status"])
	}

	if payload["paid_at"] == nil {
		t.Fatalf("el pago confirmado debe informar cuándo: %v", payload)
	}
}

func TestPayIsRefusedInProduction(t *testing.T) {
	fx := newFixture(t, config.EnvProduction, []config.Kiosk{testKiosk("kiosco-centro")}, testCombos())

	created := call(t, fx.mux, http.MethodPost, "/orders", `{"combo_id":"combo-mixto"}`)
	orderID := decode(t, created)["id"].(string)

	recorder := call(t, fx.mux, http.MethodPost, "/orders/"+orderID+"/pay", "")
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("el pago simulado debe estar prohibido en producción: se esperaba 403 y llegó %d: %s", recorder.Code, recorder.Body.String())
	}

	if len(fx.confirmer.calls) != 0 {
		t.Fatalf("en producción no se debe registrar ningún pago y se llamó %v", fx.confirmer.calls)
	}

	stored, err := fx.orders.LoadOrder(context.Background(), orderID)
	if err != nil {
		t.Fatalf("la orden no se pudo releer: %v", err)
	}

	if stored.Status != store.OrderPendingPayment || !stored.PaidAt.IsZero() {
		t.Fatalf("en producción la orden debe quedar intacta: estado %q, pagada %v", stored.Status, stored.PaidAt)
	}

	if decode(t, recorder)["error"] == "" {
		t.Fatalf("el rechazo debe explicar el motivo")
	}
}

func TestPayReturnsNotFoundForAnUnknownOrder(t *testing.T) {
	fx := newFixture(t, config.EnvSandbox, []config.Kiosk{testKiosk("kiosco-centro")}, testCombos())

	recorder := call(t, fx.mux, http.MethodPost, "/orders/no-existe/pay", "")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("se esperaba 404 y llegó %d: %s", recorder.Code, recorder.Body.String())
	}

	if len(fx.confirmer.calls) != 1 {
		t.Fatalf("la existencia de la orden la decide el motor, no un chequeo previo: se llamó %v", fx.confirmer.calls)
	}
}

func TestPayReportsTheOrderStateWhenTheDispatchFailsAfterRegisteringThePayment(t *testing.T) {
	fx := newFixture(t, config.EnvSandbox, []config.Kiosk{testKiosk("kiosco-centro")}, testCombos())

	created := call(t, fx.mux, http.MethodPost, "/orders", `{"combo_id":"combo-mixto"}`)
	orderID := decode(t, created)["id"].(string)

	fx.confirmer.saveAnyway = true
	fx.confirmer.err = errors.New("batidos: la llamada al proveedor falló")

	recorder := call(t, fx.mux, http.MethodPost, "/orders/"+orderID+"/pay", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("el pago ya quedó registrado: se esperaba 200 con el estado y llegó %d: %s", recorder.Code, recorder.Body.String())
	}

	payload := decode(t, recorder)
	if payload["status"] != string(store.OrderPaid) {
		t.Fatalf("la respuesta debe informar el estado real de la orden y llegó %v", payload["status"])
	}

	if len(fx.confirmer.calls) != 1 {
		t.Fatalf("el pago debe confirmarse una sola vez y se llamó %v", fx.confirmer.calls)
	}
}

func TestPayFailsWhenThePaymentIsNotRegistered(t *testing.T) {
	fx := newFixture(t, config.EnvSandbox, []config.Kiosk{testKiosk("kiosco-centro")}, testCombos())

	created := call(t, fx.mux, http.MethodPost, "/orders", `{"combo_id":"combo-mixto"}`)
	orderID := decode(t, created)["id"].(string)

	fx.confirmer.err = errors.New("el store no respondió")

	recorder := call(t, fx.mux, http.MethodPost, "/orders/"+orderID+"/pay", "")
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("si el pago no se registró se esperaba 500 y llegó %d: %s", recorder.Code, recorder.Body.String())
	}

	stored, err := fx.orders.LoadOrder(context.Background(), orderID)
	if err != nil {
		t.Fatalf("la orden no se pudo releer: %v", err)
	}

	if stored.Status != store.OrderPendingPayment {
		t.Fatalf("la orden debe seguir pendiente de pago y llegó %q", stored.Status)
	}
}
