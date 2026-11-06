package order

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"sync"
	"time"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/notify"
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
	Machine     dispenser.MachineRef
}

type Option func(*Engine)

type Engine struct {
	store             store.Store
	machines          map[string]dispenser.Dispenser
	now               func() time.Time
	deadline          time.Duration
	reconcileWindow   time.Duration
	reconcileInterval time.Duration
	maxAttempts       int
	notifier          notify.Notifier
	gate              Gate
	newID             func() string

	idMu sync.Mutex
}

func New(st store.Store, machines map[string]dispenser.Dispenser, opts ...Option) *Engine {
	engine := &Engine{
		store:             st,
		machines:          machines,
		now:               time.Now,
		deadline:          defaultLineDeadline,
		reconcileWindow:   defaultReconcileWindow,
		reconcileInterval: defaultReconcileInterval,
		maxAttempts:       defaultMaxAttempts,
		newID:             newRandomID,
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
