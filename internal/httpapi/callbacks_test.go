package httpapi

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"compras-remotas/internal/config"
	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/machine"
	"compras-remotas/internal/order"
	"compras-remotas/internal/store"
)

func TestBatidosCallbackConfirmsWithTheProvider(t *testing.T) {
	mux, receiver := newCallbackFixture(t, CallbackSecrets{Batidos: "ruta-secreta"})

	body := `{"code":"canje-1","deviceId":"dev-1","goodsId":"sku-1","orderNo":"A202603241535065465","status":2}`
	recorder := call(t, mux, http.MethodPost, "/callbacks/batidos/ruta-secreta/orden-1", body)

	if recorder.Code != http.StatusOK {
		t.Fatalf("se esperaba 200 y llegó %d: %s", recorder.Code, recorder.Body.String())
	}

	ack := decode(t, recorder)
	if ack["code"] != float64(200) || ack["success"] != true || ack["data"] != true || ack["msg"] != "操作成功" {
		t.Fatalf("la respuesta debe ser exactamente la que documenta el proveedor: %v", ack)
	}

	if len(receiver.confirmed) != 1 {
		t.Fatalf("el callback de batidos se confirma contra el proveedor y se llamó %d veces", len(receiver.confirmed))
	}

	got := receiver.confirmed[0]
	if got.Source != "batidos" || got.Ref != "canje-1" || got.OrderID != "orden-1" || got.State != order.CallbackDelivered {
		t.Fatalf("el evento no coincide con el payload: %+v", got)
	}

	if got.DedupeKey == "" {
		t.Fatalf("el evento debe llevar una clave de deduplicación")
	}

	if len(receiver.closed) != 0 {
		t.Fatalf("un callback sin firma no debe cerrar la línea por su cuenta: %v", receiver.closed)
	}
}

func TestBatidosCallbackMapsEveryDocumentedStatus(t *testing.T) {
	cases := []struct {
		status  int
		state   order.CallbackState
		ignored bool
	}{
		{status: 2, state: order.CallbackDelivered},
		{status: 1, state: order.CallbackProgress},
		{status: 0, state: order.CallbackFailed},
		{status: 7, ignored: true},
	}

	for _, testCase := range cases {
		t.Run(itoa(testCase.status), func(t *testing.T) {
			mux, receiver := newCallbackFixture(t, CallbackSecrets{Batidos: "ruta-secreta"})

			body := `{"code":"canje-1","orderNo":"A1","status":` + itoa(testCase.status) + `}`
			recorder := call(t, mux, http.MethodPost, "/callbacks/batidos/ruta-secreta/orden-1", body)

			if recorder.Code != http.StatusOK {
				t.Fatalf("se esperaba 200 y llegó %d: %s", recorder.Code, recorder.Body.String())
			}

			if testCase.ignored {
				if len(receiver.confirmed) != 0 {
					t.Fatalf("un estado fuera de la tabla no debe interpretarse: %v", receiver.confirmed)
				}

				return
			}

			if len(receiver.confirmed) != 1 {
				t.Fatalf("se esperaba un evento y llegaron %d", len(receiver.confirmed))
			}

			if receiver.confirmed[0].State != testCase.state {
				t.Fatalf("el estado %d debía mapearse a %q y llegó %q", testCase.status, testCase.state, receiver.confirmed[0].State)
			}
		})
	}
}

func TestBatidosCallbackRejectsAWrongSecret(t *testing.T) {
	mux, receiver := newCallbackFixture(t, CallbackSecrets{Batidos: "ruta-secreta"})

	recorder := call(t, mux, http.MethodPost, "/callbacks/batidos/otra-ruta/orden-1", `{"code":"canje-1","status":2}`)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("una ruta secreta equivocada no debe distinguirse de una inexistente: 404 y llegó %d: %s", recorder.Code, recorder.Body.String())
	}

	if len(receiver.confirmed) != 0 || len(receiver.closed) != 0 {
		t.Fatalf("un callback sin la ruta secreta no debe procesarse")
	}
}

