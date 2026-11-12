package dispenser

import (
	"context"
	"fmt"

	"compras-remotas/internal/linestate"
	"compras-remotas/internal/machine"
)

type Availability struct {
	Available bool
	Reason    string
}

type Request struct {
	OrderID     string
	LineID      string
	Machine     machine.Ref
	SKU         string
	AmountCents int64

	KnownRef string
}

type DispatchResult struct {
	Accepted    bool
	ProviderRef string
	Detail      string
}

type Status struct {
	State           linestate.State
	ProviderOrderNo string
	Reason          string
}

type Rejection struct {
	Provider   string
	HTTPStatus int
	Code       int
	Message    string
}

func (r *Rejection) Error() string {
	message := r.Message
	if message == "" {
		message = "sin mensaje del proveedor"
	}

	return fmt.Sprintf("%s: el proveedor rechazó la llamada (code=%d, http=%d): %s", r.Provider, r.Code, r.HTTPStatus, message)
}

type Dispenser interface {
	Check(ctx context.Context, machine machine.Ref) (Availability, error)

	Dispatch(ctx context.Context, req Request) (DispatchResult, error)

	Reconcile(ctx context.Context, req Request) (Status, error)
}
