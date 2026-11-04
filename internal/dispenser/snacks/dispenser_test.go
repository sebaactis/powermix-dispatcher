package snacks_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/dispenser/snacks"
	"compras-remotas/internal/fakeapi"
)

const (
	dispenserDeviceCode = "snacks-dev-1"
	dispenserSKU        = "prod_snack_1"
)

func newDispenserMachine() dispenser.MachineRef {
	return dispenser.MachineRef{
		KioskID: "kiosk-1",
		Type:    "snacks",
		Name:    "Snacks Powermix",
		MID:     dispenserDeviceCode,
	}
}

func newDispenserRequest() dispenser.Request {
	return dispenser.Request{
		OrderID:     "order-1",
		LineID:      "line-1",
		Machine:     newDispenserMachine(),
		SKU:         dispenserSKU,
		AmountCents: 1550,
	}
}

func requestWithRef(ref string) dispenser.Request {
	req := newDispenserRequest()
	req.Ref = ref
	return req
}

func newDispenserWithFake(t *testing.T) (*snacks.Dispenser, *fakeapi.Snacks) {
	t.Helper()

	fake, baseURL := newFake(t)
	client := snacks.NewClient(baseURL, testAccessKey, testAppSecret)

	return snacks.NewDispenser(client), fake
}

func TestDispenserCheckReportsHealthyMachineAsAvailable(t *testing.T) {
	d, _ := newDispenserWithFake(t)

	availability, err := d.Check(context.Background(), newDispenserMachine())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !availability.Available {
		t.Fatalf("la máquina sana se informó no disponible: %+v", availability)
	}
	if availability.Reason != "" {
		t.Fatalf("Reason = %q, se esperaba vacío", availability.Reason)
	}
}

func TestDispenserCheckNamesTheFailingAxis(t *testing.T) {
	cases := []struct {
		name   string
		broken func(*fakeapi.Snacks)
		want   string
	}{
		{
			name:   "fuera de línea",
			broken: func(f *fakeapi.Snacks) { f.SetDeviceOnline(false) },
			want:   "máquina fuera de línea",
		},
		{
			name:   "venta suspendida",
			broken: func(f *fakeapi.Snacks) { f.SetSellStatus(snacks.SellStatusSuspended) },
			want:   "venta suspendida",
		},
		{
			name:   "avería",
			broken: func(f *fakeapi.Snacks) { f.SetFaultState(snacks.FaultStateFault) },
			want:   "avería",
		},
		{
			name:   "bloqueada",
			broken: func(f *fakeapi.Snacks) { f.SetLockType(snacks.LockType(2)) },
			want:   "bloqueada",
		},
		{
			name:   "sin stock",
			broken: func(f *fakeapi.Snacks) { f.SetStockoutState(snacks.StockoutStateOutOfStock) },
			want:   "sin stock",
		},
		{
			name:   "un estado de venta que el proveedor no documentó",
			broken: func(f *fakeapi.Snacks) { f.SetSellStatus(snacks.SellStatus(99)) },
			want:   "venta suspendida",
		},
		{
			name: "avería y bloqueada",
			broken: func(f *fakeapi.Snacks) {
				f.SetFaultState(snacks.FaultStateFault)
				f.SetLockType(snacks.LockType(2))
			},
			want: "avería; bloqueada",
		},
		{
			name: "fuera de línea y avería",
			broken: func(f *fakeapi.Snacks) {
				f.SetDeviceOnline(false)
				f.SetFaultState(snacks.FaultStateFault)
			},
			want: "máquina fuera de línea; avería",
		},
		{
			name: "los cinco ejes, en orden fijo",
			broken: func(f *fakeapi.Snacks) {
				f.SetStockoutState(snacks.StockoutStateOutOfStock)
				f.SetLockType(snacks.LockType(2))
				f.SetFaultState(snacks.FaultStateFault)
				f.SetSellStatus(snacks.SellStatusProhibited)
				f.SetDeviceOnline(false)
			},
			want: "máquina fuera de línea; venta suspendida; avería; bloqueada; sin stock",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, fake := newDispenserWithFake(t)
			tc.broken(fake)

			availability, err := d.Check(context.Background(), newDispenserMachine())
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if availability.Available {
				t.Fatalf("la máquina rota se informó disponible: %+v", availability)
			}
			if availability.Reason != tc.want {
				t.Fatalf("Reason = %q, se esperaba %q", availability.Reason, tc.want)
			}
		})
	}
}

