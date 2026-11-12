package batidos_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/dispenser/batidos"
	"compras-remotas/internal/fakeapi"
	"compras-remotas/internal/linestate"
	"compras-remotas/internal/machine"
)

const (
	dispatchDeviceID     = "dev-1"
	dispatchDeviceNo     = "devno-1"
	dispatchDeviceTypeID = "dt-1"
	dispatchSKU          = "goods-1"

	produceCoffeePath = "/coffee/newThird/order/produceCoffee"
)

var sixDigitCode = regexp.MustCompile(`^\d{6}$`)

func newRef() machine.Ref {
	return machine.Ref{
		KioskID:      "kiosk-1",
		Kind:         "batidos",
		Name:         "Batidos Powermix",
		DeviceID:     dispatchDeviceID,
		DeviceNo:     dispatchDeviceNo,
		DeviceTypeID: dispatchDeviceTypeID,
		MID:          "mid-1",
	}
}

func newRequest() dispenser.Request {
	return dispenser.Request{
		OrderID:     "order-1",
		LineID:      "line-1",
		Machine:     newRef(),
		SKU:         dispatchSKU,
		AmountCents: 150000,
	}
}

func requestWithRef(ref string) dispenser.Request {
	req := newRequest()
	req.KnownRef = ref
	return req
}

func newDispenserWithFake(t *testing.T) (*batidos.Dispenser, *fakeapi.Batidos, *batidos.Client) {
	t.Helper()

	fake := fakeapi.NewBatidos(testKey, testSecret)
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)

	client := newTestClient(t, srv.URL)

	return batidos.NewDispenser(client), fake, client
}

