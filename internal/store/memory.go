package store

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"compras-remotas/internal/linestate"
	"compras-remotas/internal/machine"
)

const refundedState linestate.State = "refunded"

type webhookKey struct {
	source    string
	dedupeKey string
}

type notificationKey struct {
	orderID string
	kind    string
}

type lineAttemptKey struct {
	lineID string
	number int
}

type Memory struct {
	mu            sync.Mutex
	orders        map[string]Order
	lineOrder     map[string]string
	clientKeys    map[string]string
	attempts      map[string]struct{}
	lineAttempts  map[lineAttemptKey]struct{}
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
		lineAttempts:  make(map[lineAttemptKey]struct{}),
		webhooks:      make(map[webhookKey]struct{}),
		notifications: make(map[notificationKey]time.Time),
	}
}

func (m *Memory) CreateOrder(ctx context.Context, order Order) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := validateOrder(order); err != nil {
		return err
	}

	if _, exists := m.orders[order.ID]; exists {
		return ErrAlreadyExists
	}

	if order.ClientKey != "" {
		if _, exists := m.clientKeys[order.ClientKey]; exists {
			return ErrAlreadyExists
		}
	}

	positions := make(map[int]struct{}, len(order.Lines))

	for _, line := range order.Lines {
		if err := validateLine(line); err != nil {
			return err
		}

		if _, taken := positions[line.Position]; taken {
			return ErrAlreadyExists
		}

		positions[line.Position] = struct{}{}
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

	if err := validateOrder(order); err != nil {
		return Order{}, err
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

		if err := validateLine(line); err != nil {
			return Line{}, err
		}

		for _, other := range order.Lines {
			if other.ID != line.ID && other.Position == line.Position {
				return Line{}, ErrAlreadyExists
			}
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

	if err := validateAttempt(attempt); err != nil {
		return err
	}

	if _, exists := m.attempts[attempt.IdempotencyKey]; exists {
		return ErrAlreadyExists
	}

	lineKey := lineAttemptKey{lineID: attempt.LineID, number: attempt.Number}
	if _, exists := m.lineAttempts[lineKey]; exists {
		return ErrAlreadyExists
	}

	m.attempts[attempt.IdempotencyKey] = struct{}{}
	m.lineAttempts[lineKey] = struct{}{}

	return nil
}

func (m *Memory) SaveAttempt(ctx context.Context, attempt Attempt) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.attempts[attempt.IdempotencyKey]; !exists {
		return ErrNotFound
	}

	if err := validateAttempt(attempt); err != nil {
		return err
	}

	return nil
}

func (m *Memory) RegisterWebhook(ctx context.Context, wh Webhook) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := validateWebhook(wh); err != nil {
		return false, err
	}

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

func validateOrder(order Order) error {
	if order.AmountCents < 0 {
		return fmt.Errorf("%w: la orden %s tiene el importe %d", ErrInvalid, order.ID, order.AmountCents)
	}

	if !knownOrderStatus(order.Status) {
		return fmt.Errorf("%w: la orden %s tiene el estado %q", ErrInvalid, order.ID, order.Status)
	}

	return nil
}

func validateLine(line Line) error {
	if !knownMachineKind(line.Machine.Kind) {
		return fmt.Errorf("%w: la línea %s tiene la familia de máquina %q", ErrInvalid, line.ID, line.Machine.Kind)
	}

	if line.AmountCents < 0 {
		return fmt.Errorf("%w: la línea %s tiene el importe %d", ErrInvalid, line.ID, line.AmountCents)
	}

	if !knownLineState(line.State) {
		return fmt.Errorf("%w: la línea %s tiene el estado %q", ErrInvalid, line.ID, line.State)
	}

	return nil
}

func validateAttempt(attempt Attempt) error {
	if !knownAttemptOutcome(attempt.Outcome) {
		return fmt.Errorf("%w: el intento %s tiene el desenlace %q", ErrInvalid, attempt.ID, attempt.Outcome)
	}

	return nil
}

func validateWebhook(wh Webhook) error {
	if !knownWebhookSource(wh.Source) {
		return fmt.Errorf("%w: el webhook %s tiene la fuente %q", ErrInvalid, wh.ID, wh.Source)
	}

	return nil
}

func knownOrderStatus(status OrderStatus) bool {
	switch status {
	case OrderPendingPayment, OrderPaid, OrderDispatching, OrderDelivered, OrderPartial, OrderFailed, OrderInDoubt, OrderRefunded, OrderCancelled:
		return true
	}

	return false
}

func knownMachineKind(kind machine.Kind) bool {
	return kind == machine.Batidos || kind == machine.Snacks
}

func knownLineState(state linestate.State) bool {
	switch state {
	case linestate.Pending, linestate.Sent, linestate.Brewing, linestate.Delivered, linestate.Failed, linestate.InDoubt, refundedState:
		return true
	}

	return false
}

func knownAttemptOutcome(outcome AttemptOutcome) bool {
	switch outcome {
	case OutcomeAmbiguous, OutcomeAccepted, OutcomeRejected, OutcomeError:
		return true
	}

	return false
}

func knownWebhookSource(source string) bool {
	switch source {
	case "payment", "batidos", "snacks":
		return true
	}

	return false
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