func TestDispenserCheckReportsAnUnknownMachineAsNotAvailable(t *testing.T) {
	d, _ := newDispenserWithFake(t)

	machine := newDispenserMachine()
	machine.MID = "maquina-que-no-existe"

	availability, err := d.Check(context.Background(), machine)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if availability.Available {
		t.Fatalf("una máquina que el proveedor no informa se declaró disponible: %+v", availability)
	}
	if availability.Reason != "máquina no encontrada" {
		t.Fatalf("Reason = %q, se esperaba \"máquina no encontrada\"", availability.Reason)
	}
}

func TestDispenserDispatchShipsOneProductAndTheFakeRecordsIt(t *testing.T) {
	d, fake := newDispenserWithFake(t)

	attempt, err := d.Dispatch(context.Background(), newDispenserRequest())
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if !attempt.Accepted {
		t.Fatalf("el despacho no quedó aceptado: %+v", attempt)
	}

	if got := fake.ShipmentCount("order-1"); got != 1 {
		t.Fatalf("ShipmentCount(order-1) = %d, want 1", got)
	}

	products := fake.ShipmentProducts("order-1")
	if len(products) != 1 {
		t.Fatalf("el despacho llevó %d productos, want 1", len(products))
	}
	if products[0] != dispenserSKU {
		t.Fatalf("el despacho llevó %q, want %q", products[0], dispenserSKU)
	}

	prices := fake.ShipmentRetailPrices("order-1")
	if len(prices) != 1 {
		t.Fatalf("el despacho llevó %d precios, want 1", len(prices))
	}
	if prices[0] != 15.5 {
		t.Fatalf("retailPrice = %v, want 15.5", prices[0])
	}
	if got := strconv.FormatFloat(prices[0], 'f', 2, 64); got != "15.50" {
		t.Fatalf("1550 centavos se enviaron como %q, want \"15.50\"", got)
	}
}

func TestDispenserDispatchFailsWhenTheProviderRejects(t *testing.T) {
	client := snacks.NewClient("http://127.0.0.1:1", testAccessKey, testAppSecret)
	d := snacks.NewDispenser(client)

	attempt, err := d.Dispatch(context.Background(), newDispenserRequest())
	if err == nil {
		t.Fatalf("se esperaba el error de red, attempt = %+v", attempt)
	}
	if attempt.Accepted || attempt.Ref != "" {
		t.Fatalf("no debe inventar un intento aceptado: %+v", attempt)
	}
}

func TestDispenserDispatchReportsAProviderRefusalAsAPortRejection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"code":    40010,
			"message": "el producto no está en venta",
			"result":  nil,
		})
	}))
	defer srv.Close()

	client := snacks.NewClient(srv.URL, testAccessKey, testAppSecret)
	d := snacks.NewDispenser(client)

	attempt, err := d.Dispatch(context.Background(), newDispenserRequest())
	if err == nil {
		t.Fatalf("se esperaba el rechazo del proveedor, attempt = %+v", attempt)
	}

	var rejection *dispenser.Rejection
	if !errors.As(err, &rejection) {
		t.Fatalf("err = %v, se esperaba un *dispenser.Rejection", err)
	}
	if rejection.Provider != "snacks" {
		t.Fatalf("Provider = %q, se esperaba \"snacks\"", rejection.Provider)
	}
	if rejection.Code != 40010 {
		t.Fatalf("Code = %d, se esperaba 40010", rejection.Code)
	}
	if rejection.HTTPStatus != http.StatusBadRequest {
		t.Fatalf("HTTPStatus = %d, se esperaba %d", rejection.HTTPStatus, http.StatusBadRequest)
	}
	if !strings.Contains(rejection.Message, "no está en venta") {
		t.Fatalf("Message = %q, se esperaba el mensaje del proveedor", rejection.Message)
	}
	if !strings.Contains(rejection.Error(), "snacks") || !strings.Contains(rejection.Error(), "40010") {
		t.Fatalf("Error() = %q, se esperaba que nombre al proveedor y el código", rejection.Error())
	}

	var apiErr *snacks.APIError
	if errors.As(err, &apiErr) {
		t.Fatalf("el adaptador dejó escapar el tipo propio del proveedor: %v", err)
	}

	var zero dispenser.Attempt
	if attempt != zero {
		t.Fatalf("no debe inventar un intento: %+v", attempt)
	}
}

