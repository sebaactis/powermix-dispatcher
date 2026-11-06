package batidos

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"compras-remotas/internal/dispenser"
)

const (
	defaultCampaignWindow = 10 * time.Minute
	defaultCampaignName   = "combo-remoto"

	providerName = "batidos"

	campaignExchangeTypeSingleGoods = 1
)

var _ dispenser.Dispenser = (*Dispenser)(nil)

type DispenserOption func(*Dispenser)

func WithCampaignWindow(window time.Duration) DispenserOption {
	return func(d *Dispenser) {
		if window > 0 {
			d.campaignWindow = window
		}
	}
}

func WithCampaignName(name string) DispenserOption {
	return func(d *Dispenser) {
		if name != "" {
			d.campaignName = name
		}
	}
}

func WithNotifyURL(notifyURL func(orderID string) string) DispenserOption {
	return func(d *Dispenser) {
		if notifyURL != nil {
			d.notifyURL = notifyURL
		}
	}
}

type Dispenser struct {
	client         *Client
	campaignWindow time.Duration
	campaignName   string
	notifyURL      func(orderID string) string
}

func NewDispenser(client *Client, opts ...DispenserOption) *Dispenser {
	d := &Dispenser{
		client:         client,
		campaignWindow: defaultCampaignWindow,
		campaignName:   defaultCampaignName,
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

func (d *Dispenser) Check(ctx context.Context, machine dispenser.MachineRef) (dispenser.Availability, error) {
	device, err := d.client.Device(ctx, machine.DeviceID)
	if err != nil {
		return dispenser.Availability{}, err
	}

	reasons := make([]string, 0, 3)
	if device.Status != DeviceStatusOnline {
		reasons = append(reasons, "máquina fuera de línea")
	}
	if device.DeviceRunStatus != DeviceRunStatusNormal {
		reasons = append(reasons, "avería")
	}
	if device.ScarceStatus != ScarceStatusNormal {
		reasons = append(reasons, "sin insumos")
	}

	if len(reasons) == 0 {
		return dispenser.Availability{Available: true}, nil
	}

	return dispenser.Availability{Reason: strings.Join(reasons, "; ")}, nil
}

func (d *Dispenser) Dispatch(ctx context.Context, req dispenser.Request) (dispenser.DispatchResult, error) {
	start := d.client.now()

	request := CampaignRequest{
		ActiveName:   d.campaignName,
		Start:        start,
		End:          start.Add(d.campaignWindow),
		DeviceTypeID: req.Machine.DeviceTypeID,
		ExchangeType: campaignExchangeTypeSingleGoods,
		GoodsIDs:     []string{req.SKU},
	}

	if d.notifyURL != nil {
		request.NotifyURL = d.notifyURL(req.OrderID)
	}

	campaign, err := d.client.CreateCampaign(ctx, request)
	if err != nil {
		return dispenser.DispatchResult{}, fmt.Errorf("no se pudo emitir el código de canje: %w", asPortRejection(err))
	}

	code := campaign.ExchangeCodes[0]

	brewed, err := d.client.ProduceCoffee(ctx, ProduceRequest{
		Code:     code,
		DeviceID: req.Machine.DeviceID,
		GoodsID:  req.SKU,
		DeviceNo: req.Machine.DeviceNo,
	})
	if err != nil {
		detail := "la campaña se emitió pero el canje falló; reconciliar con el código"
		if brewed.Detail != "" {
			detail += ": " + brewed.Detail
		}

		return dispenser.DispatchResult{Accepted: brewed.Accepted, ProviderRef: code, Detail: detail},
			fmt.Errorf("no se pudo disparar el batido: %w", asPortRejection(err))
	}

	return dispenser.DispatchResult{Accepted: true, ProviderRef: code, Detail: brewed.Detail}, nil
}

func (d *Dispenser) Reconcile(ctx context.Context, req dispenser.Request) (dispenser.Status, error) {
	info, err := d.client.OrderInfo(ctx, req.KnownRef, req.Machine.DeviceID)
	if err != nil {
		return dispenser.Status{}, asPortRejection(err)
	}

	switch info.Status {
	case OrderStatusFailed:
		return dispenser.Status{State: dispenser.LineFailed, ProviderOrderNo: info.OrderNo}, nil
	case OrderStatusInProgress:
		return dispenser.Status{State: dispenser.LineBrewing, ProviderOrderNo: info.OrderNo}, nil
	case OrderStatusCompleted:
		return dispenser.Status{State: dispenser.LineDelivered, ProviderOrderNo: info.OrderNo}, nil
	default:
		return dispenser.Status{
			State:           dispenser.LineInDoubt,
			ProviderOrderNo: info.OrderNo,
			Reason:          fmt.Sprintf("el proveedor informó un estado desconocido: %d", int(info.Status)),
		}, nil
	}
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
