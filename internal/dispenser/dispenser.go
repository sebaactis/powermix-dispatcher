package dispenser

import "context"

type LineState string

const (
	LinePending   LineState = "pending"
	LineSent      LineState = "sent"
	LineBrewing   LineState = "brewing"
	LineDelivered LineState = "delivered"
	LineFailed    LineState = "failed"

	LineInDoubt LineState = "in_doubt"
)

type MachineRef struct {
	KioskID      string
	Type         string
	Name         string
	DeviceID     string
	DeviceNo     string
	DeviceTypeID string
	MID          string
}

type Availability struct {
	Available bool
	Reason    string
}

type Request struct {
	OrderID     string
	LineID      string
	Machine     MachineRef
	SKU         string
	AmountCents int64

	Ref string
}

type Attempt struct {
	Accepted bool
	Ref      string
	Detail   string
}

type Status struct {
	State  LineState
	Ref    string
	Reason string
}

type Dispenser interface {
	Check(ctx context.Context, machine MachineRef) (Availability, error)

	Dispatch(ctx context.Context, req Request) (Attempt, error)

	Reconcile(ctx context.Context, req Request) (Status, error)
}
