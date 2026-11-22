package sweep_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"compras-remotas/internal/sweep"
)

type countingSweeper struct {
	mu        sync.Mutex
	lines     int
	payments  int
	limits    []int
	linesErr  error
	paymentsE error
	ticked    chan struct{}
}

func (c *countingSweeper) SyncOverdueLines(_ context.Context, limit int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.lines++
	c.limits = append(c.limits, limit)

	return c.linesErr
}

func (c *countingSweeper) SyncPendingPayments(_ context.Context, limit int) error {
	c.mu.Lock()
	c.payments++
	c.limits = append(c.limits, limit)
	c.mu.Unlock()

	select {
	case c.ticked <- struct{}{}:
	default:
	}

	return c.paymentsE
}

func (c *countingSweeper) snapshot() (int, int, []int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.lines, c.payments, append([]int(nil), c.limits...)
}

func runOneTick(t *testing.T, sweeper *countingSweeper, logs *bytes.Buffer, opts ...sweep.Option) {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(logs, nil))
	opts = append([]sweep.Option{sweep.WithInterval(5 * time.Millisecond), sweep.WithLimit(7)}, opts...)
	runner := sweep.New(sweeper, logger, opts...)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)
		_ = runner.Run(ctx)
	}()

	if sweeper.ticked != nil {
		select {
		case <-sweeper.ticked:
		case <-time.After(2 * time.Second):
			t.Fatal("el barrido de pagos nunca corrió")
		}
	} else {
		time.Sleep(50 * time.Millisecond)
	}

	cancel()
	<-done
}

func TestRunnerReconcilesPaymentsWithSameLimit(t *testing.T) {
	sweeper := &countingSweeper{ticked: make(chan struct{}, 1)}

	runOneTick(t, sweeper, &bytes.Buffer{}, sweep.WithPaymentSync(sweeper))

	lines, payments, limits := sweeper.snapshot()
	if lines == 0 || payments == 0 {
		t.Fatalf("líneas=%d pagos=%d; quería los dos barridos", lines, payments)
	}

	for _, limit := range limits {
		if limit != 7 {
			t.Fatalf("límites = %v; quería 7 en todos", limits)
		}
	}
}

func TestRunnerWithoutPaymentsSkipsThem(t *testing.T) {
	sweeper := &countingSweeper{}

	runOneTick(t, sweeper, &bytes.Buffer{})

	lines, payments, _ := sweeper.snapshot()
	if lines == 0 {
		t.Fatal("el barrido de líneas no corrió")
	}

	if payments != 0 {
		t.Fatalf("sin WithPaymentSync no debería barrer pagos; corrió %d veces", payments)
	}
}

func TestRunnerLineErrorDoesNotStopPayments(t *testing.T) {
	sweeper := &countingSweeper{
		ticked:    make(chan struct{}, 1),
		linesErr:  errors.New("se cayó postgres"),
		paymentsE: errors.New("mercado pago no responde"),
	}
	logs := &bytes.Buffer{}

	runOneTick(t, sweeper, logs, sweep.WithPaymentSync(sweeper))

	if _, payments, _ := sweeper.snapshot(); payments == 0 {
		t.Fatal("un error en las líneas no debería frenar el barrido de pagos")
	}

	output := logs.String()
	if !strings.Contains(output, "falló el barrido de reconciliación") || !strings.Contains(output, "falló el barrido de pagos") {
		t.Fatalf("faltan los logs de los dos errores:\n%s", output)
	}
}

type cancellingLineSweeper struct {
	countingSweeper
	cancel context.CancelFunc
	done   chan struct{}
}

func (c *cancellingLineSweeper) SyncOverdueLines(ctx context.Context, limit int) error {
	_ = c.countingSweeper.SyncOverdueLines(ctx, limit)
	c.cancel()

	select {
	case c.done <- struct{}{}:
	default:
	}

	return nil
}

func TestRunnerSkipsPendingPaymentsWhenCancelledDuringLineSync(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sweeper := &cancellingLineSweeper{
		cancel: cancel,
		done:   make(chan struct{}, 1),
	}

	runner := sweep.New(sweeper, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
		sweep.WithInterval(5*time.Millisecond),
		sweep.WithPaymentSync(sweeper),
	)

	runDone := make(chan error, 1)
	go func() {
		runDone <- runner.Run(ctx)
	}()

	select {
	case <-sweeper.done:
	case <-time.After(waitTimeout):
		t.Fatal("el barrido de líneas nunca corrió")
	}

	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run devolvió %v, se esperaba nil", err)
		}
	case <-time.After(waitTimeout):
		t.Fatal("Run no terminó al cancelar el contexto")
	}

	lines, payments, _ := sweeper.snapshot()
	if lines != 1 {
		t.Fatalf("SyncOverdueLines corrió %d veces, se esperaba 1", lines)
	}
	if payments != 0 {
		t.Fatalf("SyncPendingPayments corrió %d veces tras cancelar ctx, se esperaban 0", payments)
	}
}