func TestCallbackIsRefusedWhenNoSecretIsConfigured(t *testing.T) {
	mux, receiver := newCallbackFixture(t, CallbackSecrets{})

	recorder := call(t, mux, http.MethodPost, "/callbacks/batidos/cualquier-cosa/orden-1", `{"code":"canje-1","status":2}`)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("sin secreto configurado el endpoint debe fallar cerrado: 404 y llegó %d", recorder.Code)
	}

	snacks := call(t, mux, http.MethodPost, "/callbacks/snacks/cualquier-cosa", `{"PaymentOrderId":"orden-1","OrderProductResults":[]}`)
	if snacks.Code != http.StatusNotFound {
		t.Fatalf("sin secreto configurado el endpoint de snacks debe fallar cerrado: 404 y llegó %d", snacks.Code)
	}

	if len(receiver.confirmed) != 0 || len(receiver.closed) != 0 {
		t.Fatalf("ningún callback debe procesarse sin secreto configurado")
	}
}

func TestSnacksCallbackClosesOneLinePerResult(t *testing.T) {
	mux, receiver := newCallbackFixture(t, CallbackSecrets{Snacks: "ruta-secreta"})

	body := `{
		"OrderNo":"2026*******0000",
		"PaymentOrderId":"orden-1",
		"Mid":"P50*******20001",
		"OrderProductResults":[
			{"SubOrderNo":"sub-1","ProductNo":"695","ShipmentStatus":1,"ShipmentQuantity":1},
			{"SubOrderNo":"sub-2","ProductNo":"696","ShipmentStatus":0,"ShipmentErrorReason":"canal obstruido"}
		]
	}`

	recorder := call(t, mux, http.MethodPost, "/callbacks/snacks/ruta-secreta", body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("se esperaba 200 y llegó %d: %s", recorder.Code, recorder.Body.String())
	}

	ack := decode(t, recorder)
	if ack["success"] != true || ack["code"] != float64(200) {
		t.Fatalf("la respuesta debe ser la que documenta el proveedor: %v", ack)
	}

	if len(receiver.closed) != 2 {
		t.Fatalf("se esperaba un cierre por resultado de suborden y llegaron %d", len(receiver.closed))
	}

	delivered := receiver.closed[0]
	if delivered.Source != "snacks" || delivered.OrderID != "orden-1" || delivered.Ref != "sub-1" || delivered.State != order.CallbackDelivered {
		t.Fatalf("el primer resultado debía quedar entregado: %+v", delivered)
	}

	failed := receiver.closed[1]
	if failed.Ref != "sub-2" || failed.State != order.CallbackFailed {
		t.Fatalf("el segundo resultado debía quedar fallido: %+v", failed)
	}

	if failed.Reason != "canal obstruido" {
		t.Fatalf("el motivo del proveedor debe viajar al motor y llegó %q", failed.Reason)
	}

	if delivered.DedupeKey == failed.DedupeKey {
		t.Fatalf("cada suborden es su propio evento y necesita su propia clave: %q", delivered.DedupeKey)
	}

	if len(receiver.confirmed) != 0 {
		t.Fatalf("en snacks el callback es la única fuente y no se debe consultar nada más")
	}
}

func TestSnacksCallbackIgnoresAnUnknownShipmentStatus(t *testing.T) {
	mux, receiver := newCallbackFixture(t, CallbackSecrets{Snacks: "ruta-secreta"})

	body := `{"PaymentOrderId":"orden-1","OrderProductResults":[{"SubOrderNo":"sub-1","ShipmentStatus":9}]}`

	recorder := call(t, mux, http.MethodPost, "/callbacks/snacks/ruta-secreta", body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("un estado desconocido se acusa recibo igual: 200 y llegó %d", recorder.Code)
	}

	if len(receiver.closed) != 0 {
		t.Fatalf("un estado fuera de la tabla no debe cerrar la línea: %v", receiver.closed)
	}
}

