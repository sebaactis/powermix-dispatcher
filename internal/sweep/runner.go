package sweep

import (
	"context"
	"io"
	"log/slog"
	"time"
)

const (
	defaultInterval = 30 * time.Second
	defaultLimit    = 10
)

type Sweeper interface {
	ReconcileDue(ctx context.Context, limit int) error
}

type Option func(*Runner)

type Runner struct {
	sweeper  Sweeper
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

func New(s Sweeper, logger *slog.Logger, opts ...Option) *Runner {
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
	if err := r.sweeper.ReconcileDue(ctx, r.limit); err != nil {
		r.logger.Error("falló el barrido de reconciliación", "error", err, "limit", r.limit)
	}
}
