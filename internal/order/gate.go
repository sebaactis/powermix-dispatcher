package order

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var ErrNotSellable = errors.New("el combo no se puede vender ahora")

type Gate interface {
	Check(ctx context.Context, draft Draft) error
}

type NotSellableError struct {
	Reasons []string
}

func (e *NotSellableError) Error() string {
	if len(e.Reasons) == 0 {
		return ErrNotSellable.Error()
	}

	return fmt.Sprintf("%s: %s", ErrNotSellable.Error(), strings.Join(e.Reasons, ", "))
}

func (e *NotSellableError) Unwrap() error {
	return ErrNotSellable
}

func WithGate(g Gate) Option {
	return func(engine *Engine) {
		if g != nil {
			engine.gate = g
		}
	}
}
