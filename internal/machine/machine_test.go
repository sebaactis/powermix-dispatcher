package machine_test

import (
	"testing"

	"compras-remotas/internal/machine"
)

func TestRefValidate(t *testing.T) {
	const (
		batidosMessage = "la máquina de batidos necesita device_id, device_no y device_type_id"
		snacksMessage  = "la máquina de snacks necesita mid"
	)

	batidosComplete := func() machine.Ref {
		return machine.Ref{
			KioskID:      "kiosk-1",
			Kind:         machine.Batidos,
			Name:         "Batidos Powermix",
			DeviceID:     "device-1",
			DeviceNo:     "1",
			DeviceTypeID: "2",
		}
	}

	snacksComplete := func() machine.Ref {
		return machine.Ref{
			KioskID: "kiosk-1",
			Kind:    machine.Snacks,
			Name:    "Snacks Powermix",
			MID:     "mid-1",
		}
	}

	cases := []struct {
		name    string
		ref     machine.Ref
		wantErr string
	}{
		{
			name: "batidos completo",
			ref:  batidosComplete(),
		},
		{
			name: "batidos completo y además con el mid de snacks",
			ref: func() machine.Ref {
				ref := batidosComplete()
				ref.MID = "mid-1"

				return ref
			}(),
		},
		{
			name: "batidos sin device_id",
			ref: func() machine.Ref {
				ref := batidosComplete()
				ref.DeviceID = ""

				return ref
			}(),
			wantErr: batidosMessage,
		},
		{
			name: "batidos sin device_no",
			ref: func() machine.Ref {
				ref := batidosComplete()
				ref.DeviceNo = ""

				return ref
			}(),
			wantErr: batidosMessage,
		},
		{
			name: "batidos sin device_type_id",
			ref: func() machine.Ref {
				ref := batidosComplete()
				ref.DeviceTypeID = ""

				return ref
			}(),
			wantErr: batidosMessage,
		},
		{
			name: "batidos con sólo el mid de snacks",
			ref: machine.Ref{
				KioskID: "kiosk-1",
				Kind:    machine.Batidos,
				Name:    "Batidos Powermix",
				MID:     "mid-1",
			},
			wantErr: batidosMessage,
		},
		{
			name: "snacks con mid",
			ref:  snacksComplete(),
		},
		{
			name: "snacks completo y además con los identificadores de batidos",
			ref: func() machine.Ref {
				ref := snacksComplete()
				ref.DeviceID = "device-1"
				ref.DeviceNo = "1"
				ref.DeviceTypeID = "2"

				return ref
			}(),
		},
		{
			name: "snacks sin mid",
			ref: func() machine.Ref {
				ref := snacksComplete()
				ref.MID = ""

				return ref
			}(),
			wantErr: snacksMessage,
		},
		{
			name: "snacks con sólo el device_id de batidos",
			ref: machine.Ref{
				KioskID:  "kiosk-1",
				Kind:     machine.Snacks,
				Name:     "Snacks Powermix",
				DeviceID: "device-1",
			},
			wantErr: snacksMessage,
		},
		{
			name: "tipo de máquina desconocido",
			ref: machine.Ref{
				KioskID:  "kiosk-1",
				Kind:     machine.Kind("cafetera"),
				DeviceID: "device-1",
				DeviceNo: "1",
				MID:      "mid-1",
			},
			wantErr: `tipo de máquina desconocido "cafetera"`,
		},
		{
			name:    "tipo de máquina vacío",
			ref:     machine.Ref{KioskID: "kiosk-1", DeviceID: "device-1", MID: "mid-1"},
			wantErr: `tipo de máquina desconocido ""`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.ref.Validate()

			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate()=%v, se esperaba nil", err)
				}

				return
			}

			if err == nil {
				t.Fatalf("Validate()=nil, se esperaba %q", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("Validate()=%q, se esperaba %q", err.Error(), tc.wantErr)
			}
		})
	}
}
