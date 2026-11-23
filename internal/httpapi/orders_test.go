package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"compras-remotas/internal/catalog"
	"compras-remotas/internal/config"
	"compras-remotas/internal/logging"
	"compras-remotas/internal/order"
	"compras-remotas/internal/store"
)

func TestCreateOrderBuildsTheDraftFromTheComboAndTheKiosk(t *testing.T) {
	fx := newFixture(t, config.EnvSandbox, []config.Kiosk{testKiosk("kiosco-centro")}, testCombos())

	recorder := call(t, fx.mux, http.MethodPost, "/orders", `{"kiosk_id":"kiosco-centro","combo_id":"combo-mixto","client_key":"clave-1"}`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("se esperaba 201 y llegó %d: %s", recorder.Code, recorder.Body.String())
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
	if stored.Lines[0].AmountCents != 300000 {
		t.Fatalf("la línea de batidos debe copiar el monto del ítem (300000), quedó AmountCents=%d", stored.Lines[0].AmountCents)
	}

	snacks := stored.Lines[1].Machine
	if snacks.KioskID != "kiosco-centro" || snacks.Kind != "snacks" || snacks.MID != "snacks-mid-kiosco-centro" {
		t.Fatalf("la línea de snacks debe llevar el mid de la máquina del mueble: %+v", snacks)
	}
	if stored.Lines[1].AmountCents != 150000 {
		t.Fatalf("la línea de snacks debe copiar el monto del ítem (150000), quedó AmountCents=%d", stored.Lines[1].AmountCents)
	}
}

func TestCreateOrderUsesTheOnlyKioskWhenTheBodyOmitsIt(t *testing.T) {
	fx := newFixture(t, config.EnvSandbox, []config.Kiosk{testKiosk("kiosco-unico")}, testCombos())

	recorder := call(t, fx.mux, http.MethodPost, "/orders", `{"combo_id":"combo-mixto"}`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("se esperaba 201 y llegó %d: %s", recorder.Code, recorder.Body.String())
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
	if first.Code != http.StatusCreated {
		t.Fatalf("se esperaba 201 y llegó %d: %s", first.Code, first.Body.String())
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
	if third.Code != http.StatusCreated {
		t.Fatalf("se esperaba 201 y llegó %d: %s", third.Code, third.Body.String())
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
	if created.Code != http.StatusCreated {
		t.Fatalf("se esperaba 201 al crear y llegó %d: %s", created.Code, created.Body.String())
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

func TestCreateOrderRejectsReusedClientKeyWithDifferentComboOrKiosk(t *testing.T) {
	combos := append(testCombos(), catalog.Combo{
		ID:         "combo-snack",
		Name:       "Solo snack",
		PriceCents: 150000,
		Currency:   "ARS",
		Active:     true,
		Items: []catalog.Item{
			{MachineType: "snacks", SKU: "snack-alfajor", PriceCents: 150000},
		},
	})
	kiosks := []config.Kiosk{testKiosk("kiosco-uno"), testKiosk("kiosco-dos")}

	cases := []struct {
		name string
		body string
	}{
		{
			name: "otro combo en el mismo mueble",
			body: `{"kiosk_id":"kiosco-uno","combo_id":"combo-snack","client_key":"clave-compartida"}`,
		},
		{
			name: "mismo combo en otro mueble",
			body: `{"kiosk_id":"kiosco-dos","combo_id":"combo-mixto","client_key":"clave-compartida"}`,
		},
		{
			name: "otro combo en otro mueble",
			body: `{"kiosk_id":"kiosco-dos","combo_id":"combo-snack","client_key":"clave-compartida"}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFixture(t, config.EnvSandbox, kiosks, combos)

			first := call(t, fx.mux, http.MethodPost, "/orders", `{"kiosk_id":"kiosco-uno","combo_id":"combo-mixto","client_key":"clave-compartida"}`)
			if first.Code != http.StatusCreated {
				t.Fatalf("se esperaba 201 al crear la orden inicial y llegó %d: %s", first.Code, first.Body.String())
			}

			firstID := decode(t, first)["id"].(string)
			before, err := fx.orders.LoadOrder(context.Background(), firstID)
			if err != nil {
				t.Fatalf("LoadOrder(%q): %v", firstID, err)
			}

			conflict := call(t, fx.mux, http.MethodPost, "/orders", tc.body)
			if conflict.Code != http.StatusConflict {
				t.Fatalf("se esperaba 409 Conflict y llegó %d: %s", conflict.Code, conflict.Body.String())
			}

			payload := decode(t, conflict)
			if len(payload) != 1 || payload["error"] != order.ErrClientKeyConflict.Error() {
				t.Fatalf("cuerpo de error inesperado: %v (se esperaba %q)", payload, order.ErrClientKeyConflict.Error())
			}

			after, err := fx.orders.LoadOrder(context.Background(), firstID)
			if err != nil {
				t.Fatalf("LoadOrder(%q) tras conflicto: %v", firstID, err)
			}
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("la orden existente no debía cambiar: %+v != %+v", after, before)
			}

			if _, err := fx.orders.LoadOrder(context.Background(), "id-4"); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("el rechazo no debía crear ninguna orden nueva, LoadOrder(id-4)=%v", err)
			}

			retry := call(t, fx.mux, http.MethodPost, "/orders", `{"kiosk_id":"kiosco-uno","combo_id":"combo-mixto","client_key":"clave-compartida"}`)
			if retry.Code != http.StatusCreated {
				t.Fatalf("el reintento válido debía seguir devolviendo 201 y llegó %d: %s", retry.Code, retry.Body.String())
			}
			if gotID := decode(t, retry)["id"]; gotID != firstID {
				t.Fatalf("el reintento válido debía devolver %q y devolvió %v", firstID, gotID)
			}
		})
	}
}

func TestCreateOrderEnforcesClientKeyMaxLength(t *testing.T) {
	const wantErrMsg = "la clave del cliente (client_key) no puede superar los 128 caracteres"

	cases := []struct {
		name       string
		clientKey  string
		wantStatus int
		wantTrim   string
	}{
		{
			name:       "128 caracteres ASCII aceptado",
			clientKey:  strings.Repeat("a", 128),
			wantStatus: http.StatusCreated,
			wantTrim:   strings.Repeat("a", 128),
		},
		{
			name:       "128 runas multibyte aceptado",
			clientKey:  strings.Repeat("á", 128),
			wantStatus: http.StatusCreated,
			wantTrim:   strings.Repeat("á", 128),
		},
		{
			name:       "128 caracteres con espacios alrededor aceptado tras trim",
			clientKey:  "   " + strings.Repeat("b", 128) + "   ",
			wantStatus: http.StatusCreated,
			wantTrim:   strings.Repeat("b", 128),
		},
		{
			name:       "129 caracteres ASCII rechazado con 400",
			clientKey:  strings.Repeat("a", 129),
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "129 runas multibyte rechazado con 400",
			clientKey:  strings.Repeat("á", 129),
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "129 caracteres tras trim rechazado con 400",
			clientKey:  "  " + strings.Repeat("c", 129) + "  ",
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFixture(t, config.EnvSandbox, []config.Kiosk{testKiosk("kiosco-centro")}, testCombos())

			body := `{"kiosk_id":"kiosco-centro","combo_id":"combo-mixto","client_key":"` + tc.clientKey + `"}`
			rec := call(t, fx.mux, http.MethodPost, "/orders", body)
			if rec.Code != tc.wantStatus {
				t.Fatalf("se esperaba %d y llegó %d: %s", tc.wantStatus, rec.Code, rec.Body.String())
			}

			payload := decode(t, rec)
			if tc.wantStatus == http.StatusBadRequest {
				if len(payload) != 1 || payload["error"] != wantErrMsg {
					t.Fatalf("cuerpo de error inesperado: %v (se esperaba %q)", payload, wantErrMsg)
				}
				if _, err := fx.orders.LoadOrder(context.Background(), "id-1"); !errors.Is(err, store.ErrNotFound) {
					t.Fatalf("un client_key inválido no debía crear ninguna orden, LoadOrder(id-1)=%v", err)
				}
				return
			}

			orderID, _ := payload["id"].(string)
			stored, err := fx.orders.LoadOrder(context.Background(), orderID)
			if err != nil {
				t.Fatalf("la orden aceptada debía quedar guardada: %v", err)
			}
			if stored.ClientKey != tc.wantTrim {
				t.Fatalf("ClientKey guardado=%q, se esperaba %q", stored.ClientKey, tc.wantTrim)
			}
		})
	}
}

func TestCreateOrderTrimsComboID(t *testing.T) {
	fxUnpadded := newFixture(t, config.EnvSandbox, []config.Kiosk{testKiosk("kiosco-centro")}, testCombos())
	unpadded := call(t, fxUnpadded.mux, http.MethodPost, "/orders", `{"kiosk_id":"kiosco-centro","combo_id":"combo-mixto","client_key":"clave-1"}`)
	if unpadded.Code != http.StatusCreated {
		t.Fatalf("sin espacios se esperaba 201 y llegó %d: %s", unpadded.Code, unpadded.Body.String())
	}

	fxPadded := newFixture(t, config.EnvSandbox, []config.Kiosk{testKiosk("kiosco-centro")}, testCombos())
	padded := call(t, fxPadded.mux, http.MethodPost, "/orders", `{"kiosk_id":"kiosco-centro","combo_id":" combo-mixto ","client_key":"clave-1"}`)
	if padded.Code != http.StatusCreated {
		t.Fatalf("con espacios alrededor se esperaba 201 y llegó %d: %s", padded.Code, padded.Body.String())
	}

	if padded.Body.String() != unpadded.Body.String() {
		t.Fatalf("la respuesta con combo_id con espacios difiere de la normal:\ngot:  %s\nwant: %s", padded.Body.String(), unpadded.Body.String())
	}

	got := decode(t, padded)
	retry := call(t, fxPadded.mux, http.MethodPost, "/orders", `{"kiosk_id":"kiosco-centro","combo_id":"combo-mixto","client_key":"clave-1"}`)
	if retry.Code != http.StatusCreated {
		t.Fatalf("reintento sin espacios sobre la misma client_key: se esperaba 201 y llegó %d: %s", retry.Code, retry.Body.String())
	}
	if decode(t, retry)["id"] != got["id"] {
		t.Fatalf("el reintento debía devolver la misma orden %v y devolvió %v", got["id"], decode(t, retry)["id"])
	}

	blank := call(t, fxPadded.mux, http.MethodPost, "/orders", `{"kiosk_id":"kiosco-centro","combo_id":"   "}`)
	if blank.Code != http.StatusBadRequest {
		t.Fatalf("combo_id solo con espacios se esperaba 400 y llegó %d: %s", blank.Code, blank.Body.String())
	}
}

type failingOrderReader struct {
	err error
}

func (f failingOrderReader) LoadOrder(context.Context, string) (store.Order, error) {
	return store.Order{}, f.err
}

type failingComboCatalog struct {
	err error
}

func (f failingComboCatalog) Combo(context.Context, string) (catalog.Combo, error) {
	return catalog.Combo{}, f.err
}

func (f failingComboCatalog) Combos(context.Context) ([]catalog.Combo, error) {
	return nil, f.err
}

func TestFailLogsOrderIDAndRequestIDWhenAvailable(t *testing.T) {
	t.Run("GET /orders/{id} logs order_id and request_id on 500", func(t *testing.T) {
		var buf bytes.Buffer
		logger := logging.New(&buf, slog.LevelDebug)
		comboCatalog, err := catalog.NewMemory(testCombos())
		if err != nil {
			t.Fatalf("NewMemory: %v", err)
		}

		memory := store.NewMemory()
		engine := order.New(memory, testMachines())
		handler, err := New(
			Deps{
				Catalog:      comboCatalog,
				OrderCreator: engine,
				PaidMarker:   engine,
				Orders:       failingOrderReader{err: errors.New("se cayó postgres")},
				Kiosks:       []config.Kiosk{testKiosk("kiosco-centro")},
				Env:          config.EnvSandbox,
			},
			WithLogger(logger),
		)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		mux := http.NewServeMux()
		handler.Register(mux)
		wrapped := Wrap(mux, logger)

		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/orders/ord-read-500", nil))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, quería 500", rec.Code)
		}

		reqID := rec.Header().Get("X-Request-Id")
		records := parseJSONLogLines(t, buf.String())
		if len(records) != 2 {
			t.Fatalf("se esperaban 2 logs (error + access), llegaron %d: %s", len(records), buf.String())
		}
		errLog := records[0]
		if errLog["order_id"] != "ord-read-500" || errLog["request_id"] != reqID {
			t.Fatalf("log sin order_id o request_id esperado: %v (want order_id=ord-read-500 request_id=%s)", errLog, reqID)
		}
	})

	t.Run("POST /orders/{id}/pay logs order_id and request_id on 500", func(t *testing.T) {
		var buf bytes.Buffer
		logger := logging.New(&buf, slog.LevelDebug)
		comboCatalog, err := catalog.NewMemory(testCombos())
		if err != nil {
			t.Fatalf("NewMemory: %v", err)
		}

		memory := store.NewMemory()
		engine := order.New(memory, testMachines())
		confirmer := &fakeConfirmer{orders: memory, err: errors.New("falla mark paid")}
		handler, err := New(
			Deps{
				Catalog:      comboCatalog,
				OrderCreator: engine,
				PaidMarker:   confirmer,
				Orders:       failingOrderReader{err: errors.New("falla reload")},
				Kiosks:       []config.Kiosk{testKiosk("kiosco-centro")},
				Env:          config.EnvSandbox,
			},
			WithLogger(logger),
		)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		mux := http.NewServeMux()
		handler.Register(mux)
		wrapped := Wrap(mux, logger)

		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/orders/ord-pay-500/pay", nil))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, quería 500", rec.Code)
		}

		reqID := rec.Header().Get("X-Request-Id")
		records := parseJSONLogLines(t, buf.String())
		if len(records) != 2 {
			t.Fatalf("se esperaban 2 logs (error + access), llegaron %d: %s", len(records), buf.String())
		}
		errLog := records[0]
		if errLog["order_id"] != "ord-pay-500" || errLog["request_id"] != reqID {
			t.Fatalf("log sin order_id o request_id esperado: %v (want order_id=ord-pay-500 request_id=%s)", errLog, reqID)
		}
	})

	t.Run("POST /orders logs order_id and request_id when RequestQR fails", func(t *testing.T) {
		var buf bytes.Buffer
		logger := logging.New(&buf, slog.LevelDebug)
		orders := &payOrders{}
		charger := &payCharger{err: fmt.Errorf("%w: timeout", order.ErrQRUnavailable)}
		handler, err := New(
			Deps{
				Catalog:      payCatalog{},
				OrderCreator: orders,
				PaidMarker:   orders,
				Orders:       orders,
				Kiosks:       []config.Kiosk{payKiosk()},
				Env:          config.EnvSandbox,
				QRRequester:  charger,
				Payments:     charger,
			},
			WithLogger(logger),
		)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		mux := http.NewServeMux()
		handler.Register(mux)
		wrapped := Wrap(mux, logger)

		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"combo_id":"combo-1"}`)))
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("status = %d, quería 502", rec.Code)
		}

		reqID := rec.Header().Get("X-Request-Id")
		records := parseJSONLogLines(t, buf.String())
		if len(records) != 2 {
			t.Fatalf("se esperaban 2 logs (error + access), llegaron %d: %s", len(records), buf.String())
		}
		errLog := records[0]
		if errLog["order_id"] != "ord-1" || errLog["request_id"] != reqID {
			t.Fatalf("log sin order_id o request_id esperado: %v (want order_id=ord-1 request_id=%s)", errLog, reqID)
		}
	})

	t.Run("GET /combos does not invent order_id on 500", func(t *testing.T) {
		var buf bytes.Buffer
		logger := logging.New(&buf, slog.LevelDebug)
		memory := store.NewMemory()
		engine := order.New(memory, testMachines())
		handler, err := New(
			Deps{
				Catalog:      failingComboCatalog{err: errors.New("catálogo caído")},
				OrderCreator: engine,
				PaidMarker:   engine,
				Orders:       memory,
				Kiosks:       []config.Kiosk{testKiosk("kiosco-centro")},
				Env:          config.EnvSandbox,
			},
			WithLogger(logger),
		)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		mux := http.NewServeMux()
		handler.Register(mux)
		wrapped := Wrap(mux, logger)

		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/combos", nil))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, quería 500", rec.Code)
		}

		reqID := rec.Header().Get("X-Request-Id")
		records := parseJSONLogLines(t, buf.String())
		if len(records) != 2 {
			t.Fatalf("se esperaban 2 logs (error + access), llegaron %d: %s", len(records), buf.String())
		}
		errLog := records[0]
		if _, hasOrderID := errLog["order_id"]; hasOrderID {
			t.Fatalf("GET /combos no debe inventar order_id: %v", errLog)
		}
		if errLog["request_id"] != reqID {
			t.Fatalf("falta request_id=%q: %v", reqID, errLog)
		}
	})
}
