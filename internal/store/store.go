package store

import (
	"context"
	"errors"
	"time"

	"compras-remotas/internal/dispenser"
)

type OrderStatus string

const (
	OrderPendingPayment OrderStatus = "pending_payment"
	OrderPaid           OrderStatus = "paid"
	OrderDispatching    OrderStatus = "dispatching"
	OrderDelivered      OrderStatus = "delivered"
	OrderPartial        OrderStatus = "partial"
	OrderFailed         OrderStatus = "failed"
	OrderInDoubt        OrderStatus = "in_doubt"
	OrderRefunded       OrderStatus = "refunded"
	OrderCancelled      OrderStatus = "cancelled"
)

type Order struct {
	ID          string
	KioskID     string
	ComboID     string
	ClientKey   string
	AmountCents int64
	Currency    string
	Status      OrderStatus
	CreatedAt   time.Time
	PaidAt      time.Time
	ClosedAt    time.Time
	Lines       []Line
}

type Line struct {
	ID             string
	OrderID        string
	Position       int
	Machine        dispenser.MachineRef
	SKU            string
	AmountCents    int64
	State          dispenser.LineState
	Ref            string
	Attempts       int
	DeadlineAt     time.Time
	ReconcileUntil time.Time
	LastError      string
	Version        int64
}

type AttemptOutcome string

const (
	OutcomeAmbiguous AttemptOutcome = "ambiguous"
	OutcomeAccepted  AttemptOutcome = "accepted"
	OutcomeRejected  AttemptOutcome = "rejected"
	OutcomeError     AttemptOutcome = "error"
)

type Attempt struct {
	ID             string
	LineID         string
	Number         int
	IdempotencyKey string
	Outcome        AttemptOutcome
	Detail         string
	StartedAt      time.Time
	FinishedAt     time.Time
}

type Webhook struct {
	ID         string
	Source     string
	DedupeKey  string
	ReceivedAt time.Time
}

var (
	ErrNotFound      = errors.New("el registro no existe")
	ErrAlreadyExists = errors.New("el registro ya existe")
	ErrStale         = errors.New("la línea cambió desde que se leyó")
)

type Store interface {
	CreateOrder(ctx context.Context, order Order) error
	LoadOrder(ctx context.Context, orderID string) (Order, error)
	OrderByClientKey(ctx context.Context, key string) (Order, error)
	SaveOrder(ctx context.Context, order Order) error
	SaveLine(ctx context.Context, line Line) (Line, error)
	AppendAttempt(ctx context.Context, attempt Attempt) error
	SaveAttempt(ctx context.Context, attempt Attempt) error
	RegisterWebhook(ctx context.Context, wh Webhook) (duplicate bool, err error)
	DueLines(ctx context.Context, now time.Time, limit int) ([]Line, error)
}
