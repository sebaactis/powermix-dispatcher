package preflight

import (
	"context"
	"errors"

	"compras-remotas/internal/dispenser/batidos"
	"compras-remotas/internal/dispenser/snacks"
)

const (
	devicesPage     = 1
	devicesPageSize = 10
)

type Batidos interface {
	Device(ctx context.Context, deviceID string) (batidos.Device, error)
	DeviceGoods(ctx context.Context, deviceID string) ([]batidos.DeviceGoods, error)
}

type Snacks interface {
	Devices(ctx context.Context, page, pageSize int, deviceCode string) (snacks.DevicesPage, error)
	Slots(ctx context.Context, deviceCode string) ([]snacks.Slot, error)
}

type Request struct {
	BatidosDeviceID  string
	BatidosSKU       string
	SnacksDeviceCode string
	SnacksSKU        string
}

type Reason string

const (
	ReasonBatidosDeviceOffline   Reason = "batidos_device_offline"
	ReasonBatidosDeviceFaulted   Reason = "batidos_device_faulted"
	ReasonBatidosOutOfSupplies   Reason = "batidos_out_of_supplies"
	ReasonBatidosSKUNotListed    Reason = "batidos_sku_not_listed"
	ReasonBatidosSKUSoldOut      Reason = "batidos_sku_sold_out"
	ReasonSnacksDeviceNotFound   Reason = "snacks_device_not_found"
	ReasonSnacksDeviceOffline    Reason = "snacks_device_offline"
	ReasonSnacksSaleNotAllowed   Reason = "snacks_sale_not_allowed"
	ReasonSnacksDeviceFaulted    Reason = "snacks_device_faulted"
	ReasonSnacksDeviceLocked     Reason = "snacks_device_locked"
	ReasonSnacksDeviceOutOfStock Reason = "snacks_device_out_of_stock"
	ReasonSnacksSKUNotInAnySlot  Reason = "snacks_sku_not_in_any_slot"
	ReasonSnacksSKUOutOfStock    Reason = "snacks_sku_out_of_stock"
	ReasonSnacksSlotNotSellable  Reason = "snacks_slot_not_sellable"
)

type Result struct {
	Sellable bool
	Reasons  []Reason
}

type Checker struct {
	batidos Batidos
	snacks  Snacks
}

func New(b Batidos, s Snacks) *Checker {
	return &Checker{batidos: b, snacks: s}
}

func (c *Checker) Check(ctx context.Context, req Request) (Result, error) {
	batidosReasons, batidosErr := c.checkBatidos(ctx, req)
	snacksReasons, snacksErr := c.checkSnacks(ctx, req)

	if err := errors.Join(batidosErr, snacksErr); err != nil {
		return Result{}, err
	}

	reasons := make([]Reason, 0, len(batidosReasons)+len(snacksReasons))
	reasons = append(reasons, batidosReasons...)
	reasons = append(reasons, snacksReasons...)

	return Result{Sellable: len(reasons) == 0, Reasons: reasons}, nil
}

func (c *Checker) checkBatidos(ctx context.Context, req Request) ([]Reason, error) {
	device, deviceErr := c.batidos.Device(ctx, req.BatidosDeviceID)
	goods, goodsErr := c.batidos.DeviceGoods(ctx, req.BatidosDeviceID)
	if err := errors.Join(deviceErr, goodsErr); err != nil {
		return nil, err
	}

	reasons := make([]Reason, 0, 5)
	if device.Status != batidos.DeviceStatusOnline {
		reasons = append(reasons, ReasonBatidosDeviceOffline)
	}
	if device.DeviceRunStatus != batidos.DeviceRunStatusNormal {
		reasons = append(reasons, ReasonBatidosDeviceFaulted)
	}
	if device.ScarceStatus != batidos.ScarceStatusNormal {
		reasons = append(reasons, ReasonBatidosOutOfSupplies)
	}

	item, found := findGoods(goods, req.BatidosSKU)
	if !found {
		return append(reasons, ReasonBatidosSKUNotListed), nil
	}
	if !item.Listing {
		reasons = append(reasons, ReasonBatidosSKUNotListed)
	}
	if item.SoldOut != 0 {
		reasons = append(reasons, ReasonBatidosSKUSoldOut)
	}

	return reasons, nil
}

func (c *Checker) checkSnacks(ctx context.Context, req Request) ([]Reason, error) {
	page, pageErr := c.snacks.Devices(ctx, devicesPage, devicesPageSize, req.SnacksDeviceCode)
	slots, slotsErr := c.snacks.Slots(ctx, req.SnacksDeviceCode)
	if err := errors.Join(pageErr, slotsErr); err != nil {
		return nil, err
	}

	device, found := findDevice(page.Items, req.SnacksDeviceCode)
	if !found {
		return []Reason{ReasonSnacksDeviceNotFound}, nil
	}

	reasons := make([]Reason, 0, 7)
	if device.OnlineStatus != snacks.OnlineStatusOnline {
		reasons = append(reasons, ReasonSnacksDeviceOffline)
	}
	if device.SellStatus != snacks.SellStatusNormal {
		reasons = append(reasons, ReasonSnacksSaleNotAllowed)
	}
	if device.FaultState != snacks.FaultStateNormal {
		reasons = append(reasons, ReasonSnacksDeviceFaulted)
	}
	if device.LockType != snacks.LockTypeNormal {
		reasons = append(reasons, ReasonSnacksDeviceLocked)
	}
	if device.StockoutState != snacks.StockoutStateNormal {
		reasons = append(reasons, ReasonSnacksDeviceOutOfStock)
	}

	return append(reasons, slotReasons(slots, req.SnacksSKU)...), nil
}

func findGoods(goods []batidos.DeviceGoods, sku string) (batidos.DeviceGoods, bool) {
	for _, item := range goods {
		if item.ID == sku {
			return item, true
		}
	}
	return batidos.DeviceGoods{}, false
}

func findDevice(devices []snacks.Device, deviceCode string) (snacks.Device, bool) {
	for _, device := range devices {
		if device.DeviceCode == deviceCode {
			return device, true
		}
	}
	return snacks.Device{}, false
}

func slotReasons(slots []snacks.Slot, sku string) []Reason {
	matching := make([]snacks.Slot, 0, len(slots))
	for _, slot := range slots {
		if slot.ProductID == sku {
			matching = append(matching, slot)
		}
	}
	if len(matching) == 0 {
		return []Reason{ReasonSnacksSKUNotInAnySlot}
	}

	stockedSomewhere := false
	for _, slot := range matching {
		if slot.Stock <= 0 {
			continue
		}
		if slot.SlotStatus == snacks.SlotStatusNormal {
			return nil
		}
		stockedSomewhere = true
	}
	if stockedSomewhere {
		return []Reason{ReasonSnacksSlotNotSellable}
	}

	return []Reason{ReasonSnacksSKUOutOfStock}
}
