package fakeapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"compras-remotas/internal/payments"
	"compras-remotas/internal/payments/mercadopago"
)

const (
	mpTestToken  = "TEST-token"
	mpTestSecret = "webhook-secret"
	mpTestPath   = "/callbacks/mercadopago/ruta-secreta"
)

type mpReceived struct {
	mu    sync.Mutex
	calls []*http.Request
	errs  []error
}

type mpRig struct {
	fake     *MercadoPago
	client   *mercadopago.Client
	admin    http.Handler
	received *mpReceived
	now      time.Time
}

func newMPRig(t *testing.T) *mpRig {
	t.Helper()
	rig := &mpRig{received: &mpReceived{}, now: time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)}

	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := mercadopago.VerifySignature(mpTestSecret, r.Header.Get("X-Signature"), r.Header.Get("X-Request-Id"), r.URL.Query().Get("data.id"), rig.now)
		rig.received.mu.Lock()
		rig.received.calls = append(rig.received.calls, r)
		rig.received.errs = append(rig.received.errs, err)
		rig.received.mu.Unlock()
		if r.URL.Path != mpTestPath || err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(webhook.Close)

	rig.fake = NewMercadoPago(mpTestToken, mpTestSecret, webhook.URL+mpTestPath)
	rig.fake.SetClock(func() time.Time { return rig.now })
	api := httptest.NewServer(rig.fake)
	t.Cleanup(api.Close)

	rig.client = mercadopago.NewClient(api.URL, mpTestToken)
	rig.admin = NewAdmin(NewBatidos("k", "s"), NewSnacks("ak", "s"), rig.fake)
	return rig
}

func (rig *mpRig) createQR(t *testing.T, orderID string) payments.Charge {
	t.Helper()
	charge, err := rig.client.CreateQR(context.Background(), payments.QRRequest{
		OrderID: orderID, PosID: "MUEBLE01POS01", AmountCents: 150050, Title: "Combo", ExpiresIn: 180 * time.Second,
	})
	if err != nil {
		t.Fatalf("CreateQR: %v", err)
	}
	return charge
}

func (rig *mpRig) adminPost(t *testing.T, path, body string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	rig.admin.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("respuesta de administración ilegible: %v", err)
	}
	return rec.Code, out
}

func TestMPFakeCreateIsIdempotentAndPending(t *testing.T) {
	rig := newMPRig(t)
	first := rig.createQR(t, "orden1")
	second := rig.createQR(t, "orden1")

	if first.ProviderID == "" || first.ProviderID != second.ProviderID || first.QRData != second.QRData {
		t.Fatalf("la misma clave de idempotencia dio dos órdenes: %+v / %+v", first, second)
	}
	if first.Status != payments.ChargePending || first.AmountCents != 150050 || first.ExternalRef != "orden1" {
		t.Fatalf("charge = %+v", first)
	}
	if other := rig.createQR(t, "orden2"); other.ProviderID == first.ProviderID {
		t.Fatal("dos órdenes distintas comparten id")
	}
}

func TestMPFakePayThroughAdminSendsSignedWebhookAndLookupSeesIt(t *testing.T) {
	rig := newMPRig(t)
	charge := rig.createQR(t, "orden1")

	code, out := rig.adminPost(t, adminMPPayPath, `{"order":"orden1"}`)
	if code != http.StatusOK || out["order_id"] != charge.ProviderID {
		t.Fatalf("pay = %d %v", code, out)
	}
	webhook := out["webhook"].(map[string]any)
	if webhook["sent"] != true || webhook["http_status"] != float64(200) {
		t.Fatalf("el webhook no fue aceptado: %v", webhook)
	}
	if strings.Contains(webhook["url"].(string), "ruta-secreta") {
		t.Fatalf("la respuesta expone el secreto de ruta: %v", webhook["url"])
	}

	if len(rig.received.calls) != 1 || rig.received.errs[0] != nil {
		t.Fatalf("avisos = %d, error de firma = %v", len(rig.received.calls), rig.received.errs)
	}
	got := rig.received.calls[0]
	if got.URL.Query().Get("data.id") != charge.ProviderID || got.URL.Query().Get("type") != "order" {
		t.Fatalf("query del aviso = %s", got.URL.RawQuery)
	}

	paid, err := rig.client.Lookup(context.Background(), charge.ProviderID)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if paid.Status != payments.ChargeApproved || paid.PaidAmountCents != 150050 || paid.ProviderStatusDetail != "accredited" {
		t.Fatalf("charge pagada = %+v", paid)
	}
}

func TestMPFakePayWithDifferentAmount(t *testing.T) {
	rig := newMPRig(t)
	charge := rig.createQR(t, "orden1")
	if code, out := rig.adminPost(t, adminMPPayPath, `{"order":"`+charge.ProviderID+`","amount_cents":100}`); code != http.StatusOK {
		t.Fatalf("pay = %d %v", code, out)
	}
	paid, _ := rig.client.Lookup(context.Background(), charge.ProviderID)
	if paid.Status != payments.ChargeApproved || paid.PaidAmountCents != 100 || paid.AmountCents != 150050 {
		t.Fatalf("charge = %+v", paid)
	}
}

func TestMPFakeExpiresByClock(t *testing.T) {
	rig := newMPRig(t)
	charge := rig.createQR(t, "orden1")

	rig.now = rig.now.Add(180 * time.Second)
	if c, _ := rig.client.Lookup(context.Background(), charge.ProviderID); c.Status != payments.ChargePending {
		t.Fatalf("vencida en el borde: %s", c.Status)
	}
	rig.now = rig.now.Add(time.Millisecond)
	if c, _ := rig.client.Lookup(context.Background(), charge.ProviderID); c.Status != payments.ChargeExpired {
		t.Fatalf("status = %s, se esperaba expired", c.Status)
	}
	if code, _ := rig.adminPost(t, adminMPPayPath, `{"order":"orden1"}`); code != http.StatusConflict {
		t.Fatalf("se pagó una orden vencida: %d", code)
	}
}

