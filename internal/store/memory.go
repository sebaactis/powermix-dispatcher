package store

import (
	"context"
	"slices"
	"sort"
	"sync"
	"time"

	"compras-remotas/internal/linestate"
)

type webhookKey struct {
	source    string
	dedupeKey string
}

type notificationKey struct {
	orderID string
	kind    string
}

type Memory struct {
	mu            sync.Mutex
	orders        map[string]Order
	lineOrder     map[string]string
	clientKeys    map[string]string
	attempts      map[string]struct{}
	webhooks      map[webhookKey]struct{}
	notifications map[notificationKey]time.Time
}

var _ Store = (*Memory)(nil)

func NewMemory() *Memory {
	return &Memory{
		orders:        make(map[string]Order),
		lineOrder:     make(map[string]string),
		clientKeys:    make(map[string]string),
		attempts:      make(map[string]struct{}),
		webhooks:      make(map[webhookKey]struct{}),
		notifications: make(map[notificationKey]time.Time),
	}
}

func (m *Memory) CreateOrder(ctx context.Context, order Order) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.orders[order.ID]; exists {
		return ErrAlreadyExists
	}

	if order.ClientKey != "" {
		if _, exists := m.clientKeys[order.ClientKey]; exists {
			return ErrAlreadyExists
		}
	}

	order = cloneOrder(order)

	for i := range order.Lines {
		order.Lines[i].Version = 0
	}

	m.putOrderLocked(order)

	if order.ClientKey != "" {
		m.clientKeys[order.ClientKey] = order.ID
	}

	return nil
}

func (m *Memory) OrderByClientKey(ctx context.Context, key string) (Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	orderID, exists := m.clientKeys[key]
	if !exists {
		return Order{}, ErrNotFound
	}

	order, exists := m.orders[orderID]
	if !exists {
		return Order{}, ErrNotFound
	}

	return cloneOrder(order), nil
}

func (m *Memory) LoadOrder(ctx context.Context, orderID string) (Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	order, exists := m.orders[orderID]
	if !exists {
		return Order{}, ErrNotFound
	}

	return cloneOrder(order), nil
}

func (m *Memory) SaveOrder(ctx context.Context, order Order) (Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	stored, exists := m.orders[order.ID]
	if !exists {
		return Order{}, ErrNotFound
	}

	if stored.Version != order.Version {
		return Order{}, ErrStale
	}

	stored.KioskID = order.KioskID
	stored.ComboID = order.ComboID
	stored.ClientKey = order.ClientKey
	stored.AmountCents = order.AmountCents
	stored.Currency = order.Currency
	stored.Status = order.Status
	stored.CreatedAt = order.CreatedAt
	stored.PaidAt = order.PaidAt
	stored.ClosedAt = order.ClosedAt
	stored.Version = order.Version + 1

	m.orders[order.ID] = stored

	order.Version = stored.Version

	return cloneOrder(order), nil
}

func (m *Memory) SaveLine(ctx context.Context, line Line) (Line, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	orderID, exists := m.lineOrder[line.ID]
	if !exists {
		return Line{}, ErrNotFound
	}

	order, exists := m.orders[orderID]
	if !exists {
		return Line{}, ErrNotFound
	}

	for i := range order.Lines {
		if order.Lines[i].ID != line.ID {
			continue
		}

		if order.Lines[i].Version != line.Version {
			return Line{}, ErrStale
		}

		line.Version = order.Lines[i].Version + 1
		order.Lines[i] = line

		return line, nil
	}

	return Line{}, ErrNotFound
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

func (m *Memory) SaveAttempt(ctx context.Context, attempt Attempt) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.attempts[attempt.IdempotencyKey]; !exists {
		return ErrNotFound
	}

	return nil
}

func (m *Memory) RegisterWebhook(ctx context.Context, wh Webhook) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := webhookKey{source: wh.Source, dedupeKey: wh.DedupeKey}
	if _, exists := m.webhooks[key]; exists {
		return true, nil
	}

	m.webhooks[key] = struct{}{}

	return false, nil
}

func (m *Memory) NotificationSent(ctx context.Context, orderID, kind string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.orders[orderID]; !exists {
		return false, ErrNotFound
	}

	_, sent := m.notifications[notificationKey{orderID: orderID, kind: kind}]

	return sent, nil
}

func (m *Memory) RecordNotification(ctx context.Context, orderID, kind string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.orders[orderID]; !exists {
		return ErrNotFound
	}

	m.notifications[notificationKey{orderID: orderID, kind: kind}] = at

	return nil
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
	switch line.State {
	case linestate.Pending, linestate.Sent, linestate.Brewing, linestate.InDoubt:
	default:
		return false
	}

	if line.DeadlineAt.IsZero() {
		return false
	}

	return !line.DeadlineAt.After(now)
}

func (m *Memory) StalledLines(ctx context.Context, paidBefore time.Time, limit int) ([]Line, error) {
	type stalledLine struct {
		paidAt time.Time
		line   Line
	}

	m.mu.Lock()
	found := make([]stalledLine, 0)
	for _, order := range m.orders {
		if order.Status != OrderPaid && order.Status != OrderDispatching {
			continue
		}
		if order.PaidAt.IsZero() || order.PaidAt.After(paidBefore) {
			continue
		}

		for _, line := range order.Lines {
			if line.State != linestate.Pending || !line.DeadlineAt.IsZero() {
				continue
			}

			found = append(found, stalledLine{paidAt: order.PaidAt, line: line})
		}
	}
	m.mu.Unlock()

	sort.Slice(found, func(i, j int) bool {
		if !found[i].paidAt.Equal(found[j].paidAt) {
			return found[i].paidAt.Before(found[j].paidAt)
		}
		return found[i].line.ID < found[j].line.ID
	})

	lines := make([]Line, 0, len(found))
	for _, item := range found {
		lines = append(lines, item.line)
	}

	if limit > 0 && len(lines) > limit {
		lines = lines[:limit]
	}

	return lines, nil
}
