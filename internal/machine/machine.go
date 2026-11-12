package machine

type Kind string

const (
	Batidos Kind = "batidos"
	Snacks  Kind = "snacks"
)

type Ref struct {
	KioskID      string
	Kind         Kind
	Name         string
	DeviceID     string
	DeviceNo     string
	DeviceTypeID string
	MID          string
}
