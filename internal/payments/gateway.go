package payments

import (
	"context"
	"errors"
	"time"
)

type ChargeStatus string

const (
	ChargePending   ChargeStatus = "pending"
	ChargeApproved  ChargeStatus = "approved"
	ChargeExpired   ChargeStatus = "expired"
	ChargeCancelled ChargeStatus = "cancelled"
	ChargeUnknown   ChargeStatus = "unknown"
)

var ErrNotFound = errors.New("payments: el cobro no existe en el proveedor")

var ErrNotCancelable = errors.New("payments: el proveedor no acepta cancelar el cobro en su estado actual")

type QRRequest struct {
	OrderID     string
	PosID       string
	AmountCents int64
	Title       string
	ExpiresIn   time.Duration
}

type Charge struct {
	ProviderID           string
	ExternalRef          string
	QRData               string
	Status               ChargeStatus
	ProviderStatus       string
	ProviderStatusDetail string
	AmountCents          int64
	PaidAmountCents      int64
}

type Gateway interface {
	CreateQR(ctx context.Context, req QRRequest) (Charge, error)
	Lookup(ctx context.Context, providerID string) (Charge, error)
	Cancel(ctx context.Context, providerID string) (Charge, error)
}