func TestDispenserCheckReportsHealthyMachineAsAvailable(t *testing.T) {
	d, _, _ := newDispenserWithFake(t)

	availability, err := d.Check(context.Background(), newRef())
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
		broken func(*fakeapi.Batidos)
		want   string
	}{
		{
			name:   "fuera de línea",
			broken: func(f *fakeapi.Batidos) { f.SetDeviceOffline(dispatchDeviceID) },
			want:   "máquina fuera de línea",
		},
		{
			name:   "avería",
			broken: func(f *fakeapi.Batidos) { f.SetDeviceAbnormal(dispatchDeviceID) },
			want:   "avería",
		},
		{
			name:   "sin insumos",
			broken: func(f *fakeapi.Batidos) { f.SetDeviceOutOfStock(dispatchDeviceID) },
			want:   "sin insumos",
		},
		{
			name: "avería y sin insumos",
			broken: func(f *fakeapi.Batidos) {
				f.SetDeviceAbnormal(dispatchDeviceID)
				f.SetDeviceOutOfStock(dispatchDeviceID)
			},
			want: "avería; sin insumos",
		},
		{
			name: "fuera de línea y avería",
			broken: func(f *fakeapi.Batidos) {
				f.SetDeviceOffline(dispatchDeviceID)
				f.SetDeviceAbnormal(dispatchDeviceID)
			},
			want: "máquina fuera de línea; avería",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, fake, _ := newDispenserWithFake(t)
			tc.broken(fake)

			availability, err := d.Check(context.Background(), newRef())
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

func TestDispenserDispatchIssuesACodeAndTriggersTheDrink(t *testing.T) {
	d, _, client := newDispenserWithFake(t)
	ctx := context.Background()

	attempt, err := d.Dispatch(ctx, newRequest())
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if !attempt.Accepted {
		t.Fatalf("el pedido no quedó aceptado: %+v", attempt)
	}
	if !sixDigitCode.MatchString(attempt.ProviderRef) {
		t.Fatalf("ProviderRef = %q, se esperaba un código de canje de seis dígitos", attempt.ProviderRef)
	}

	info, err := client.OrderInfo(ctx, attempt.ProviderRef, dispatchDeviceID)
	if err != nil {
		t.Fatalf("el proveedor no conoce el código %q: %v", attempt.ProviderRef, err)
	}
	if info.Status != batidos.OrderStatusInProgress {
		t.Fatalf("status = %d, se esperaba %d (en preparación)", info.Status, batidos.OrderStatusInProgress)
	}
	if info.OrderNo == "" {
		t.Fatalf("el pedido aceptado volvió sin orderNo: %+v", info)
	}
}

func TestDispenserDispatchKeepsTheCodeWhenTheBrewIsRejected(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(campaignPath, func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(w, `{"exchangeCodes":["000001"],"activeInfo":{"activeNo":"AD000001","activeName":"combo","exchangeType":1}}`)
	})
	mux.HandleFunc(produceCoffeePath, func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(w, `false`)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := batidos.NewClient(srv.URL, testKey, testSecret)
	d := batidos.NewDispenser(client)

	attempt, err := d.Dispatch(context.Background(), newRequest())
	if err == nil {
		t.Fatalf("se esperaba el rechazo del canje, attempt = %+v", attempt)
	}
	if attempt.Accepted {
		t.Fatalf("el canje rechazado no quedó aceptado: %+v", attempt)
	}
	if attempt.ProviderRef != "000001" {
		t.Fatalf("ProviderRef = %q, se esperaba el código emitido por la campaña (000001)", attempt.ProviderRef)
	}
}

func TestDispenserDispatchReportsAProviderRefusalAsAPortRejection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":40005,"success":false,"msg":"nonce repetido","data":null}`))
	}))
	defer srv.Close()

	client := batidos.NewClient(srv.URL, testKey, testSecret)
	d := batidos.NewDispenser(client)

	attempt, err := d.Dispatch(context.Background(), newRequest())
	if err == nil {
		t.Fatalf("se esperaba el rechazo del proveedor, attempt = %+v", attempt)
	}

	var rejection *dispenser.Rejection
	if !errors.As(err, &rejection) {
		t.Fatalf("err = %v, se esperaba un *dispenser.Rejection", err)
	}
	if rejection.Provider != "batidos" {
		t.Fatalf("Provider = %q, se esperaba \"batidos\"", rejection.Provider)
	}
	if rejection.Code != 40005 {
		t.Fatalf("Code = %d, se esperaba 40005", rejection.Code)
	}
	if rejection.HTTPStatus != http.StatusOK {
		t.Fatalf("HTTPStatus = %d, se esperaba %d", rejection.HTTPStatus, http.StatusOK)
	}
	if !strings.Contains(rejection.Message, "nonce repetido") {
		t.Fatalf("Message = %q, se esperaba el mensaje del proveedor", rejection.Message)
	}
	if !strings.Contains(rejection.Error(), "batidos") || !strings.Contains(rejection.Error(), "40005") {
		t.Fatalf("Error() = %q, se esperaba que nombre al proveedor y el código", rejection.Error())
	}

	var apiErr *batidos.APIError
	if errors.As(err, &apiErr) {
		t.Fatalf("el adaptador dejó escapar el tipo propio del proveedor: %v", err)
	}

	var zero dispenser.DispatchResult
	if attempt != zero {
		t.Fatalf("no debe inventar un intento: %+v", attempt)
	}
}

func TestDispenserReconcileFollowsTheDrinkToItsFinalState(t *testing.T) {
	d, fake, _ := newDispenserWithFake(t)
	ctx := context.Background()

	attempt, err := d.Dispatch(ctx, newRequest())
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	req := requestWithRef(attempt.ProviderRef)

	status, err := d.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("Reconcile (en preparación): %v", err)
	}
	if status.State != linestate.Brewing {
		t.Fatalf("State = %q, se esperaba %q", status.State, linestate.Brewing)
	}
	if status.ProviderOrderNo == "" {
		t.Fatalf("Reconcile volvió sin el orderNo del proveedor: %+v", status)
	}
	orderNo := status.ProviderOrderNo

	fake.Advance(attempt.ProviderRef, 2)

	status, err = d.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("Reconcile (completado): %v", err)
	}
	if status.State != linestate.Delivered {
		t.Fatalf("State = %q, se esperaba %q", status.State, linestate.Delivered)
	}
	if status.ProviderOrderNo != orderNo {
		t.Fatalf("ProviderOrderNo = %q, se esperaba %q", status.ProviderOrderNo, orderNo)
	}

	fake.Advance(attempt.ProviderRef, 0)

	status, err = d.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("Reconcile (falló): %v", err)
	}
	if status.State != linestate.Failed {
		t.Fatalf("State = %q, se esperaba %q", status.State, linestate.Failed)
	}
	if status.ProviderOrderNo != orderNo {
		t.Fatalf("ProviderOrderNo = %q, se esperaba %q", status.ProviderOrderNo, orderNo)
	}
}

