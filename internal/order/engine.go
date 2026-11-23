package order

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/machine"
	"compras-remotas/internal/notify"
	"compras-remotas/internal/payments"
	"compras-remotas/internal/store"
)

type Draft struct {
	KioskID     string
	ComboID     string
	ClientKey   string
	AmountCents int64
	Currency    string
	Items       []Item
}

type Item struct {
	SKU         string
	AmountCents int64
	Machine     machine.Ref
}

type Option func(*Engine)

type Engine struct {
	store             store.Store
	machines          map[machine.Kind]dispenser.Dispenser
	now               func() time.Time
	deadline          time.Duration
	reconcileWindow   time.Duration
	reconcileInterval time.Duration
	maxAttempts       int
	notifier          notify.Notifier
	gate              AvailabilityChecker
	newID             func() string
	payments          payments.Gateway
	paymentProvider   string
	qrExpiry          time.Duration
	background        func(task func())
	backgroundErrors  func(orderID string, err error)
	logger            *slog.Logger

	idMu     sync.Mutex
	bgMu     sync.Mutex
	closed   bool
	inFlight sync.WaitGroup
}

func New(st store.Store, machines map[machine.Kind]dispenser.Dispenser, opts ...Option) *Engine {
	engine := &Engine{
		store:             st,
		machines:          machines,
		now:               time.Now,
		deadline:          defaultLineDeadline,
		reconcileWindow:   defaultReconcileWindow,
		reconcileInterval: defaultReconcileInterval,
		maxAttempts:       defaultMaxAttempts,
		newID:             newRandomID,
		qrExpiry:          defaultQRExpiry,
		background:        func(task func()) { go task() },
	}

	for _, opt := range opts {
		if opt != nil {
			opt(engine)
		}
	}

	return engine
}

func WithClock(now func() time.Time) Option {
	return func(engine *Engine) {
		if now != nil {
			engine.now = now
		}
	}
}

func WithLineDeadline(d time.Duration) Option {
	return func(engine *Engine) {
		if d > 0 {
			engine.deadline = d
		}
	}
}

func WithReconcileWindow(d time.Duration) Option {
	return func(engine *Engine) {
		if d > 0 {
			engine.reconcileWindow = d
		}
	}
}

func WithReconcileInterval(d time.Duration) Option {
	return func(engine *Engine) {
		if d > 0 {
			engine.reconcileInterval = d
		}
	}
}

func WithNotifier(n notify.Notifier) Option {
	return func(engine *Engine) {
		if n != nil {
			engine.notifier = n
		}
	}
}

func WithMaxAttempts(n int) Option {
	return func(engine *Engine) {
		if n > 0 {
			engine.maxAttempts = n
		}
	}
}

func WithIDGenerator(newID func() string) Option {
	return func(engine *Engine) {
		if newID != nil {
			engine.newID = newID
		}
	}
}

func WithQRPayments(provider string, gateway payments.Gateway) Option {
	return func(engine *Engine) {
		if provider != "" && gateway != nil {
			engine.paymentProvider = provider
			engine.payments = gateway
		}
	}
}

func WithQRExpiry(d time.Duration) Option {
	return func(engine *Engine) {
		if d > 0 {
			engine.qrExpiry = d
		}
	}
}

func WithBackground(run func(task func())) Option {
	return func(engine *Engine) {
		if run != nil {
			engine.background = run
		}
	}
}

func WithBackgroundErrors(report func(orderID string, err error)) Option {
	return func(engine *Engine) {
		if report != nil {
			engine.backgroundErrors = report
		}
	}
}

func WithLogger(logger *slog.Logger) Option {
	return func(engine *Engine) {
		if logger != nil {
			engine.logger = logger
		}
	}
}

func (e *Engine) log() *slog.Logger {
	if e.logger != nil {
		return e.logger
	}

	return slog.Default()
}

func (e *Engine) Wait() {
	e.bgMu.Lock()
	e.closed = true
	e.bgMu.Unlock()

	e.inFlight.Wait()
}

func (e *Engine) spawn(orderID string, task func() error) {
	e.bgMu.Lock()
	if e.closed {
		e.bgMu.Unlock()
		e.log().Warn("motor cerrado al apagar: no se inicia el despacho y el barrido lo retoma al reiniciar", "order_id", orderID)
		return
	}
	e.inFlight.Add(1)
	e.bgMu.Unlock()

	e.background(func() {
		defer e.inFlight.Done()
		defer func() {
			if r := recover(); r != nil {
				err := e.recoverPanic(context.Background(), "panic in background task", r, debug.Stack(),
					"order_id", orderID,
				)
				err = errors.Join(err, e.recoverOrder(context.Background(), orderID))
				if e.backgroundErrors != nil {
					e.backgroundErrors(orderID, err)
				}
			}
		}()

		if err := task(); err != nil && e.backgroundErrors != nil {
			e.backgroundErrors(orderID, err)
		}
	})
}

func (e *Engine) recoverOrder(ctx context.Context, orderID string) error {
	settleErr := e.guard(ctx, "panic al recalcular la orden tras un panic", func() error {
		return e.settleOrder(ctx, orderID)
	}, "order_id", orderID)

	notifyErr := e.guard(ctx, "panic al avisar el panic de un pedido", func() error {
		return e.notifyOnce(ctx, notify.Event{
			Kind:    notify.KindDispatchPanic,
			OrderID: orderID,
			Detail:  "el procesamiento del pedido sufrió un panic fuera del despacho de una línea: verificar el estado del pedido y si salió el producto",
		})
	}, "order_id", orderID)

	return errors.Join(settleErr, notifyErr)
}

func (e *Engine) guard(ctx context.Context, msg string, fn func() error, attrs ...any) error {
	_, err := e.guardPanicked(ctx, msg, fn, attrs...)

	return err
}

func (e *Engine) guardPanicked(ctx context.Context, msg string, fn func() error, attrs ...any) (panicked bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
			err = e.recoverPanic(ctx, msg, r, debug.Stack(), attrs...)
		}
	}()

	return false, fn()
}

func (e *Engine) recoverPanic(ctx context.Context, msg string, panicked any, stack []byte, attrs ...any) error {
	args := make([]any, 0, len(attrs)+4)
	args = append(args, attrs...)
	args = append(args, "panic", panicked, "stack", string(stack))

	e.log().ErrorContext(ctx, msg, args...)

	return fmt.Errorf("%s: %v", msg, panicked)
}

func (e *Engine) nextID() string {
	e.idMu.Lock()
	defer e.idMu.Unlock()

	return e.newID()
}

func newRandomID() string {
	var buf [16]byte

	if _, err := rand.Read(buf[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}

	return hex.EncodeToString(buf[:])
}