func TestDispenserReconcileReportsAnUnknownOrderAsInDoubt(t *testing.T) {
	d, _ := newDispenserWithFake(t)

	status, err := d.Reconcile(context.Background(), requestWithRef("po-que-no-existe"))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if status.State != dispenser.LineInDoubt {
		t.Fatalf("State = %q, se esperaba %q", status.State, dispenser.LineInDoubt)
	}
	if status.Reason == "" {
		t.Fatalf("un pedido que el proveedor no informa necesita un motivo: %+v", status)
	}
}

func TestDispenserReconcileNeverDeclaresDeliveredForASnack(t *testing.T) {
	d, fake := newDispenserWithFake(t)
	ctx := context.Background()

	if _, err := d.Dispatch(ctx, newDispenserRequest()); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	fake.SetOrderStatus(10, "pagado")

	status, err := d.Reconcile(ctx, requestWithRef("order-1"))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if status.State != dispenser.LineInDoubt {
		t.Fatalf("State = %q, se esperaba %q: el estado del pedido no es el resultado del despacho", status.State, dispenser.LineInDoubt)
	}
	if status.State == dispenser.LineDelivered || status.State == dispenser.LineFailed {
		t.Fatalf("Reconcile inventó un veredicto: %+v", status)
	}
	if !strings.Contains(status.Reason, "10") {
		t.Fatalf("Reason = %q, se esperaba que nombre el estado 10 del proveedor", status.Reason)
	}
	if !strings.Contains(status.Reason, "pagado") {
		t.Fatalf("Reason = %q, se esperaba que nombre el estado \"pagado\" del proveedor", status.Reason)
	}
	if status.Ref == "" {
		t.Fatalf("Reconcile volvió sin el orderNo del proveedor: %+v", status)
	}
}

func TestDispenserReconcileFallsBackToTheOrderID(t *testing.T) {
	d, fake := newDispenserWithFake(t)
	ctx := context.Background()

	if _, err := d.Dispatch(ctx, newDispenserRequest()); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	fake.SetOrderStatus(3, "cancelado")

	status, err := d.Reconcile(ctx, newDispenserRequest())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if status.State != dispenser.LineInDoubt {
		t.Fatalf("State = %q, se esperaba %q", status.State, dispenser.LineInDoubt)
	}
	if !strings.Contains(status.Reason, "cancelado") {
		t.Fatalf("Reason = %q, se esperaba el estado del proveedor por OrderID", status.Reason)
	}
}

func TestDispenserReconcilePropagatesAProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"code":    40004,
			"message": "la firma no coincide",
			"result":  nil,
		})
	}))
	defer srv.Close()

	client := snacks.NewClient(srv.URL, testAccessKey, testAppSecret)
	d := snacks.NewDispenser(client)

	status, err := d.Reconcile(context.Background(), requestWithRef("order-1"))
	if err == nil {
		t.Fatalf("se esperaba el error del proveedor, status = %+v", status)
	}

	var rejection *dispenser.Rejection
	if !errors.As(err, &rejection) {
		t.Fatalf("err = %v, se esperaba un *dispenser.Rejection", err)
	}
	if rejection.Provider != "snacks" {
		t.Fatalf("Provider = %q, se esperaba \"snacks\"", rejection.Provider)
	}
	if rejection.Code != 40004 {
		t.Fatalf("Code = %d, se esperaba 40004", rejection.Code)
	}
	if rejection.HTTPStatus != http.StatusUnauthorized {
		t.Fatalf("HTTPStatus = %d, se esperaba %d", rejection.HTTPStatus, http.StatusUnauthorized)
	}

	var apiErr *snacks.APIError
	if errors.As(err, &apiErr) {
		t.Fatalf("el adaptador dejó escapar el tipo propio del proveedor: %v", err)
	}

	var zero dispenser.Status
	if status != zero {
		t.Fatalf("no debe inventar un estado: %+v", status)
	}
}