func TestDispenserReconcileFailsWhenTheCodeWasNeverIssued(t *testing.T) {
	d, _, _ := newDispenserWithFake(t)

	status, err := d.Reconcile(context.Background(), requestWithRef("999999"))
	if err == nil {
		t.Fatalf("se esperaba el rechazo del proveedor, status = %+v", status)
	}

	var rejection *dispenser.Rejection
	if !errors.As(err, &rejection) {
		t.Fatalf("err = %v, se esperaba un *dispenser.Rejection", err)
	}
	if rejection.Provider != "batidos" {
		t.Fatalf("Provider = %q, se esperaba \"batidos\"", rejection.Provider)
	}
	if rejection.Code == 0 {
		t.Fatalf("Code = %d, se esperaba el código del proveedor", rejection.Code)
	}

	var apiErr *batidos.APIError
	if errors.As(err, &apiErr) {
		t.Fatalf("el adaptador dejó escapar el tipo propio del proveedor: %v", err)
	}

	var zero dispenser.Status
	if status != zero {
		t.Fatalf("no debe inventar un estado: %+v", status)
	}
}

func TestDispenserReconcileReportsUnknownProviderStatusAsInDoubt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(w, `{"orderNo":"A000042","deviceNo":"devno-1","goodsName":"goods-1","status":7,"outStockStatus":0}`)
	}))
	defer srv.Close()

	client := batidos.NewClient(srv.URL, testKey, testSecret)
	d := batidos.NewDispenser(client)

	status, err := d.Reconcile(context.Background(), requestWithRef("000001"))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if status.State != linestate.InDoubt {
		t.Fatalf("State = %q, se esperaba %q: un estado desconocido no se declara fallido", status.State, linestate.InDoubt)
	}
	if !strings.Contains(status.Reason, "7") {
		t.Fatalf("Reason = %q, se esperaba que nombre el estado desconocido 7", status.Reason)
	}
	if status.ProviderOrderNo != "A000042" {
		t.Fatalf("ProviderOrderNo = %q, se esperaba %q", status.ProviderOrderNo, "A000042")
	}
}

type capturedCampaign struct {
	ActiveName    string `json:"activeName"`
	StartTime     string `json:"startTime"`
	EndTime       string `json:"endTime"`
	ExchangeType  int    `json:"exchangeType"`
	NotifyURL     string `json:"notifyUrl"`
	DeviceTypeID  string `json:"deviceTypeId"`
	GoodsItemDtos []struct {
		GoodsID string `json:"goodsId"`
		Qty     int    `json:"qty"`
	} `json:"goodsItemDtos"`
}

func newCapturingServer(t *testing.T) (*httptest.Server, func() capturedCampaign) {
	t.Helper()

	var (
		mu   sync.Mutex
		last capturedCampaign
	)

	mux := http.NewServeMux()
	mux.HandleFunc(campaignPath, func(w http.ResponseWriter, r *http.Request) {
		var body capturedCampaign
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "cuerpo inválido", http.StatusBadRequest)
			return
		}

		mu.Lock()
		last = body
		mu.Unlock()

		writeEnvelope(w, `{"exchangeCodes":["000001"],"activeInfo":{"activeNo":"AD000001","activeName":"combo","exchangeType":1}}`)
	})
	mux.HandleFunc(produceCoffeePath, func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(w, `true`)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv, func() capturedCampaign {
		mu.Lock()
		defer mu.Unlock()

		return last
	}
}

