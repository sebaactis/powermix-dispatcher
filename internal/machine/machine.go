package machine

import (
	"errors"
	"fmt"
)

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

func (r Ref) Validate() error {
	switch r.Kind {
	case Batidos:
		if r.DeviceID == "" || r.DeviceNo == "" || r.DeviceTypeID == "" {
			return errors.New("la máquina de batidos necesita device_id, device_no y device_type_id")
		}
	case Snacks:
		if r.MID == "" {
			return errors.New("la máquina de snacks necesita mid")
		}
	default:
		return fmt.Errorf("tipo de máquina desconocido %q", r.Kind)
	}

	return nil
}
