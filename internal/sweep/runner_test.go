package sweep_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"compras-remotas/internal/logging"
	"compras-remotas/internal/sweep"
)

const waitTimeout = 2 * time.Second

type fakeSweeper struct {
	limits   chan int
	release  chan struct{}
	hook     func(ctx context.Context, call int) error
	call     atomic.Int32
	inFlight atomic.Int32
	peak     atomic.Int32
}

func newFakeSweeper() *fakeSweeper {
	return &fakeSweeper{limits: make(chan int, 64)}
}

func (f *fakeSweeper) ReconcileDue(ctx context.Context, limit int) error {
	number := int(f.call.Add(1))
	current := f.inFlight.Add(1)
	defer f.inFlight.Add(-1)

	for {
		peak := f.peak.Load()
		if current <= peak || f.peak.CompareAndSwap(peak, current) {
			break
		}
	}

	select {
	case f.limits <- limit:
	default:
	}

	if f.release != nil {
		select {
		case <-f.release:
		case <-ctx.Done():
		}
	}

	if f.hook != nil {
		return f.hook(ctx, number)
	}

	return nil
}

func startRunner(t *testing.T, sweeper sweep.Sweeper, logger *slog.Logger, opts ...sweep.Option) (context.CancelFunc, <-chan error) {
	t.Helper()

	runner := sweep.New(sweeper, logger, opts...)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- runner.Run(ctx)
	}()

	return cancel, done
}

func receive[T any](t *testing.T, values <-chan T) T {
	t.Helper()

	select {
	case value := <-values:
		return value
	case <-time.After(waitTimeout):
		t.Fatal("la espera superó el límite de 2 segundos")
		var zero T
		return zero
	}
}

func TestRunReconcilesOncePerTickWithConfiguredLimit(t *testing.T) {
	sweeper := newFakeSweeper()

	cancel, done := startRunner(t, sweeper, nil, sweep.WithInterval(5*time.Millisecond), sweep.WithLimit(7))
	defer cancel()

	for tick := 1; tick <= 2; tick++ {
		if limit := receive(t, sweeper.limits); limit != 7 {
			t.Fatalf("el barrido %d recibió limit=%d, se esperaba 7", tick, limit)
		}
	}

	cancel()

	if err := receive(t, done); err != nil {
		t.Fatalf("Run devolvió %v, se esperaba nil", err)
	}
}

func TestRunKeepsGoingAfterAnError(t *testing.T) {
	sweeper := newFakeSweeper()
	sweeper.hook = func(ctx context.Context, call int) error {
		if call == 1 {
			return errors.New("el proveedor no respondió")
		}
		return nil
	}

	var logs bytes.Buffer

	cancel, done := startRunner(t, sweeper, logging.New(&logs, slog.LevelInfo), sweep.WithInterval(5*time.Millisecond))

	receive(t, sweeper.limits)
	receive(t, sweeper.limits)

	cancel()

	if err := receive(t, done); err != nil {
		t.Fatalf("Run devolvió %v, se esperaba nil", err)
	}

	output := logs.String()

	if !strings.Contains(output, "level=ERROR") {
		t.Fatalf("el error del barrido no se registró como ERROR: %s", output)
	}
	if !strings.Contains(output, "barrido de reconciliación") {
		t.Fatalf("el log no describe el barrido de reconciliación: %s", output)
	}
	if !strings.Contains(output, "proveedor no respondió") {
		t.Fatalf("el log no incluye el error original: %s", output)
	}
}

func TestRunStopsBeforeTheNextTickOnCancellation(t *testing.T) {
	sweeper := newFakeSweeper()

	cancel, done := startRunner(t, sweeper, nil, sweep.WithInterval(time.Hour))
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run devolvió %v, se esperaba nil", err)
		}
	case <-time.After(waitTimeout):
		t.Fatal("Run no terminó al cancelar el contexto")
	}

	select {
	case limit := <-sweeper.limits:
		t.Fatalf("el barrido se ejecutó pese a la cancelación (limit=%d)", limit)
	default:
	}
}

func TestRunNeverOverlapsTicks(t *testing.T) {
	sweeper := newFakeSweeper()
	release := make(chan struct{})
	sweeper.release = release

	cancel, done := startRunner(t, sweeper, nil, sweep.WithInterval(2*time.Millisecond))
	defer cancel()

	receive(t, sweeper.limits)
	time.Sleep(30 * time.Millisecond)
	close(release)
	receive(t, sweeper.limits)

	if peak := sweeper.peak.Load(); peak != 1 {
		t.Fatalf("hubo %d barridos simultáneos, se esperaba 1", peak)
	}

	cancel()

	if err := receive(t, done); err != nil {
		t.Fatalf("Run devolvió %v, se esperaba nil", err)
	}
}

func TestWithIntervalZeroOrNegativeKeepsTheDefaultInterval(t *testing.T) {
	for _, interval := range []time.Duration{0, -time.Second} {
		t.Run(interval.String(), func(t *testing.T) {
			sweeper := newFakeSweeper()

			cancel, done := startRunner(t, sweeper, nil, sweep.WithInterval(interval))
			defer cancel()

			select {
			case limit := <-sweeper.limits:
				t.Fatalf("WithInterval(%v) no conservó el intervalo por defecto: hubo un barrido con limit=%d", interval, limit)
			case <-time.After(60 * time.Millisecond):
			}

			cancel()

			if err := receive(t, done); err != nil {
				t.Fatalf("Run devolvió %v, se esperaba nil", err)
			}
		})
	}
}

func TestWithLimitZeroOrNegativeKeepsTheDefaultLimit(t *testing.T) {
	for _, limit := range []int{0, -5} {
		t.Run(strconv.Itoa(limit), func(t *testing.T) {
			sweeper := newFakeSweeper()

			cancel, done := startRunner(t, sweeper, nil, sweep.WithInterval(5*time.Millisecond), sweep.WithLimit(limit))
			defer cancel()

			if received := receive(t, sweeper.limits); received != 10 {
				t.Fatalf("WithLimit(%d) envió limit=%d, se esperaba el valor por defecto 10", limit, received)
			}

			cancel()

			if err := receive(t, done); err != nil {
				t.Fatalf("Run devolvió %v, se esperaba nil", err)
			}
		})
	}
}
