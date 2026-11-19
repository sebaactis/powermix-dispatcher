package order

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/linestate"
	"compras-remotas/internal/machine"
	"compras-remotas/internal/notify"
	"compras-remotas/internal/payments"
	"compras-remotas/internal/store"
)

type fakeGateway struct {
	mu        sync.Mutex
	charges   map[string]payments.Charge
	created   []payments.QRRequest
	lookups   int
	cancels   int
	createErr error
	cancelErr error
	next      int
}

func newFakeGateway() *fakeGateway {
	return &fakeGateway{charges: make(map[string]payments.Charge)}
}

func (g *fakeGateway) CreateQR(_ context.Context, req payments.QRRequest) (payments.Charge, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.created = append(g.created, req)
	if g.createErr != nil {
		return payments.Charge{}, g.createErr
	}
	for _, c := range g.charges {
		if c.ExternalRef == req.OrderID {
			return c, nil
		}
	}
	g.next++
	c := payments.Charge{
		ProviderID:  fmt.Sprintf("ORD%03d", g.next),
		ExternalRef: req.OrderID,
		QRData:      fmt.Sprintf("qr-%d", g.next),
		Status:      payments.ChargePending,
		AmountCents: req.AmountCents,
	}
	g.charges[c.ProviderID] = c
	return c, nil
}

func (g *fakeGateway) Lookup(_ context.Context, id string) (payments.Charge, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.lookups++
	c, ok := g.charges[id]
	if !ok {
		return payments.Charge{}, payments.ErrNotFound
	}
	return c, nil
}

func (g *fakeGateway) Cancel(_ context.Context, id string) (payments.Charge, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cancels++
	if g.cancelErr != nil {
		return payments.Charge{}, g.cancelErr
	}
	c := g.charges[id]
	c.Status = payments.ChargeCancelled
	g.charges[id] = c
	return c, nil
}

func (g *fakeGateway) set(id string, status payments.ChargeStatus, paid int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	c := g.charges[id]
	c.Status = status
	c.PaidAmountCents = paid
	c.ProviderStatus = string(status)
	g.charges[id] = c
}

type countingDispenser struct {
	mu       sync.Mutex
	dispatch int
	release  chan struct{}
}

func (d *countingDispenser) Check(context.Context, machine.Ref) (dispenser.Availability, error) {
	return dispenser.Availability{Available: true}, nil
}

func (d *countingDispenser) Dispatch(ctx context.Context, req dispenser.Request) (dispenser.DispatchResult, error) {
	if d.release != nil {
		<-d.release
	}
	d.mu.Lock()
	d.dispatch++
	d.mu.Unlock()
	return dispenser.DispatchResult{Accepted: true, ProviderRef: "ref-" + req.LineID}, nil
}

func (d *countingDispenser) Reconcile(context.Context, dispenser.Request) (dispenser.Status, error) {
	return dispenser.Status{State: linestate.Sent}, nil
}

func (d *countingDispenser) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dispatch
}

type recordingNotifier struct {
	mu     sync.Mutex
	events []notify.Event
}

func (n *recordingNotifier) Notify(_ context.Context, ev notify.Event) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.events = append(n.events, ev)
	return nil
}

func (n *recordingNotifier) kinds() []notify.Kind {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]notify.Kind, 0, len(n.events))
	for _, ev := range n.events {
		out = append(out, ev.Kind)
	}
	return out
}

type chargeRig struct {
	engine   *Engine
	store    *store.Memory
	gateway  *fakeGateway
	machine  *countingDispenser
	notifier *recordingNotifier
	now      time.Time
}

func newChargeRig(t *testing.T, opts ...Option) *chargeRig {
	t.Helper()
	rig := &chargeRig{
		store:    store.NewMemory(),
		gateway:  newFakeGateway(),
		machine:  &countingDispenser{},
		notifier: &recordingNotifier{},
		now:      time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC),
	}
	base := []Option{
		WithClock(func() time.Time { return rig.now }),
		WithNotifier(rig.notifier),
		WithQRPayments("mercadopago", rig.gateway),
		WithBackground(func(task func()) { task() }),
	}
	rig.engine = New(rig.store, map[machine.Kind]dispenser.Dispenser{machine.Snacks: rig.machine}, append(base, opts...)...)
	return rig
}

