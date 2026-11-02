package preflight_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"compras-remotas/internal/dispenser/batidos"
	"compras-remotas/internal/dispenser/snacks"
	"compras-remotas/internal/preflight"
)

const (
	testBatidosDeviceID  = "dev-batidos-1"
	testBatidosSKU       = "sku-batido-1"
	testSnacksDeviceCode = "snacks-code-1"
	testSnacksSKU        = "sku-snack-1"
)

var errProvider = errors.New("el proveedor no respondió")

type batidosStub struct {
	device      batidos.Device
	goods       []batidos.DeviceGoods
	deviceErr   error
	goodsErr    error
	deviceCalls int
	goodsCalls  int
}

func (s *batidosStub) Device(ctx context.Context, deviceID string) (batidos.Device, error) {
	s.deviceCalls++
	if s.deviceErr != nil {
		return batidos.Device{}, s.deviceErr
	}
	return s.device, nil
}

func (s *batidosStub) DeviceGoods(ctx context.Context, deviceID string) ([]batidos.DeviceGoods, error) {
	s.goodsCalls++
	if s.goodsErr != nil {
		return nil, s.goodsErr
	}
	return s.goods, nil
}

type snacksStub struct {
	page        snacks.DevicesPage
	slots       []snacks.Slot
	pageErr     error
	slotsErr    error
	pageCalls   int
	slotsCalls  int
	gotPage     int
	gotPageSize int
}

func (s *snacksStub) Devices(ctx context.Context, page, pageSize int, deviceCode string) (snacks.DevicesPage, error) {
	s.pageCalls++
	s.gotPage = page
	s.gotPageSize = pageSize
	if s.pageErr != nil {
		return snacks.DevicesPage{}, s.pageErr
	}
	return s.page, nil
}

func (s *snacksStub) Slots(ctx context.Context, deviceCode string) ([]snacks.Slot, error) {
	s.slotsCalls++
	if s.slotsErr != nil {
		return nil, s.slotsErr
	}
	return s.slots, nil
}

type batidosState struct {
	device    batidos.Device
	goods     []batidos.DeviceGoods
	deviceErr error
	goodsErr  error
}

type snacksState struct {
	page     snacks.DevicesPage
	slots    []snacks.Slot
	pageErr  error
	slotsErr error
}

func healthyBatidos() batidosState {
	return batidosState{
		device: batidos.Device{
			ID:              testBatidosDeviceID,
			DeviceNo:        "devno-1",
			Status:          batidos.DeviceStatusOnline,
			DeviceRunStatus: batidos.DeviceRunStatusNormal,
			ScarceStatus:    batidos.ScarceStatusNormal,
		},
		goods: []batidos.DeviceGoods{{ID: testBatidosSKU, No: "no-1", Name: "Batido", Price: 2500, SoldOut: 0, Listing: true}},
	}
}

func healthySnacks() snacksState {
	return snacksState{
		page: snacks.DevicesPage{
			Page:     1,
			PageSize: 10,
			Total:    1,
			Items: []snacks.Device{{
				DeviceCode:    testSnacksDeviceCode,
				OnlineStatus:  snacks.OnlineStatusOnline,
				SellStatus:    snacks.SellStatusNormal,
				FaultState:    snacks.FaultStateNormal,
				LockType:      snacks.LockTypeNormal,
				StockoutState: snacks.StockoutStateNormal,
			}},
		},
		slots: []snacks.Slot{{
			SlotID:      "1",
			SlotNo:      "A1",
			SlotStatus:  snacks.SlotStatusNormal,
			Stock:       4,
			Capacity:    8,
			ProductID:   testSnacksSKU,
			ProductName: "Snack",
		}},
	}
}

