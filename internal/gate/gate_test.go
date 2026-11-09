package gate_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/gate"
	"compras-remotas/internal/order"
	"compras-remotas/internal/preflight"
)

type stubChecker struct {
	mu       sync.Mutex
	requests []preflight.Request
	result   preflight.Result
	err      error
}

func (s *stubChecker) Check(ctx context.Context, req preflight.Request) (preflight.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.requests = append(s.requests, req)

	return s.result, s.err
}

func (s *stubChecker) onlyRequest(t *testing.T) preflight.Request {
	t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.requests) != 1 {
		t.Fatalf("el verificador recibió %d consultas, se esperaba 1", len(s.requests))
	}

	return s.requests[0]
}

func mixedDraft() order.Draft {
	return order.Draft{
		KioskID:     "kiosk-1",
		ComboID:     "combo-1",
		AmountCents: 2500,
		Currency:    "ARS",
		Items: []order.Item{
			{
				SKU: "batido-frutilla",
				Machine: dispenser.MachineRef{
					KioskID:  "kiosk-1",
					Kind:     "batidos",
					Name:     "Batidos Powermix",
					DeviceID: "batidos-device-1",
					DeviceNo: "7",
				},
			},
			{
				SKU: "snack-alfajor",
				Machine: dispenser.MachineRef{
					KioskID: "kiosk-1",
					Kind:    "snacks",
					Name:    "Snacks Powermix",
					MID:     "snacks-mid-1",
				},
			},
		},
	}
}

func TestGateTranslatesTheDraftIntoAPreflightRequest(t *testing.T) {
	checker := &stubChecker{result: preflight.Result{Sellable: true}}

	var _ order.Gate = gate.New(checker)

	if err := gate.New(checker).Check(context.Background(), mixedDraft()); err != nil {
		t.Fatalf("Check: %v", err)
	}

	request := checker.onlyRequest(t)

	if request.BatidosDeviceID != "batidos-device-1" {
		t.Fatalf("BatidosDeviceID=%q, se esperaba el DeviceID de batidos", request.BatidosDeviceID)
	}
	if request.BatidosSKU != "batido-frutilla" {
		t.Fatalf("BatidosSKU=%q, se esperaba %q", request.BatidosSKU, "batido-frutilla")
	}
	if request.SnacksDeviceCode != "snacks-mid-1" {
		t.Fatalf("SnacksDeviceCode=%q, se esperaba el MID de snacks", request.SnacksDeviceCode)
	}
	if request.SnacksSKU != "snack-alfajor" {
		t.Fatalf("SnacksSKU=%q, se esperaba %q", request.SnacksSKU, "snack-alfajor")
	}
}

func TestGateRejectsADraftWithoutBothMachineTypes(t *testing.T) {
	cases := []struct {
		name  string
		items []order.Item
	}{
		{name: "sin snacks", items: mixedDraft().Items[:1]},
		{name: "sin batidos", items: mixedDraft().Items[1:]},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checker := &stubChecker{result: preflight.Result{Sellable: true}}

			draft := mixedDraft()
			draft.Items = tc.items

			err := gate.New(checker).Check(context.Background(), draft)
			if err == nil {
				t.Fatal("se esperaba un error de forma del draft y llegó nil")
			}
			if errors.Is(err, order.ErrNotSellable) {
				t.Fatalf("un draft que no se supo evaluar no es un combo no vendible: %v", err)
			}

			checker.mu.Lock()
			consultas := len(checker.requests)
			checker.mu.Unlock()

			if consultas != 0 {
				t.Fatalf("se consultó al verificador %d veces con un draft que no se puede evaluar", consultas)
			}
		})
	}
}

func TestGateRejectsADraftWithTwoItemsOfTheSameMachineType(t *testing.T) {
	checker := &stubChecker{result: preflight.Result{Sellable: true}}

	draft := mixedDraft()
	draft.Items = append(draft.Items, order.Item{
		SKU:     "batido-chocolate",
		Machine: dispenser.MachineRef{KioskID: "kiosk-1", Kind: "batidos", DeviceID: "batidos-device-2"},
	})

	err := gate.New(checker).Check(context.Background(), draft)
	if err == nil {
		t.Fatal("se esperaba un error por dos ítems de batidos y llegó nil")
	}
	if errors.Is(err, order.ErrNotSellable) {
		t.Fatalf("un draft repetido no es un combo no vendible: %v", err)
	}
}

func TestGateReturnsNotSellableWithTheReasonsInOrder(t *testing.T) {
	checker := &stubChecker{result: preflight.Result{
		Sellable: false,
		Reasons:  []preflight.Reason{preflight.ReasonBatidosDeviceOffline, preflight.ReasonSnacksSKUOutOfStock},
	}}

	err := gate.New(checker).Check(context.Background(), mixedDraft())
	if err == nil {
		t.Fatal("se esperaba un error de bloqueo y llegó nil")
	}
	if !errors.Is(err, order.ErrNotSellable) {
		t.Fatalf("errors.Is(err, ErrNotSellable)=false para %v", err)
	}

	var notSellable *order.NotSellableError
	if !errors.As(err, &notSellable) {
		t.Fatalf("se esperaba un *order.NotSellableError y llegó %T", err)
	}

	want := []string{"batidos_device_offline", "snacks_sku_out_of_stock"}
	if len(notSellable.Reasons) != len(want) {
		t.Fatalf("Reasons=%v, se esperaba %v", notSellable.Reasons, want)
	}
	for i := range want {
		if notSellable.Reasons[i] != want[i] {
			t.Fatalf("Reasons=%v, se esperaba %v", notSellable.Reasons, want)
		}
	}
}

func TestGatePropagatesACheckerError(t *testing.T) {
	caido := errors.New("el proveedor de batidos no responde")
	checker := &stubChecker{err: caido}

	err := gate.New(checker).Check(context.Background(), mixedDraft())
	if !errors.Is(err, caido) {
		t.Fatalf("se esperaba el error del verificador y llegó %v", err)
	}
	if errors.Is(err, order.ErrNotSellable) {
		t.Fatalf("un proveedor inalcanzable no es un combo no vendible: %v", err)
	}
}

func TestGateWithANilCheckerFailsClosed(t *testing.T) {
	err := gate.New(nil).Check(context.Background(), mixedDraft())
	if err == nil {
		t.Fatal("sin verificador se esperaba un error y llegó nil")
	}
	if errors.Is(err, order.ErrNotSellable) {
		t.Fatalf("la falta de verificador no es un combo no vendible: %v", err)
	}
}
