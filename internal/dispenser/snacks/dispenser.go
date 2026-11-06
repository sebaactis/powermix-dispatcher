package snacks

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"compras-remotas/internal/dispenser"
)

const (
	defaultOrdersPageSize = 10

	firstPage = 1

	devicesPageSize = 10

	providerName = "snacks"
)

var _ dispenser.Dispenser = (*Dispenser)(nil)

type DispenserOption func(*Dispenser)

func WithOrdersPageSize(size int) DispenserOption {
	return func(d *Dispenser) {
		if size > 0 {
			d.ordersPageSize = size
		}
	}
}

type Dispenser struct {
	client         *Client
	ordersPageSize int
}

func NewDispenser(client *Client, opts ...DispenserOption) *Dispenser {
	d := &Dispenser{
		client:         client,
		ordersPageSize: defaultOrdersPageSize,
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

func (d *Dispenser) Check(ctx context.Context, machine dispenser.MachineRef) (dispenser.Availability, error) {
	page, err := d.client.Devices(ctx, PageQuery{Page: firstPage, Size: devicesPageSize, DeviceCode: machine.MID})
	if err != nil {
		return dispenser.Availability{}, err
	}

	device, found := findDevice(page.Items, machine.MID)
	if !found {
		return dispenser.Availability{Reason: "máquina no encontrada"}, nil
	}

	reasons := make([]string, 0, 5)
	if device.OnlineStatus != OnlineStatusOnline {
		reasons = append(reasons, "máquina fuera de línea")
	}
	if device.SellStatus != SellStatusNormal {
		reasons = append(reasons, "venta suspendida")
	}
	if device.FaultState != FaultStateNormal {
		reasons = append(reasons, "avería")
	}
	if device.LockType != LockTypeNormal {
		reasons = append(reasons, "bloqueada")
	}
	if device.StockoutState != StockoutStateNormal {
		reasons = append(reasons, "sin stock")
	}

	if len(reasons) == 0 {
		return dispenser.Availability{Available: true}, nil
	}

	return dispenser.Availability{Reason: strings.Join(reasons, "; ")}, nil
}

func (d *Dispenser) Dispatch(ctx context.Context, req dispenser.Request) (dispenser.DispatchResult, error) {
	attempt, err := d.client.Ship(ctx, ShipmentRequest{
		PaymentOrderID: req.OrderID,
		MID:            req.Machine.MID,
		Products: []ShipmentProduct{
			{
				OrderProductID: req.LineID,
				ProductID:      req.SKU,
				RetailPrice:    centsToUnits(req.AmountCents),
			},
		},
	})
	if err != nil {
		return dispenser.DispatchResult{}, fmt.Errorf("no se pudo disparar el despacho remoto: %w", asPortRejection(err))
	}

	return dispenser.DispatchResult{Accepted: attempt.Accepted, ProviderRef: attempt.ProviderRef, Detail: attempt.Detail}, nil
}

func (d *Dispenser) Reconcile(ctx context.Context, req dispenser.Request) (dispenser.Status, error) {
	ref := req.KnownRef
	if ref == "" {
		ref = req.OrderID
	}

	page, err := d.client.Orders(ctx, PageQuery{Page: firstPage, Size: d.ordersPageSize})
	if err != nil {
		return dispenser.Status{}, asPortRejection(err)
	}

	order, found := findOrder(page.Items, ref)
	if !found {
		return dispenser.Status{
			State:           dispenser.LineInDoubt,
			ProviderOrderNo: ref,
			Reason:          "el proveedor no informa el pedido",
		}, nil
	}

	name := order.OrderStatusName
	if name == "" {
		name = "sin nombre"
	}

	return dispenser.Status{
		State:           dispenser.LineInDoubt,
		ProviderOrderNo: order.OrderNo,
		Reason: fmt.Sprintf(
			"el proveedor informa el estado del pedido %d (%s), no el resultado del despacho",
			order.OrderStatus, name,
		),
	}, nil
}

func centsToUnits(cents int64) float64 {
	return float64(cents) / 100
}

func asPortRejection(err error) error {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return err
	}

	return &dispenser.Rejection{
		Provider:   providerName,
		HTTPStatus: apiErr.HTTPStatus,
		Code:       apiErr.Code,
		Message:    apiErr.Message,
	}
}

func findDevice(devices []Device, deviceCode string) (Device, bool) {
	for _, device := range devices {
		if device.DeviceCode == deviceCode {
			return device, true
		}
	}
	return Device{}, false
}

func findOrder(orders []Order, transactionOrderNo string) (Order, bool) {
	for _, order := range orders {
		if order.TransactionOrderNo == transactionOrderNo {
			return order, true
		}
	}
	return Order{}, false
}
