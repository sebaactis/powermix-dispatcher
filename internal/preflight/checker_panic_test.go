package preflight_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"compras-remotas/internal/dispenser/batidos"
	"compras-remotas/internal/dispenser/snacks"
	"compras-remotas/internal/preflight"
)

type panicBatidosStub struct {
	panicVal any
	device   batidos.Device
	goods    []batidos.DeviceGoods
}

func (s *panicBatidosStub) Device(ctx context.Context, deviceID string) (batidos.Device, error) {
	if s.panicVal != nil {
		panic(s.panicVal)
	}
	return s.device, nil
}

func (s *panicBatidosStub) DeviceGoods(ctx context.Context, deviceID string) ([]batidos.DeviceGoods, error) {
	return s.goods, nil
}

type panicSnacksStub struct {
	panicVal any
	page     snacks.DevicesPage
	slots    []snacks.Slot
}

func (s *panicSnacksStub) Devices(ctx context.Context, query snacks.PageQuery) (snacks.DevicesPage, error) {
	if s.panicVal != nil {
		panic(s.panicVal)
	}
	return s.page, nil
}

func (s *panicSnacksStub) Slots(ctx context.Context, deviceCode string) ([]snacks.Slot, error) {
	return s.slots, nil
}

func panicHealthyBatidos() *panicBatidosStub {
	return &panicBatidosStub{
		device: batidos.Device{
			ID:              "bat-1",
			Status:          batidos.DeviceStatusOnline,
			DeviceRunStatus: batidos.DeviceRunStatusNormal,
			ScarceStatus:    batidos.ScarceStatusNormal,
		},
		goods: []batidos.DeviceGoods{
			{ID: "BATIDO-1", Listing: true, SoldOut: 0},
		},
	}
}

func panicHealthySnacks() *panicSnacksStub {
	return &panicSnacksStub{
		page: snacks.DevicesPage{
			Items: []snacks.Device{
				{
					DeviceCode:    "snk-1",
					OnlineStatus:  snacks.OnlineStatusOnline,
					SellStatus:    snacks.SellStatusNormal,
					FaultState:    snacks.FaultStateNormal,
					LockType:      snacks.LockTypeNormal,
					StockoutState: snacks.StockoutStateNormal,
				},
			},
		},
		slots: []snacks.Slot{
			{ProductID: "SNACK-1", Stock: 3, SlotStatus: snacks.SlotStatusNormal},
		},
	}
}

func panicCaptureDefaultSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() {
		slog.SetDefault(prev)
	})
	return &buf
}

func TestCheckerRecoversPanicFromProviderGoroutines(t *testing.T) {
	cases := []struct {
		name      string
		batidos   *panicBatidosStub
		snacks    *panicSnacksStub
		wantPanic string
	}{
		{
			name:      "batidos provider panics",
			batidos:   &panicBatidosStub{panicVal: "batidos preflight boom"},
			snacks:    panicHealthySnacks(),
			wantPanic: "batidos preflight boom",
		},
		{
			name:      "snacks provider panics",
			batidos:   panicHealthyBatidos(),
			snacks:    &panicSnacksStub{panicVal: "snacks preflight boom"},
			wantPanic: "snacks preflight boom",
		},
		{
			name:      "both providers panic",
			batidos:   &panicBatidosStub{panicVal: "batidos preflight boom"},
			snacks:    &panicSnacksStub{panicVal: "snacks preflight boom"},
			wantPanic: "batidos preflight boom",
		},
	}

	req := preflight.Request{
		BatidosDeviceID:  "bat-1",
		BatidosSKU:       "BATIDO-1",
		SnacksDeviceCode: "snk-1",
		SnacksSKU:        "SNACK-1",
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logBuf := panicCaptureDefaultSlog(t)
			checker := preflight.New(tc.batidos, tc.snacks)

			res, err := checker.Check(context.Background(), req)
			if err == nil {
				t.Fatalf("expected error from recovered panic, got result %+v", res)
			}
			if !errors.Is(err, preflight.ErrPanic) {
				t.Fatalf("expected error to wrap preflight.ErrPanic, got %v", err)
			}
			if res.Sellable || len(res.Reasons) != 0 {
				t.Fatalf("expected empty unsellable result on error, got %+v", res)
			}

			var found bool
			for _, line := range strings.Split(strings.TrimSpace(logBuf.String()), "\n") {
				if strings.TrimSpace(line) == "" {
					continue
				}
				var rec map[string]any
				if jsonErr := json.Unmarshal([]byte(line), &rec); jsonErr != nil {
					t.Fatalf("invalid slog JSON %q: %v", line, jsonErr)
				}
				if rec["panic"] == tc.wantPanic && strings.Contains(fmt.Sprint(rec["stack"]), "goroutine") {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing structured panic log with panic=%q and stack; raw:\n%s", tc.wantPanic, logBuf.String())
			}
		})
	}
}

func TestCheckerWithLoggerLogsPanic(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	checker := preflight.New(
		&panicBatidosStub{panicVal: "injected logger boom"},
		panicHealthySnacks(),
		preflight.WithLogger(logger),
	)

	_, err := checker.Check(context.Background(), preflight.Request{
		BatidosDeviceID:  "bat-1",
		BatidosSKU:       "BATIDO-1",
		SnacksDeviceCode: "snk-1",
		SnacksSKU:        "SNACK-1",
	})
	if err == nil {
		t.Fatal("expected error when batidos panics")
	}
	if !strings.Contains(buf.String(), "injected logger boom") {
		t.Fatalf("expected injected logger to capture panic, got: %s", buf.String())
	}
}

func TestCheckerProviderErrorAndTimeoutAreNotErrPanic(t *testing.T) {
	t.Run("provider error", func(t *testing.T) {
		checker := preflight.New(
			&batidosStub{deviceErr: errProvider},
			&snacksStub{page: healthySnacks().page, slots: healthySnacks().slots},
		)

		_, err := checker.Check(context.Background(), testRequest())
		if !errors.Is(err, errProvider) {
			t.Fatalf("expected provider error, got %v", err)
		}
		if errors.Is(err, preflight.ErrPanic) {
			t.Fatalf("provider error must not match preflight.ErrPanic: %v", err)
		}
	})

	t.Run("provider timeout", func(t *testing.T) {
		checker := preflight.New(
			&blockedBatidosStub{},
			&blockedSnacksStub{},
			preflight.WithBudget(20*time.Millisecond),
		)

		_, err := checker.Check(context.Background(), testRequest())
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected context.DeadlineExceeded, got %v", err)
		}
		if errors.Is(err, preflight.ErrPanic) {
			t.Fatalf("timeout must not match preflight.ErrPanic: %v", err)
		}
	})
}