func TestDispenserDispatchSendsTheConfiguredCampaignWindow(t *testing.T) {
	cases := []struct {
		name       string
		opts       []batidos.DispenserOption
		wantWindow time.Duration
		wantName   string
	}{
		{
			name:       "ventana y nombre por defecto",
			wantWindow: 10 * time.Minute,
			wantName:   "combo-remoto",
		},
		{
			name: "ventana y nombre configurados",
			opts: []batidos.DispenserOption{
				batidos.WithCampaignWindow(3 * time.Minute),
				batidos.WithCampaignName("combo-prueba"),
			},
			wantWindow: 3 * time.Minute,
			wantName:   "combo-prueba",
		},
		{
			name: "valores no positivos conservan el default",
			opts: []batidos.DispenserOption{
				batidos.WithCampaignWindow(0),
				batidos.WithCampaignName(""),
			},
			wantWindow: 10 * time.Minute,
			wantName:   "combo-remoto",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, last := newCapturingServer(t)
			client := batidos.NewClient(srv.URL, testKey, testSecret, batidos.WithClock(func() time.Time {
				return fixedNow
			}))
			d := batidos.NewDispenser(client, tc.opts...)

			if _, err := d.Dispatch(context.Background(), newRequest()); err != nil {
				t.Fatalf("Dispatch: %v", err)
			}

			campaign := last()

			start, err := batidos.ParseCampaignTime(campaign.StartTime)
			if err != nil {
				t.Fatalf("startTime = %q: %v", campaign.StartTime, err)
			}
			end, err := batidos.ParseCampaignTime(campaign.EndTime)
			if err != nil {
				t.Fatalf("endTime = %q: %v", campaign.EndTime, err)
			}

			if !start.Equal(fixedNow) {
				t.Fatalf("startTime = %s, se esperaba el ahora del reloj (%s)", start, fixedNow)
			}
			if got := end.Sub(start); got != tc.wantWindow {
				t.Fatalf("ventana = %s, se esperaba %s", got, tc.wantWindow)
			}
			if campaign.ActiveName != tc.wantName {
				t.Fatalf("activeName = %q, se esperaba %q", campaign.ActiveName, tc.wantName)
			}
			if campaign.ExchangeType != 1 {
				t.Fatalf("exchangeType = %d, se esperaba 1", campaign.ExchangeType)
			}
			if campaign.DeviceTypeID != dispatchDeviceTypeID {
				t.Fatalf("deviceTypeId = %q, se esperaba %q", campaign.DeviceTypeID, dispatchDeviceTypeID)
			}
			if len(campaign.GoodsItemDtos) != 1 || campaign.GoodsItemDtos[0].GoodsID != dispatchSKU {
				t.Fatalf("goodsItemDtos = %+v, se esperaba el SKU %q", campaign.GoodsItemDtos, dispatchSKU)
			}
		})
	}
}

func TestDispenserDispatchSendsTheNotifyURLOfTheOrder(t *testing.T) {
	cases := []struct {
		name string
		opts []batidos.DispenserOption
		want string
	}{
		{
			name: "sin la opción la campaña no lleva notifyUrl",
		},
		{
			name: "con la opción la campaña lleva la URL de esa orden",
			opts: []batidos.DispenserOption{batidos.WithNotifyURL(func(orderID string) string {
				return "https://kiosco.example.test/callbacks/batidos/ruta-secreta/" + orderID
			})},
			want: "https://kiosco.example.test/callbacks/batidos/ruta-secreta/order-1",
		},
		{
			name: "una función vacía conserva el comportamiento anterior",
			opts: []batidos.DispenserOption{batidos.WithNotifyURL(nil)},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, last := newCapturingServer(t)
			client := batidos.NewClient(srv.URL, testKey, testSecret, batidos.WithClock(func() time.Time {
				return fixedNow
			}))
			d := batidos.NewDispenser(client, tc.opts...)

			if _, err := d.Dispatch(context.Background(), newRequest()); err != nil {
				t.Fatalf("Dispatch: %v", err)
			}

			if got := last().NotifyURL; got != tc.want {
				t.Fatalf("notifyUrl = %q, se esperaba %q", got, tc.want)
			}
		})
	}
}

func writeEnvelope(w http.ResponseWriter, data string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"code":200,"success":true,"msg":"ok","data":` + data + `}`))
}
