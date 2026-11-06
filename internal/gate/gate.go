package gate

import (
	"context"
	"errors"
	"fmt"

	"compras-remotas/internal/config"
	"compras-remotas/internal/order"
	"compras-remotas/internal/preflight"
)

var errNoChecker = errors.New("no hay un verificador de disponibilidad configurado")

type Checker interface {
	Check(ctx context.Context, req preflight.Request) (preflight.Result, error)
}

type Gate struct {
	checker Checker
}

func New(checker Checker) *Gate {
	return &Gate{checker: checker}
}

func (g *Gate) Check(ctx context.Context, draft order.Draft) error {
	if g.checker == nil {
		return errNoChecker
	}

	request, err := g.request(draft)
	if err != nil {
		return err
	}

	result, err := g.checker.Check(ctx, request)
	if err != nil {
		return err
	}

	if result.Sellable {
		return nil
	}

	reasons := make([]string, 0, len(result.Reasons))
	for _, reason := range result.Reasons {
		reasons = append(reasons, string(reason))
	}

	return &order.NotSellableError{Reasons: reasons}
}

func (g *Gate) request(draft order.Draft) (preflight.Request, error) {
	var (
		request preflight.Request
		batidos int
		snacks  int
	)

	for _, item := range draft.Items {
		switch item.Machine.Type {
		case string(config.MachineBatidos):
			batidos++

			request.BatidosDeviceID = item.Machine.DeviceID
			request.BatidosSKU = item.SKU
		case string(config.MachineSnacks):
			snacks++

			request.SnacksDeviceCode = item.Machine.MID
			request.SnacksSKU = item.SKU
		}
	}

	if batidos != 1 || snacks != 1 {
		return preflight.Request{}, fmt.Errorf(
			"el combo no se puede evaluar: se esperaba un ítem de batidos y uno de snacks, y hay %d de batidos y %d de snacks",
			batidos, snacks,
		)
	}

	return request, nil
}
