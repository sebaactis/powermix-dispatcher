package preflight_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"compras-remotas/internal/dispenser/batidos"
	"compras-remotas/internal/dispenser/snacks"
	"compras-remotas/internal/preflight"
)

const (
	testGuard  = 5 * time.Second
	testBudget = 50 * time.Millisecond
)

func testRequest() preflight.Request {
	return preflight.Request{
		BatidosDeviceID:  testBatidosDeviceID,
		BatidosSKU:       testBatidosSKU,
		SnacksDeviceCode: testSnacksDeviceCode,
		SnacksSKU:        testSnacksSKU,
	}
}

type barrier struct {
	mu      sync.Mutex
	arrived int
	open    chan struct{}
}

func newBarrier() *barrier {
	return &barrier{open: make(chan struct{})}
}

func (b *barrier) wait(ctx context.Context) error {
	b.mu.Lock()
	b.arrived++
	if b.arrived == 2 {
		close(b.open)
	}
	b.mu.Unlock()

	select {
	case <-b.open:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type batidosBarrierStub struct {
	barrier *barrier
	device  batidos.Device
	goods   []batidos.DeviceGoods
	calls   int
}

func (s *batidosBarrierStub) Device(ctx context.Context, deviceID string) (batidos.Device, error) {
	s.calls++
	if err := s.barrier.wait(ctx); err != nil {
		return batidos.Device{}, err
	}

	return s.device, nil
}

func (s *batidosBarrierStub) DeviceGoods(ctx context.Context, deviceID string) ([]batidos.DeviceGoods, error) {
	return s.goods, nil
}

type snacksBarrierStub struct {
	barrier *barrier
	page    snacks.DevicesPage
	slots   []snacks.Slot
	calls   int
}

func (s *snacksBarrierStub) Devices(ctx context.Context, query snacks.PageQuery) (snacks.DevicesPage, error) {
	s.calls++
	if err := s.barrier.wait(ctx); err != nil {
		return snacks.DevicesPage{}, err
	}

	return s.page, nil
}

func (s *snacksBarrierStub) Slots(ctx context.Context, deviceCode string) ([]snacks.Slot, error) {
	return s.slots, nil
}

func TestCheck_ConsultaLasDosMaquinasALaVez(t *testing.T) {
	bState := healthyBatidos()
	sState := healthySnacks()
	shared := newBarrier()

	bStub := &batidosBarrierStub{barrier: shared, device: bState.device, goods: bState.goods}
	sStub := &snacksBarrierStub{barrier: shared, page: sState.page, slots: sState.slots}
	checker := preflight.New(bStub, sStub)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	type outcome struct {
		result preflight.Result
		err    error
	}

	done := make(chan outcome, 1)
	go func() {
		result, err := checker.Check(ctx, testRequest())
		done <- outcome{result: result, err: err}
	}()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("Check no pudo completarse con las dos máquinas esperándose: devolvió %v (batidos entró %d vez, snacks %d vez)",
				got.err, bStub.calls, sStub.calls)
		}
		if !got.result.Sellable {
			t.Fatalf("las dos máquinas están sanas y el resultado trajo las razones %v", got.result.Reasons)
		}
		if bStub.calls != 1 || sStub.calls != 1 {
			t.Fatalf("se esperaba una consulta de cada máquina, se hizo batidos=%d snacks=%d", bStub.calls, sStub.calls)
		}
	case <-time.After(testGuard):
		t.Fatalf("Check no terminó en %v con las dos máquinas esperándose: el chequeo sigue siendo secuencial", testGuard)
	}
}

type blockedBatidosStub struct {
	calls int
}

func (s *blockedBatidosStub) Device(ctx context.Context, deviceID string) (batidos.Device, error) {
	s.calls++
	<-ctx.Done()

	return batidos.Device{}, ctx.Err()
}

func (s *blockedBatidosStub) DeviceGoods(ctx context.Context, deviceID string) ([]batidos.DeviceGoods, error) {
	<-ctx.Done()

	return nil, ctx.Err()
}

type blockedSnacksStub struct {
	calls int
}

func (s *blockedSnacksStub) Devices(ctx context.Context, query snacks.PageQuery) (snacks.DevicesPage, error) {
	s.calls++
	<-ctx.Done()

	return snacks.DevicesPage{}, ctx.Err()
}

