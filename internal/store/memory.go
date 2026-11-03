package store

import (
	"context"
	"slices"
	"sort"
	"sync"
	"time"

	"compras-remotas/internal/dispenser"
)

type webhookKey struct {
	source    string
	dedupeKey string
}

type Memory struct {
	mu        sync.Mutex
	orders    map[string]Order
	lineOrder map[string]string
	attempts  map[string]struct{}
	webhooks  map[webhookKey]struct{}
}

var _ Store = (*Memory)(nil)

func NewMemory() *Memory {
	return &Memory{
		orders:    make(map[string]Order),
		lineOrder: make(map[string]string),
		attempts:  make(map[string]struct{}),
		webhooks:  make(map[webhookKey]struct{}),
	}
}

func (m *Memory) CreateOrder(ctx context.Context, order Order) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.orders[order.ID]; exists {
		return ErrAlreadyExists
	}

	m.putOrderLocked(order)

	return nil
}

func (m *Memory) Order(ctx context.Context, orderID string) (Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	order, exists := m.orders[orderID]
	if !exists {
		return Order{}, ErrNotFound
	}

	return cloneOrder(order), nil
}

func (m *Memory) SaveOrder(ctx context.Context, order Order) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.orders[order.ID]; !exists {
		return ErrNotFound
	}

	m.putOrderLocked(order)

	return nil
}

func (m *Memory) SaveLine(ctx context.Context, line Line) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	orderID, exists := m.lineOrder[line.ID]
	if !exists {
		return ErrNotFound
	}

	order, exists := m.orders[orderID]
	if !exists {
		return ErrNotFound
	}

	for i := range order.Lines {
		if order.Lines[i].ID == line.ID {
			order.Lines[i] = line
			return nil
		}
	}

	return ErrNotFound
}

func (m *Memory) AppendAttempt(ctx context.Context, attempt Attempt) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.attempts[attempt.IdempotencyKey]; exists {
		return ErrAlreadyExists
	}

	m.attempts[attempt.IdempotencyKey] = struct{}{}

	return nil
}

func (m *Memory) RegisterWebhook(ctx context.Context, wh Webhook) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := webhookKey{source: wh.Source, dedupeKey: wh.DedupeKey}
	if _, exists := m.webhooks[key]; exists {
		return false, nil
	}

	m.webhooks[key] = struct{}{}

	return true, nil
}

func (m *Memory) DueLines(ctx context.Context, now time.Time, limit int) ([]Line, error) {
	m.mu.Lock()
	due := make([]Line, 0)
	for _, order := range m.orders {
		for _, line := range order.Lines {
			if lineDue(line, now) {
				due = append(due, line)
			}
		}
	}
	m.mu.Unlock()

	sort.Slice(due, func(i, j int) bool {
		if !due[i].DeadlineAt.Equal(due[j].DeadlineAt) {
			return due[i].DeadlineAt.Before(due[j].DeadlineAt)
		}
		return due[i].ID < due[j].ID
	})

	if limit > 0 && len(due) > limit {
		due = due[:limit]
	}

	return due, nil
}

func (m *Memory) putOrderLocked(order Order) {
	for _, line := range m.orders[order.ID].Lines {
		delete(m.lineOrder, line.ID)
	}

	stored := cloneOrder(order)
	m.orders[order.ID] = stored

	for _, line := range stored.Lines {
		m.lineOrder[line.ID] = order.ID
	}
}

func cloneOrder(order Order) Order {
	order.Lines = slices.Clone(order.Lines)

	return order
}

func lineDue(line Line, now time.Time) bool {
	if line.State != dispenser.LineSent && line.State != dispenser.LineBrewing {
		return false
	}

	if line.DeadlineAt.IsZero() {
		return false
	}

	return !line.DeadlineAt.After(now)
}