func TestCheck(t *testing.T) {
	cases := []struct {
		name         string
		mutate       func(*batidosState, *snacksState)
		wantSellable bool
		wantReasons  []preflight.Reason
		wantErr      error
	}{
		{
			name:         "las dos máquinas sanas y los dos SKU disponibles",
			wantSellable: true,
		},
		{
			name: "batidos fuera de línea",
			mutate: func(b *batidosState, s *snacksState) {
				b.device.Status = batidos.DeviceStatusOffline
			},
			wantReasons: []preflight.Reason{preflight.ReasonBatidosDeviceOffline},
		},
		{
			name: "batidos con falla de funcionamiento",
			mutate: func(b *batidosState, s *snacksState) {
				b.device.DeviceRunStatus = batidos.DeviceRunStatusAbnormal
			},
			wantReasons: []preflight.Reason{preflight.ReasonBatidosDeviceFaulted},
		},
		{
			name: "batidos sin insumos",
			mutate: func(b *batidosState, s *snacksState) {
				b.device.ScarceStatus = batidos.ScarceStatusOutOfStock
			},
			wantReasons: []preflight.Reason{preflight.ReasonBatidosOutOfSupplies},
		},
		{
			name: "SKU de batido ausente del catálogo",
			mutate: func(b *batidosState, s *snacksState) {
				b.goods = nil
			},
			wantReasons: []preflight.Reason{preflight.ReasonBatidosSKUNotListed},
		},
		{
			name: "SKU de batido presente sin listar",
			mutate: func(b *batidosState, s *snacksState) {
				b.goods[0].Listing = false
			},
			wantReasons: []preflight.Reason{preflight.ReasonBatidosSKUNotListed},
		},
		{
			name: "SKU de batido agotado con soldOut 1",
			mutate: func(b *batidosState, s *snacksState) {
				b.goods[0].SoldOut = 1
			},
			wantReasons: []preflight.Reason{preflight.ReasonBatidosSKUSoldOut},
		},
		{
			name: "SKU de batido con el soldOut ambiguo -1 del proveedor",
			mutate: func(b *batidosState, s *snacksState) {
				b.goods[0].SoldOut = -1
			},
			wantReasons: []preflight.Reason{preflight.ReasonBatidosSKUSoldOut},
		},
		{
			name: "dispositivo de snacks inexistente",
			mutate: func(b *batidosState, s *snacksState) {
				s.page.Items = nil
				s.slots = nil
			},
			wantReasons: []preflight.Reason{preflight.ReasonSnacksDeviceNotFound},
		},
		{
			name: "dispositivo de snacks con otro código en la página",
			mutate: func(b *batidosState, s *snacksState) {
				s.page.Items[0].DeviceCode = "otro-codigo"
			},
			wantReasons: []preflight.Reason{preflight.ReasonSnacksDeviceNotFound},
		},
		{
			name: "snacks fuera de línea",
			mutate: func(b *batidosState, s *snacksState) {
				s.page.Items[0].OnlineStatus = snacks.OnlineStatusOffline
			},
			wantReasons: []preflight.Reason{preflight.ReasonSnacksDeviceOffline},
		},
		{
			name: "venta de snacks suspendida",
			mutate: func(b *batidosState, s *snacksState) {
				s.page.Items[0].SellStatus = snacks.SellStatusSuspended
			},
			wantReasons: []preflight.Reason{preflight.ReasonSnacksSaleNotAllowed},
		},
		{
			name: "snacks con falla",
			mutate: func(b *batidosState, s *snacksState) {
				s.page.Items[0].FaultState = snacks.FaultStateFault
			},
			wantReasons: []preflight.Reason{preflight.ReasonSnacksDeviceFaulted},
		},
		{
			name: "snacks bloqueada",
			mutate: func(b *batidosState, s *snacksState) {
				s.page.Items[0].LockType = snacks.LockType(1)
			},
			wantReasons: []preflight.Reason{preflight.ReasonSnacksDeviceLocked},
		},
		{
			name: "snacks sin stock",
			mutate: func(b *batidosState, s *snacksState) {
				s.page.Items[0].StockoutState = snacks.StockoutStateOutOfStock
			},
			wantReasons: []preflight.Reason{preflight.ReasonSnacksDeviceOutOfStock},
		},
		{
			name: "SKU de snack ausente de todos los slots",
			mutate: func(b *batidosState, s *snacksState) {
				s.slots = []snacks.Slot{{SlotID: "2", SlotStatus: snacks.SlotStatusNormal, Stock: 5, ProductID: "otro-sku"}}
			},
			wantReasons: []preflight.Reason{preflight.ReasonSnacksSKUNotInAnySlot},
		},
		{
			name: "SKU de snack en un slot con stock 0",
			mutate: func(b *batidosState, s *snacksState) {
				s.slots[0].Stock = 0
			},
			wantReasons: []preflight.Reason{preflight.ReasonSnacksSKUOutOfStock},
		},
		{
			name: "SKU de snack en un slot deshabilitado",
			mutate: func(b *batidosState, s *snacksState) {
				s.slots[0].SlotStatus = snacks.SlotStatusDisabled
			},
			wantReasons: []preflight.Reason{preflight.ReasonSnacksSlotNotSellable},
		},
		{
			name: "SKU de snack repartido entre un slot vacío y uno deshabilitado",
			mutate: func(b *batidosState, s *snacksState) {
				s.slots = []snacks.Slot{
					{SlotID: "1", SlotStatus: snacks.SlotStatusNormal, Stock: 0, ProductID: testSnacksSKU},
					{SlotID: "2", SlotStatus: snacks.SlotStatusDisabled, Stock: 5, ProductID: testSnacksSKU},
				}
			},
			wantReasons: []preflight.Reason{preflight.ReasonSnacksSlotNotSellable},
		},
		{
			name: "SKU de snack en un slot normal con stock",
			mutate: func(b *batidosState, s *snacksState) {
				s.slots = []snacks.Slot{
					{SlotID: "1", SlotStatus: snacks.SlotStatusDisabled, Stock: 5, ProductID: testSnacksSKU},
					{SlotID: "2", SlotStatus: snacks.SlotStatusNormal, Stock: 2, ProductID: testSnacksSKU},
				}
			},
			wantSellable: true,
		},
		{
			name: "las dos máquinas con problemas reportan todas las razones juntas",
			mutate: func(b *batidosState, s *snacksState) {
				b.device.Status = batidos.DeviceStatusOffline
				b.device.ScarceStatus = batidos.ScarceStatusOutOfStock
				b.goods[0].SoldOut = 1
				s.page.Items[0].OnlineStatus = snacks.OnlineStatusOffline
				s.page.Items[0].SellStatus = snacks.SellStatusSuspended
				s.slots = nil
			},
			wantReasons: []preflight.Reason{
				preflight.ReasonBatidosDeviceOffline,
				preflight.ReasonBatidosOutOfSupplies,
				preflight.ReasonBatidosSKUSoldOut,
				preflight.ReasonSnacksDeviceOffline,
				preflight.ReasonSnacksSaleNotAllowed,
				preflight.ReasonSnacksSKUNotInAnySlot,
			},
		},
		{
			name: "un error de batidos se propaga sin devolver un resultado",
			mutate: func(b *batidosState, s *snacksState) {
				b.deviceErr = errProvider
			},
			wantErr: errProvider,
		},
		{
			name: "un error de snacks se propaga sin devolver un resultado",
			mutate: func(b *batidosState, s *snacksState) {
				s.slotsErr = errProvider
			},
			wantErr: errProvider,
		},
		{
			name: "un error del catálogo de batidos se propaga sin devolver un resultado",
			mutate: func(b *batidosState, s *snacksState) {
				b.goodsErr = errProvider
			},
			wantErr: errProvider,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bState := healthyBatidos()
			sState := healthySnacks()
			if tc.mutate != nil {
				tc.mutate(&bState, &sState)
			}

			bStub := &batidosStub{
				device:    bState.device,
				goods:     bState.goods,
				deviceErr: bState.deviceErr,
				goodsErr:  bState.goodsErr,
			}
			sStub := &snacksStub{
				page:     sState.page,
				slots:    sState.slots,
				pageErr:  sState.pageErr,
				slotsErr: sState.slotsErr,
			}

			checker := preflight.New(bStub, sStub)
			result, err := checker.Check(context.Background(), preflight.Request{
				BatidosDeviceID:  testBatidosDeviceID,
				BatidosSKU:       testBatidosSKU,
				SnacksDeviceCode: testSnacksDeviceCode,
				SnacksSKU:        testSnacksSKU,
			})

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Check devolvió el error %v, se esperaba %v", err, tc.wantErr)
			}

			if bStub.deviceCalls != 1 || bStub.goodsCalls != 1 {
				t.Fatalf("se esperaba una consulta de cada dato de batidos, se hizo device=%d goods=%d",
					bStub.deviceCalls, bStub.goodsCalls)
			}
			if sStub.pageCalls != 1 || sStub.slotsCalls != 1 {
				t.Fatalf("se esperaba una consulta de cada dato de snacks, se hizo page=%d slots=%d",
					sStub.pageCalls, sStub.slotsCalls)
			}
			if sStub.gotPage != 1 || sStub.gotPageSize != 10 {
				t.Fatalf("la página de snacks se pidió como page=%d pageSize=%d, se esperaba page=1 pageSize=10",
					sStub.gotPage, sStub.gotPageSize)
			}

			if tc.wantErr != nil {
				if result.Sellable {
					t.Fatal("un error no puede devolver un resultado vendible")
				}
				if len(result.Reasons) != 0 {
					t.Fatalf("un error no puede devolver razones, se devolvieron %v", result.Reasons)
				}
				return
			}

			if result.Sellable != tc.wantSellable {
				t.Fatalf("Sellable=%v, se esperaba %v (razones %v)", result.Sellable, tc.wantSellable, result.Reasons)
			}
			if !slices.Equal(result.Reasons, tc.wantReasons) {
				t.Fatalf("razones %v, se esperaba %v", result.Reasons, tc.wantReasons)
			}
		})
	}
}