func TestCallbackForAnUnknownLineIsAcknowledged(t *testing.T) {
	mux, receiver := newCallbackFixture(t, CallbackSecrets{Snacks: "ruta-secreta"})
	receiver.closeErr = order.ErrCallbackUnknownLine

	body := `{"PaymentOrderId":"orden-1","OrderProductResults":[{"SubOrderNo":"sub-1","ShipmentStatus":1}]}`

	recorder := call(t, mux, http.MethodPost, "/callbacks/snacks/ruta-secreta", body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("si no hay nada que cerrar se acusa recibo para que no se reintente: 200 y llegó %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestCallbackWithAnIncompleteEventIsAcknowledged(t *testing.T) {
	mux, receiver := newCallbackFixture(t, CallbackSecrets{Snacks: "ruta-secreta"})
	receiver.closeErr = order.ErrCallbackIncomplete

	body := `{"OrderProductResults":[{"ShipmentStatus":1}]}`

	recorder := call(t, mux, http.MethodPost, "/callbacks/snacks/ruta-secreta", body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("un evento incompleto nunca se va a poder aplicar: se acusa recibo con 200 y llegó %d: %s", recorder.Code, recorder.Body.String())
	}

	ack := decode(t, recorder)
	if ack["success"] != true || ack["code"] != float64(200) {
		t.Fatalf("la respuesta debe ser la que documenta el proveedor: %v", ack)
	}
}

func TestCallbackWithABrokenBodyIsAcknowledged(t *testing.T) {
	mux, receiver := newCallbackFixture(t, CallbackSecrets{Batidos: "ruta-secreta"})

	recorder := call(t, mux, http.MethodPost, "/callbacks/batidos/ruta-secreta/orden-1", "no es json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("un cuerpo roto nunca se va a poder aplicar: se acusa recibo con 200 y llegó %d: %s", recorder.Code, recorder.Body.String())
	}

	ack := decode(t, recorder)
	if ack["code"] != float64(200) || ack["success"] != true || ack["data"] != true {
		t.Fatalf("la respuesta debe ser la que documenta el proveedor: %v", ack)
	}

	if len(receiver.confirmed) != 0 || len(receiver.closed) != 0 {
		t.Fatalf("un cuerpo roto no debe llegar al motor")
	}
}

func TestCallbackWithARealFailureAsksForARetry(t *testing.T) {
	mux, receiver := newCallbackFixture(t, CallbackSecrets{Snacks: "ruta-secreta"})
	receiver.closeErr = errors.New("el proveedor no respondió")

	body := `{"PaymentOrderId":"orden-1","OrderProductResults":[{"SubOrderNo":"sub-1","ShipmentStatus":1}]}`

	recorder := call(t, mux, http.MethodPost, "/callbacks/snacks/ruta-secreta", body)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("un error transitorio sí se puede reintentar: 500 y llegó %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestCallbackRoutesAreAbsentWithoutAReceiver(t *testing.T) {
	fx := newFixture(t, config.EnvSandbox, []config.Kiosk{testKiosk("kiosco-centro")}, testCombos())

	recorder := call(t, fx.mux, http.MethodPost, "/callbacks/snacks/ruta-secreta", `{}`)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("sin receptor de callbacks la ruta no debe existir: 404 y llegó %d", recorder.Code)
	}
}

func TestSnacksCallbackClosesTheLineByThirdOrderNo(t *testing.T) {
	fx := newLiveCallbackFixture(t, twoSnackItemsCombos(), map[machine.Kind]dispenser.Dispenser{
		"batidos": fakeDispenser{},
		"snacks":  silentRefDispenser{},
	})

	paid := paidOrder(t, fx, "combo-doble-snack")

	first := paid.Lines[0]
	if first.Ref != "" {
		t.Fatalf("el escenario necesita una línea sin referencia y tiene %q", first.Ref)
	}

	body := `{
		"OrderNo":"2026*******0000",
		"PaymentOrderId":"` + paid.ID + `",
		"Mid":"P50*******20001",
		"OrderProductResults":[
			{"SubOrderNo":"sub-que-no-coincide","ThirdOrderNo":"` + first.ID + `","ShipmentStatus":1}
		]
	}`

	recorder := call(t, fx.mux, http.MethodPost, "/callbacks/snacks/ruta-secreta", body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("se esperaba 200 y llegó %d: %s", recorder.Code, recorder.Body.String())
	}

	loaded, err := fx.orders.LoadOrder(context.Background(), paid.ID)
	if err != nil {
		t.Fatalf("la orden no se pudo releer: %v", err)
	}

	if loaded.Lines[0].State != dispenser.LineDelivered {
		t.Fatalf("el ThirdOrderNo identifica la línea y debía quedar entregada, pero quedó %q", loaded.Lines[0].State)
	}

	if loaded.Lines[1].State == dispenser.LineDelivered {
		t.Fatalf("la otra línea no era la del callback y no debe cerrarse: %q", loaded.Lines[1].State)
	}
}

func TestSnacksCallbackWithTwoCandidatesIsAcknowledgedWithoutClosingAnything(t *testing.T) {
	fx := newLiveCallbackFixture(t, twoSnackItemsCombos(), map[machine.Kind]dispenser.Dispenser{
		"batidos": fakeDispenser{},
		"snacks":  silentRefDispenser{},
	})

	paid := paidOrder(t, fx, "combo-doble-snack")

	body := `{
		"OrderNo":"2026*******0000",
		"PaymentOrderId":"` + paid.ID + `",
		"Mid":"P50*******20001",
		"OrderProductResults":[
			{"ShipmentStatus":1}
		]
	}`

	recorder := call(t, fx.mux, http.MethodPost, "/callbacks/snacks/ruta-secreta", body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("un callback ambiguo se acusa recibo para que no se reintente: 200 y llegó %d: %s", recorder.Code, recorder.Body.String())
	}

	loaded, err := fx.orders.LoadOrder(context.Background(), paid.ID)
	if err != nil {
		t.Fatalf("la orden no se pudo releer: %v", err)
	}

	for _, line := range loaded.Lines {
		if line.State == dispenser.LineDelivered || line.State == dispenser.LineFailed {
			t.Fatalf("un callback ambiguo no debe cerrar ninguna línea: %+v", line)
		}
	}
}

func TestSnacksCallbackDoesNotCloseTheBatidosLineByThirdOrderNo(t *testing.T) {
	fx := newLiveCallbackFixture(t, testCombos(), map[machine.Kind]dispenser.Dispenser{
		"batidos": fakeDispenser{},
		"snacks":  silentRefDispenser{},
	})

	paid := paidOrder(t, fx, "combo-mixto")

	batidos := store.Line{}

	for _, line := range paid.Lines {
		if line.Machine.Kind == "batidos" {
			batidos = line
		}
	}

	if batidos.ID == "" {
		t.Fatalf("el combo mixto debía tener una línea de batidos: %+v", paid.Lines)
	}

	body := `{
		"OrderNo":"2026*******0000",
		"PaymentOrderId":"` + paid.ID + `",
		"Mid":"P50*******20001",
		"OrderProductResults":[
			{"SubOrderNo":"sub-de-batidos","ThirdOrderNo":"` + batidos.ID + `","ShipmentStatus":1}
		]
	}`

	recorder := call(t, fx.mux, http.MethodPost, "/callbacks/snacks/ruta-secreta", body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("un ThirdOrderNo ajeno se acusa recibo para que no se reintente: 200 y llegó %d: %s", recorder.Code, recorder.Body.String())
	}

	loaded, err := fx.orders.LoadOrder(context.Background(), paid.ID)
	if err != nil {
		t.Fatalf("la orden no se pudo releer: %v", err)
	}

	for _, line := range loaded.Lines {
		if line.Machine.Kind != "batidos" {
			continue
		}

		if line.State == dispenser.LineDelivered || line.State == dispenser.LineFailed {
			t.Fatalf("un callback de snacks no debe cerrar la línea de batidos: %+v", line)
		}
	}
}