func TestMPFakeExpireThroughAdminNotifies(t *testing.T) {
	rig := newMPRig(t)
	charge := rig.createQR(t, "orden1")
	if code, out := rig.adminPost(t, adminMPExpirePath, `{"order":"orden1"}`); code != http.StatusOK {
		t.Fatalf("expire = %d %v", code, out)
	}
	if len(rig.received.calls) != 1 {
		t.Fatalf("avisos = %d", len(rig.received.calls))
	}
	if c, _ := rig.client.Lookup(context.Background(), charge.ProviderID); c.Status != payments.ChargeExpired {
		t.Fatalf("status = %s", c.Status)
	}
}

func TestMPFakeCancel(t *testing.T) {
	rig := newMPRig(t)
	charge := rig.createQR(t, "orden1")

	cancelled, err := rig.client.Cancel(context.Background(), charge.ProviderID)
	if err != nil || cancelled.Status != payments.ChargeCancelled {
		t.Fatalf("Cancel = %+v, %v", cancelled, err)
	}
	if again, err := rig.client.Cancel(context.Background(), charge.ProviderID); err != nil || again.Status != payments.ChargeCancelled {
		t.Fatalf("la segunda cancelación = %+v, %v", again, err)
	}
	if code, _ := rig.adminPost(t, adminMPPayPath, `{"order":"orden1"}`); code != http.StatusConflict {
		t.Fatalf("se pagó una orden cancelada: %d", code)
	}
}

func TestMPFakeCancelAfterPaymentIsNotCancelable(t *testing.T) {
	rig := newMPRig(t)
	charge := rig.createQR(t, "orden1")
	rig.adminPost(t, adminMPPayPath, `{"order":"orden1"}`)

	_, err := rig.client.Cancel(context.Background(), charge.ProviderID)
	if !errors.Is(err, payments.ErrNotCancelable) {
		t.Fatalf("err = %v, se esperaba ErrNotCancelable", err)
	}
}

func TestMPFakeNotifyRepeatsTheWebhook(t *testing.T) {
	rig := newMPRig(t)
	rig.createQR(t, "orden1")
	rig.adminPost(t, adminMPPayPath, `{"order":"orden1"}`)
	if code, _ := rig.adminPost(t, adminMPNotifyPath, `{"order":"orden1"}`); code != http.StatusOK {
		t.Fatalf("notify = %d", code)
	}
	if len(rig.received.calls) != 2 {
		t.Fatalf("avisos = %d", len(rig.received.calls))
	}
	if a, b := rig.received.calls[0].Header.Get("X-Request-Id"), rig.received.calls[1].Header.Get("X-Request-Id"); a == b {
		t.Fatal("dos envíos con el mismo x-request-id")
	}
}

func TestMPFakeLookupUnknown(t *testing.T) {
	rig := newMPRig(t)
	if _, err := rig.client.Lookup(context.Background(), "ORDNOEXISTE"); !errors.Is(err, payments.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if code, _ := rig.adminPost(t, adminMPPayPath, `{"order":"nada"}`); code != http.StatusNotFound {
		t.Fatalf("pay de una orden inexistente = %d", code)
	}
}

func TestMPFakeRejectsWrongToken(t *testing.T) {
	rig := newMPRig(t)
	api := httptest.NewServer(rig.fake)
	t.Cleanup(api.Close)
	_, err := mercadopago.NewClient(api.URL, "otro").Lookup(context.Background(), "ORD1")
	var apiErr *mercadopago.APIError
	if !errors.As(err, &apiErr) || apiErr.HTTPStatus != http.StatusUnauthorized {
		t.Fatalf("err = %v", err)
	}
}

func TestMPFakeRejectsCreateWithoutIdempotencyKey(t *testing.T) {
	rig := newMPRig(t)
	req := httptest.NewRequest(http.MethodPost, mpOrdersPath, strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+mpTestToken)
	rec := httptest.NewRecorder()
	rig.fake.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestMPFakeWithoutWebhookURLDoesNotSend(t *testing.T) {
	fake := NewMercadoPago(mpTestToken, mpTestSecret, "")
	api := httptest.NewServer(fake)
	t.Cleanup(api.Close)
	client := mercadopago.NewClient(api.URL, mpTestToken)
	if _, err := client.CreateQR(context.Background(), payments.QRRequest{OrderID: "o1", PosID: "P", AmountCents: 100, Title: "x", ExpiresIn: time.Minute}); err != nil {
		t.Fatal(err)
	}
	admin := NewAdmin(NewBatidos("k", "s"), NewSnacks("ak", "s"), fake)
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, adminMPPayPath, strings.NewReader(`{"order":"o1"}`)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"sent":false`) {
		t.Fatalf("respuesta = %d %s", rec.Code, rec.Body.String())
	}
}

func TestMPParseISODuration(t *testing.T) {
	ok := map[string]time.Duration{"PT3M": 3 * time.Minute, "PT90S": 90 * time.Second, "PT1H2M3S": time.Hour + 2*time.Minute + 3*time.Second}
	for raw, want := range ok {
		if got, err := mpParseISODuration(raw); err != nil || got != want {
			t.Errorf("%s = %s, %v", raw, got, err)
		}
	}
	for _, raw := range []string{"", "PT", "3M", "PTM", "PT3X", "P1D"} {
		if _, err := mpParseISODuration(raw); err == nil {
			t.Errorf("%q aceptado", raw)
		}
	}
}
