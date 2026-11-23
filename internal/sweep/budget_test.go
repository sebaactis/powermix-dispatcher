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

type slowLineSweeper struct {
	mu              sync.Mutex
	lineDeadline    time.Duration
	paymentsLeft    time.Duration
	paymentsStarted chan struct{}
}

func (s *slowLineSweeper) SyncOverdueLines(ctx context.Context, _ int) error {
	deadline, ok := ctx.Deadline()

	s.mu.Lock()
	if ok {
		s.lineDeadline = time.Until(deadline)
	}
	s.mu.Unlock()

	<-ctx.Done()

	return ctx.Err()
}

func (s *slowLineSweeper) SyncPendingPayments(ctx context.Context, _ int) error {
	deadline, ok := ctx.Deadline()

	s.mu.Lock()
	if ok {
		s.paymentsLeft = time.Until(deadline)
	} else {
		s.paymentsLeft = -1
	}
	s.mu.Unlock()

	select {
	case s.paymentsStarted <- struct{}{}:
	default:
	}

	return ctx.Err()
}

func TestRunnerGivesEachStageItsOwnBudget(t *testing.T) {
	const interval = 50 * time.Millisecond

	sweeper := &slowLineSweeper{paymentsStarted: make(chan struct{}, 1)}
	logs := &bytes.Buffer{}
	var logsMu sync.Mutex
	logger := slog.New(slog.NewTextHandler(&lockedWriter{mu: &logsMu, w: logs}, nil))

	runner := sweep.New(sweeper, logger, sweep.WithInterval(interval), sweep.WithPaymentSync(sweeper))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = runner.Run(ctx)
	}()

	select {
	case <-sweeper.paymentsStarted:
	case <-time.After(waitTimeout):
		cancel()
		<-done
		t.Fatal("un barrido de líneas lento no debería impedir que corra el de pagos")
	}

	cancel()
	<-done

	sweeper.mu.Lock()
	lineDeadline, paymentsLeft := sweeper.lineDeadline, sweeper.paymentsLeft
	sweeper.mu.Unlock()

	if lineDeadline <= 0 || lineDeadline > interval {
		t.Fatalf("el barrido de líneas tenía %v de plazo, se esperaba como máximo el intervalo (%v)", lineDeadline, interval)
	}
	if paymentsLeft <= interval/2 {
		t.Fatalf("el barrido de pagos arrancó con %v de plazo: tenía que tener su propio plazo y no lo que sobró de las líneas", paymentsLeft)
	}

	logsMu.Lock()
	output := logs.String()
	logsMu.Unlock()

	if !strings.Contains(output, "el barrido agotó su plazo") || !strings.Contains(output, "stage=reconciliación") {
		t.Fatalf("faltaba el aviso de plazo agotado del barrido de líneas:\n%s", output)
	}
	if strings.Contains(output, "falló el barrido de reconciliación") {
		t.Fatalf("un plazo agotado no es una falla del barrido:\n%s", output)
	}
}

func TestRunnerStillReportsStageErrorsWithinTheBudget(t *testing.T) {
	sweeper := &countingSweeper{ticked: make(chan struct{}, 1), linesErr: errors.New("se cayó postgres")}
	logs := &bytes.Buffer{}

	runOneTick(t, sweeper, logs, sweep.WithPaymentSync(sweeper))

	output := logs.String()
	if !strings.Contains(output, "falló el barrido de reconciliación") {
		t.Fatalf("un error dentro del plazo se tiene que loguear como falla:\n%s", output)
	}
	if strings.Contains(output, "agotó su plazo") {
		t.Fatalf("un error dentro del plazo no es un plazo agotado:\n%s", output)
	}
}

type lockedWriter struct {
	mu *sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.w.Write(p)
}
