package order

import (
	"crypto/rand"
	"encoding/hex"
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
	gate              Gate
	newID             func() string
	payments          payments.Gateway
	paymentProvider   string
	qrExpiry          time.Duration
	background        func(task func())
	backgroundErrors  func(orderID string, err error)

	idMu     sync.Mutex
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

func WithPayments(provider string, gateway payments.Gateway) Option {
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

func (e *Engine) Wait() {
	e.inFlight.Wait()
}

func (e *Engine) spawn(orderID string, task func() error) {
	e.inFlight.Add(1)
	e.background(func() {
		defer e.inFlight.Done()

		if err := task(); err != nil && e.backgroundErrors != nil {
			e.backgroundErrors(orderID, err)
		}
	})
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
