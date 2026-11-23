package sweep

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"runtime/debug"
	"time"
)

const (
	defaultInterval = 30 * time.Second
	defaultLimit    = 10
)

type OverdueLineSyncer interface {
	SyncOverdueLines(ctx context.Context, limit int) error
}

type PendingPaymentSyncer interface {
	SyncPendingPayments(ctx context.Context, limit int) error
}

type Option func(*Runner)

type Runner struct {
	sweeper  OverdueLineSyncer
	payments PendingPaymentSyncer
	logger   *slog.Logger
	interval time.Duration
	limit    int
}

func WithInterval(d time.Duration) Option {
	return func(runner *Runner) {
		if d > 0 {
			runner.interval = d
		}
	}
}

func WithLimit(n int) Option {
	return func(runner *Runner) {
		if n > 0 {
			runner.limit = n
		}
	}
}

func WithPaymentSync(p PendingPaymentSyncer) Option {
	return func(runner *Runner) {
		if p != nil {
			runner.payments = p
		}
	}
}

func New(s OverdueLineSyncer, logger *slog.Logger, opts ...Option) *Runner {
	runner := &Runner{
		sweeper:  s,
		logger:   logger,
		interval: defaultInterval,
		limit:    defaultLimit,
	}

	if runner.logger == nil {
		runner.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	for _, opt := range opts {
		if opt != nil {
			opt(runner)
		}
	}

	return runner
}

func (r *Runner) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			r.tick(ctx)
		}
	}
}

func (r *Runner) tick(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}

	r.runStage(ctx, "reconciliación", r.sweeper.SyncOverdueLines)

	if r.payments == nil || ctx.Err() != nil {
		return
	}

	r.runStage(ctx, "pagos", r.payments.SyncPendingPayments)
}

func (r *Runner) callStage(ctx context.Context, name string, sync func(context.Context, int) error) (err error) {
	defer func() {
		if p := recover(); p != nil {
			r.logger.Error("panic en el barrido de "+name+"; sigue en la próxima vuelta",
				"stage", name,
				"panic", p,
				"stack", string(debug.Stack()),
			)
			err = nil
		}
	}()

	return sync(ctx, r.limit)
}

func (r *Runner) runStage(ctx context.Context, name string, sync func(context.Context, int) error) {
	stageCtx, cancel := context.WithTimeout(ctx, r.interval)
	defer cancel()

	err := r.callStage(stageCtx, name, sync)

	if ctx.Err() == nil && errors.Is(stageCtx.Err(), context.DeadlineExceeded) {
		r.logger.Warn("el barrido agotó su plazo; sigue en la próxima vuelta",
			"stage", name,
			"timeout", r.interval,
			"limit", r.limit,
			"error", err,
		)
		return
	}

	if ctx.Err() != nil && errors.Is(err, context.Canceled) {
		return
	}

	if err != nil {
		r.logger.Error("falló el barrido de "+name, "error", err, "limit", r.limit)
	}
}