func (rig *chargeRig) place(t *testing.T, key string) store.Order {
	t.Helper()
	placed, err := rig.engine.CreateOrder(context.Background(), Draft{
		KioskID: "k1", ComboID: "c1", ClientKey: key, AmountCents: 150050, Currency: "ARS",
		Items: []Item{
			{SKU: "s1", AmountCents: 100000, Machine: machine.Ref{KioskID: "k1", Kind: machine.Snacks, MID: "m1"}},
			{SKU: "s2", AmountCents: 50050, Machine: machine.Ref{KioskID: "k1", Kind: machine.Snacks, MID: "m1"}},
		},
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	return placed
}

func (rig *chargeRig) qr(t *testing.T, orderID string) store.PaymentIntent {
	t.Helper()
	intent, err := rig.engine.RequestQR(context.Background(), orderID, "POS1")
	if err != nil {
		t.Fatalf("RequestQR: %v", err)
	}
	return intent
}

func (rig *chargeRig) order(t *testing.T, id string) store.Order {
	t.Helper()
	loaded, err := rig.store.LoadOrder(context.Background(), id)
	if err != nil {
		t.Fatalf("LoadOrder: %v", err)
	}
	return loaded
}

func (rig *chargeRig) intent(t *testing.T, orderID string) store.PaymentIntent {
	t.Helper()
	intent, err := rig.store.PaymentIntentByOrder(context.Background(), orderID)
	if err != nil {
		t.Fatalf("PaymentIntentByOrder: %v", err)
	}
	return intent
}

func TestRequestQRCreatesOnceAndReturnsTheSameIntent(t *testing.T) {
	rig := newChargeRig(t)
	placed := rig.place(t, "a")

	first := rig.qr(t, placed.ID)
	second := rig.qr(t, placed.ID)

	if len(rig.gateway.created) != 1 {
		t.Fatalf("CreateQR llamado %d veces", len(rig.gateway.created))
	}
	req := rig.gateway.created[0]
	if req.OrderID != placed.ID || req.PosID != "POS1" || req.AmountCents != 150050 || req.ExpiresIn != defaultQRExpiry {
		t.Fatalf("pedido = %+v", req)
	}
	if first.ID != second.ID || first.ProviderID != "ORD001" || first.QRData != "qr-1" || first.Provider != "mercadopago" {
		t.Fatalf("intents = %+v / %+v", first, second)
	}
	if first.Status != store.PaymentPending || !first.ExpiresAt.Equal(rig.now.Add(defaultQRExpiry)) || first.AmountCents != 150050 {
		t.Fatalf("intent = %+v", first)
	}
}

func TestRequestQRWithoutGateway(t *testing.T) {
	st := store.NewMemory()
	engine := New(st, map[machine.Kind]dispenser.Dispenser{machine.Snacks: &countingDispenser{}})
	if _, err := engine.RequestQR(context.Background(), "x", "POS1"); !errors.Is(err, ErrPaymentsDisabled) {
		t.Fatalf("err = %v", err)
	}
	if err := engine.SyncPayment(context.Background(), "ORD1"); !errors.Is(err, ErrPaymentsDisabled) {
		t.Fatalf("err = %v", err)
	}
	if err := engine.SyncPendingPayments(context.Background(), 10); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestRequestQRRefusesAnOrderThatIsNotWaitingForPayment(t *testing.T) {
	rig := newChargeRig(t)
	placed := rig.place(t, "a")
	if err := rig.engine.MarkPaid(context.Background(), placed.ID, rig.now); err != nil {
		t.Fatalf("MarkPaid: %v", err)
	}
	if _, err := rig.engine.RequestQR(context.Background(), placed.ID, "POS1"); !errors.Is(err, ErrNotPayable) {
		t.Fatalf("err = %v", err)
	}
	if len(rig.gateway.created) != 0 {
		t.Fatal("se pidió un QR para una orden pagada")
	}
}

func TestRequestQRGatewayFailureStoresNothingAndRetries(t *testing.T) {
	rig := newChargeRig(t)
	placed := rig.place(t, "a")
	rig.gateway.createErr = errors.New("mercado pago caído")

	if _, err := rig.engine.RequestQR(context.Background(), placed.ID, "POS1"); err == nil {
		t.Fatal("se aceptó un QR que no se creó")
	}
	if _, err := rig.store.PaymentIntentByOrder(context.Background(), placed.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("quedó un pago guardado: %v", err)
	}

	rig.gateway.createErr = nil
	if intent := rig.qr(t, placed.ID); intent.ProviderID == "" {
		t.Fatal("el reintento no creó el QR")
	}
}

func TestSyncPaymentPaysAndDispatchesOnce(t *testing.T) {
	rig := newChargeRig(t)
	placed := rig.place(t, "a")
	intent := rig.qr(t, placed.ID)
	rig.gateway.set(intent.ProviderID, payments.ChargeApproved, 150050)

	if err := rig.engine.SyncPayment(context.Background(), intent.ProviderID); err != nil {
		t.Fatalf("SyncPayment: %v", err)
	}
	if err := rig.engine.SyncPayment(context.Background(), intent.ProviderID); err != nil {
		t.Fatalf("webhook repetido: %v", err)
	}

	if got := rig.machine.count(); got != 2 {
		t.Fatalf("despachos = %d, se esperaban 2 (uno por línea)", got)
	}
	loaded := rig.order(t, placed.ID)
	if loaded.Status != store.OrderDispatching || !loaded.PaidAt.Equal(rig.now) {
		t.Fatalf("orden = %s pagada %s", loaded.Status, loaded.PaidAt)
	}
	saved := rig.intent(t, placed.ID)
	if saved.Status != store.PaymentApproved || saved.PaidAmountCents != 150050 || !saved.ApprovedAt.Equal(rig.now) {
		t.Fatalf("pago = %+v", saved)
	}
	if len(rig.notifier.kinds()) != 0 {
		t.Fatalf("avisos = %v", rig.notifier.kinds())
	}
}

func TestSyncPaymentWrongAmountDoesNotDispatchAndNotifiesOnce(t *testing.T) {
	for _, paid := range []int64{150049, 150051} {
		t.Run(fmt.Sprint(paid), func(t *testing.T) {
			rig := newChargeRig(t)
			placed := rig.place(t, "a")
			intent := rig.qr(t, placed.ID)
			rig.gateway.set(intent.ProviderID, payments.ChargeApproved, paid)

			for i := 0; i < 2; i++ {
				if err := rig.engine.SyncPayment(context.Background(), intent.ProviderID); err != nil {
					t.Fatalf("SyncPayment: %v", err)
				}
			}

			if rig.machine.count() != 0 {
				t.Fatal("se despachó con el monto equivocado")
			}
			if s := rig.order(t, placed.ID).Status; s != store.OrderPendingPayment {
				t.Fatalf("orden = %s, se esperaba pending_payment", s)
			}
			saved := rig.intent(t, placed.ID)
			if saved.Status != store.PaymentAmountMismatch || saved.PaidAmountCents != paid {
				t.Fatalf("pago = %+v", saved)
			}
			if k := rig.notifier.kinds(); len(k) != 1 || k[0] != notify.KindPaymentMismatch {
				t.Fatalf("avisos = %v", k)
			}
			ev := rig.notifier.events[0]
			if ev.KioskID != "k1" {
				t.Fatalf("el aviso de pago tiene que decir en qué mueble pasó: KioskID=%q", ev.KioskID)
			}
			if !strings.Contains(ev.Detail, notify.Pesos(paid)) || !strings.Contains(ev.Detail, "$1.500,50") {
				t.Fatalf("el detalle tiene que dar los montos en pesos: %q", ev.Detail)
			}
		})
	}
}

func TestSyncPaymentExpiredOrCancelledCancelsTheOrder(t *testing.T) {
	cases := map[payments.ChargeStatus]store.PaymentStatus{
		payments.ChargeExpired:   store.PaymentExpired,
		payments.ChargeCancelled: store.PaymentCancelled,
	}
	for charge, want := range cases {
		t.Run(string(charge), func(t *testing.T) {
			rig := newChargeRig(t)
			placed := rig.place(t, "a")
			intent := rig.qr(t, placed.ID)
			rig.gateway.set(intent.ProviderID, charge, 0)

			if err := rig.engine.SyncPayment(context.Background(), intent.ProviderID); err != nil {
				t.Fatalf("SyncPayment: %v", err)
			}
			if s := rig.order(t, placed.ID).Status; s != store.OrderCancelled {
				t.Fatalf("orden = %s", s)
			}
			if s := rig.intent(t, placed.ID).Status; s != want {
				t.Fatalf("pago = %s, se esperaba %s", s, want)
			}
		})
	}
}

func TestSyncPaymentPendingChangesNothing(t *testing.T) {
	rig := newChargeRig(t)
	placed := rig.place(t, "a")
	intent := rig.qr(t, placed.ID)
	if err := rig.engine.SyncPayment(context.Background(), intent.ProviderID); err != nil {
		t.Fatalf("SyncPayment: %v", err)
	}
	if rig.order(t, placed.ID).Status != store.OrderPendingPayment || rig.intent(t, placed.ID).Status != store.PaymentPending {
		t.Fatal("un cobro pendiente cambió algo")
	}
}

func TestSyncPaymentUnknownStatusNotifiesOnceAndTouchesNothing(t *testing.T) {
	rig := newChargeRig(t)
	placed := rig.place(t, "a")
	intent := rig.qr(t, placed.ID)
	rig.gateway.set(intent.ProviderID, payments.ChargeUnknown, 150050)

	for i := 0; i < 2; i++ {
		if err := rig.engine.SyncPayment(context.Background(), intent.ProviderID); err != nil {
			t.Fatalf("SyncPayment: %v", err)
		}
	}
	if k := rig.notifier.kinds(); len(k) != 1 || k[0] != notify.KindPaymentUnknown {
		t.Fatalf("avisos = %v", k)
	}
	if rig.order(t, placed.ID).Status != store.OrderPendingPayment || rig.intent(t, placed.ID).Status != store.PaymentPending || rig.machine.count() != 0 {
		t.Fatal("un estado desconocido cambió algo")
	}
}

func TestSyncPaymentRejectsForeignCharges(t *testing.T) {
	rig := newChargeRig(t)
	placed := rig.place(t, "a")
	intent := rig.qr(t, placed.ID)

	if err := rig.engine.SyncPayment(context.Background(), "ORDNOEXISTE"); !errors.Is(err, ErrUnknownCharge) {
		t.Fatalf("cobro inexistente: %v", err)
	}

	rig.gateway.charges["ORDAJENA"] = payments.Charge{ProviderID: "ORDAJENA", ExternalRef: "otra-orden", Status: payments.ChargeApproved, PaidAmountCents: 150050, AmountCents: 150050}
	if err := rig.engine.SyncPayment(context.Background(), "ORDAJENA"); !errors.Is(err, ErrUnknownCharge) {
		t.Fatalf("cobro de otra orden: %v", err)
	}

	rig.gateway.charges["ORDDOBLE"] = payments.Charge{ProviderID: "ORDDOBLE", ExternalRef: placed.ID, Status: payments.ChargeApproved, PaidAmountCents: 150050, AmountCents: 150050}
	if err := rig.engine.SyncPayment(context.Background(), "ORDDOBLE"); !errors.Is(err, ErrUnknownCharge) {
		t.Fatalf("segundo cobro de la misma orden: %v", err)
	}

	if rig.machine.count() != 0 || rig.intent(t, placed.ID).ProviderID != intent.ProviderID {
		t.Fatal("un cobro ajeno cambió algo")
	}
}

func TestSyncPaymentFinishesWhatACrashLeftHalfDone(t *testing.T) {
	rig := newChargeRig(t)
	placed := rig.place(t, "a")
	intent := rig.qr(t, placed.ID)

	if _, _, err := rig.engine.claimPaid(context.Background(), placed.ID, rig.now); err != nil {
		t.Fatalf("claimPaid: %v", err)
	}
	rig.gateway.set(intent.ProviderID, payments.ChargeApproved, 150050)

	if err := rig.engine.SyncPayment(context.Background(), intent.ProviderID); err != nil {
		t.Fatalf("SyncPayment: %v", err)
	}
	if rig.machine.count() != 0 {
		t.Fatal("se despachó dos veces: el despacho de una orden ya pagada es del barrido de líneas")
	}
	if s := rig.intent(t, placed.ID).Status; s != store.PaymentApproved {
		t.Fatalf("pago = %s", s)
	}
}

func TestSyncPaymentPaidAfterCancelNotifies(t *testing.T) {
	rig := newChargeRig(t)
	placed := rig.place(t, "a")
	intent := rig.qr(t, placed.ID)

	loaded := rig.order(t, placed.ID)
	loaded.Status = store.OrderCancelled
	if _, err := rig.store.SaveOrder(context.Background(), loaded); err != nil {
		t.Fatal(err)
	}
	rig.gateway.set(intent.ProviderID, payments.ChargeApproved, 150050)

	if err := rig.engine.SyncPayment(context.Background(), intent.ProviderID); err != nil {
		t.Fatalf("SyncPayment: %v", err)
	}
	if rig.machine.count() != 0 || rig.order(t, placed.ID).Status != store.OrderCancelled {
		t.Fatal("se reabrió una orden cancelada")
	}
	if k := rig.notifier.kinds(); len(k) != 1 || k[0] != notify.KindPaymentUnknown {
		t.Fatalf("avisos = %v", k)
	}
}

func TestSyncPaymentAnswersBeforeTheDispatchEnds(t *testing.T) {
	rig := newChargeRig(t, WithBackground(func(task func()) { go task() }))
	rig.machine.release = make(chan struct{})
	placed := rig.place(t, "a")
	intent := rig.qr(t, placed.ID)
	rig.gateway.set(intent.ProviderID, payments.ChargeApproved, 150050)

	if err := rig.engine.SyncPayment(context.Background(), intent.ProviderID); err != nil {
		t.Fatalf("SyncPayment: %v", err)
	}
	if rig.machine.count() != 0 {
		t.Fatal("el despacho terminó antes de responder")
	}
	if s := rig.order(t, placed.ID).Status; s != store.OrderDispatching {
		t.Fatalf("orden = %s, se esperaba dispatching antes de responder", s)
	}

	close(rig.machine.release)
	rig.engine.Wait()
	if rig.machine.count() != 2 {
		t.Fatalf("despachos = %d después de Wait", rig.machine.count())
	}
}

func TestBackgroundErrorsAreReported(t *testing.T) {
	var mu sync.Mutex
	reported := map[string]error{}
	rig := newChargeRig(t, WithBackgroundErrors(func(orderID string, err error) {
		mu.Lock()
		reported[orderID] = err
		mu.Unlock()
	}))
	rig.engine.spawn("o1", func() error { return errors.New("boom") })
	rig.engine.spawn("o2", func() error { return nil })
	rig.engine.Wait()
	if len(reported) != 1 || reported["o1"] == nil {
		t.Fatalf("reportados = %v", reported)
	}
}

func TestSyncPendingPaymentsSkipsYoungIntents(t *testing.T) {
	rig := newChargeRig(t)
	placed := rig.place(t, "a")
	intent := rig.qr(t, placed.ID)
	rig.gateway.set(intent.ProviderID, payments.ChargeApproved, 150050)

	rig.now = rig.now.Add(paymentReconcileAge - time.Millisecond)
	if err := rig.engine.SyncPendingPayments(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if rig.gateway.lookups != 0 {
		t.Fatal("se consultó un QR recién mostrado")
	}

	rig.now = rig.now.Add(time.Millisecond)
	if err := rig.engine.SyncPendingPayments(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if rig.intent(t, placed.ID).Status != store.PaymentApproved || rig.machine.count() != 2 {
		t.Fatal("el barrido no recuperó el pago que el webhook no trajo")
	}
}

func TestSyncPendingPaymentsCancelsOnlyAfterTheGrace(t *testing.T) {
	rig := newChargeRig(t)
	placed := rig.place(t, "a")
	rig.qr(t, placed.ID)

	rig.now = rig.now.Add(defaultQRExpiry + paymentCancelGrace)
	if err := rig.engine.SyncPendingPayments(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if rig.gateway.cancels != 0 || rig.order(t, placed.ID).Status != store.OrderPendingPayment {
		t.Fatal("se canceló dentro del margen")
	}

	rig.now = rig.now.Add(time.Millisecond)
	if err := rig.engine.SyncPendingPayments(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if rig.gateway.cancels != 1 {
		t.Fatalf("cancelaciones = %d", rig.gateway.cancels)
	}
	if rig.order(t, placed.ID).Status != store.OrderCancelled || rig.intent(t, placed.ID).Status != store.PaymentCancelled {
		t.Fatal("la orden no obedeció la cancelación de Mercado Pago")
	}
}

func TestSyncPendingPaymentsObeysMercadoPagoWhenItRefusesToCancel(t *testing.T) {
	rig := newChargeRig(t)
	placed := rig.place(t, "a")
	intent := rig.qr(t, placed.ID)

	rig.now = rig.now.Add(defaultQRExpiry + paymentCancelGrace + time.Second)
	rig.gateway.cancelErr = fmt.Errorf("%w: no está en created", payments.ErrNotCancelable)

	if err := rig.engine.SyncPendingPayments(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if rig.order(t, placed.ID).Status != store.OrderPendingPayment {
		t.Fatal("se canceló por nuestro reloj aunque Mercado Pago no canceló")
	}

	rig.gateway.set(intent.ProviderID, payments.ChargeApproved, 150050)
	if err := rig.engine.SyncPendingPayments(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if rig.machine.count() != 2 || rig.intent(t, placed.ID).Status != store.PaymentApproved {
		t.Fatal("el pago tardío no se despachó")
	}
}

func TestSyncPendingPaymentsReportsGatewayErrorsAndContinues(t *testing.T) {
	rig := newChargeRig(t)
	first := rig.place(t, "a")
	second := rig.place(t, "b")
	broken := rig.qr(t, first.ID)
	healthy := rig.qr(t, second.ID)
	delete(rig.gateway.charges, broken.ProviderID)
	rig.gateway.set(healthy.ProviderID, payments.ChargeApproved, 150050)

	rig.now = rig.now.Add(time.Minute)
	err := rig.engine.SyncPendingPayments(context.Background(), 10)
	if !errors.Is(err, payments.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if rig.intent(t, second.ID).Status != store.PaymentApproved {
		t.Fatal("un error en un pago frenó a los demás")
	}
}

func TestAClosedPaymentIgnoresLaterProviderStates(t *testing.T) {
	rig := newChargeRig(t)
	placed := rig.place(t, "a")
	intent := rig.qr(t, placed.ID)
	rig.gateway.set(intent.ProviderID, payments.ChargeApproved, 150050)
	if err := rig.engine.SyncPayment(context.Background(), intent.ProviderID); err != nil {
		t.Fatal(err)
	}

	rig.gateway.set(intent.ProviderID, payments.ChargeUnknown, 150050)
	if err := rig.engine.SyncPayment(context.Background(), intent.ProviderID); err != nil {
		t.Fatal(err)
	}
	if k := rig.notifier.kinds(); len(k) != 0 {
		t.Fatalf("un reembolso manual posterior generó avisos: %v", k)
	}
}
