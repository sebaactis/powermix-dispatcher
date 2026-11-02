package batidos

import (
	"context"
	"fmt"
	"strings"
	"time"

	"compras-remotas/internal/dispenser"
)

const (
	defaultCampaignWindow = 10 * time.Minute
	defaultCampaignName   = "combo-remoto"

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

type Dispenser struct {
	client         *Client
	campaignWindow time.Duration
	campaignName   string
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

func (d *Dispenser) Dispatch(ctx context.Context, req dispenser.Request) (dispenser.Attempt, error) {
	start := d.client.now()

	campaign, err := d.client.CreateCampaign(ctx, CampaignRequest{
		ActiveName:   d.campaignName,
		Start:        start,
		End:          start.Add(d.campaignWindow),
		DeviceTypeID: req.Machine.DeviceTypeID,
		ExchangeType: campaignExchangeTypeSingleGoods,
		GoodsIDs:     []string{req.SKU},
	})
	if err != nil {
		return dispenser.Attempt{}, fmt.Errorf("no se pudo emitir el código de canje: %w", err)
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

		return dispenser.Attempt{Accepted: brewed.Accepted, Ref: code, Detail: detail},
			fmt.Errorf("no se pudo disparar el batido: %w", err)
	}

	return dispenser.Attempt{Accepted: true, Ref: code, Detail: brewed.Detail}, nil
}

func (d *Dispenser) Reconcile(ctx context.Context, req dispenser.Request) (dispenser.Status, error) {
	info, err := d.client.OrderInfo(ctx, req.Ref, req.Machine.DeviceID)
	if err != nil {
		return dispenser.Status{}, err
	}

	switch info.Status {
	case OrderStatusFailed:
		return dispenser.Status{State: dispenser.LineFailed, Ref: info.OrderNo}, nil
	case OrderStatusInProgress:
		return dispenser.Status{State: dispenser.LineBrewing, Ref: info.OrderNo}, nil
	case OrderStatusCompleted:
		return dispenser.Status{State: dispenser.LineDelivered, Ref: info.OrderNo}, nil
	default:
		return dispenser.Status{
			State:  dispenser.LineInDoubt,
			Ref:    info.OrderNo,
			Reason: fmt.Sprintf("el proveedor informó un estado desconocido: %d", int(info.Status)),
		}, nil
	}
}