func (s *blockedSnacksStub) Slots(ctx context.Context, deviceCode string) ([]snacks.Slot, error) {
	<-ctx.Done()

	return nil, ctx.Err()
}

func TestCheck_RespetaElPresupuesto(t *testing.T) {
	checker := preflight.New(&blockedBatidosStub{}, &blockedSnacksStub{}, preflight.WithBudget(testBudget))

	type outcome struct {
		result  preflight.Result
		err     error
		elapsed time.Duration
	}

	done := make(chan outcome, 1)
	go func() {
		start := time.Now()
		result, err := checker.Check(context.Background(), testRequest())
		done <- outcome{result: result, err: err, elapsed: time.Since(start)}
	}()

	select {
	case got := <-done:
		if !errors.Is(got.err, context.DeadlineExceeded) {
			t.Fatalf("Check devolvió el error %v, se esperaba el plazo agotado, igual que un proveedor que no contesta", got.err)
		}
		if got.result.Sellable || len(got.result.Reasons) != 0 {
			t.Fatalf("un plazo agotado no puede devolver veredicto, se devolvió %+v", got.result)
		}
		if got.elapsed > testBudget+time.Second {
			t.Fatalf("Check tardó %v, más que el presupuesto de %v más un segundo de margen", got.elapsed, testBudget)
		}
	case <-time.After(testGuard):
		t.Fatalf("Check no terminó en %v: el chequeo no se impone ningún presupuesto propio", testGuard)
	}
}

type batidosAfterSnacksStub struct {
	device     batidos.Device
	goods      []batidos.DeviceGoods
	snacksDone <-chan struct{}
}

func (s *batidosAfterSnacksStub) Device(ctx context.Context, deviceID string) (batidos.Device, error) {
	select {
	case <-s.snacksDone:
		return s.device, nil
	case <-ctx.Done():
		return batidos.Device{}, ctx.Err()
	}
}

func (s *batidosAfterSnacksStub) DeviceGoods(ctx context.Context, deviceID string) ([]batidos.DeviceGoods, error) {
	return s.goods, nil
}

type snacksFirstStub struct {
	page  snacks.DevicesPage
	slots []snacks.Slot
	done  chan struct{}
}

func (s *snacksFirstStub) Devices(ctx context.Context, query snacks.PageQuery) (snacks.DevicesPage, error) {
	return s.page, nil
}

func (s *snacksFirstStub) Slots(ctx context.Context, deviceCode string) ([]snacks.Slot, error) {
	close(s.done)

	return s.slots, nil
}

func TestCheck_ElOrdenDeLasRazonesNoDependeDeCualTerminaAntes(t *testing.T) {
	bState := healthyBatidos()
	sState := healthySnacks()
	bState.device.Status = batidos.DeviceStatusOffline
	bState.device.ScarceStatus = batidos.ScarceStatusOutOfStock
	bState.goods[0].SoldOut = 1
	sState.page.Items[0].OnlineStatus = snacks.OnlineStatusOffline
	sState.page.Items[0].SellStatus = snacks.SellStatusSuspended
	sState.slots = nil

	snacksDone := make(chan struct{})
	bStub := &batidosAfterSnacksStub{device: bState.device, goods: bState.goods, snacksDone: snacksDone}
	sStub := &snacksFirstStub{page: sState.page, slots: sState.slots, done: snacksDone}
	checker := preflight.New(bStub, sStub)

	result, err := checker.Check(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Check devolvió %v, se esperaba un resultado con razones", err)
	}

	want := []preflight.Reason{
		preflight.ReasonBatidosDeviceOffline,
		preflight.ReasonBatidosOutOfSupplies,
		preflight.ReasonBatidosSKUSoldOut,
		preflight.ReasonSnacksDeviceOffline,
		preflight.ReasonSnacksSaleNotAllowed,
		preflight.ReasonSnacksSKUNotInAnySlot,
	}
	if !slices.Equal(result.Reasons, want) {
		t.Fatalf("razones %v, se esperaba el orden de siempre %v aunque snacks haya terminado antes que batidos", result.Reasons, want)
	}
	if result.Sellable {
		t.Fatal("dos máquinas con problemas no pueden dar un resultado vendible")
	}
}
