package linestate

type State string

const (
	Pending   State = "pending"
	Sent      State = "sent"
	Brewing   State = "brewing"
	Delivered State = "delivered"
	Failed    State = "failed"

	InDoubt State = "in_doubt"
)
