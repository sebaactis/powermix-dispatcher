package store

import (
	"context"
	"fmt"
	"sort"
	"time"
)

type providerKey struct {
	provider   string
	providerID string
}

func (m *Memory) CreatePaymentIntent(ctx context.Context, intent PaymentIntent) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := validatePaymentIntent(intent); err != nil {
		return err
	}

	if _, exists := m.orders[intent.OrderID]; !exists {
		return ErrNotFound
	}

	if _, exists := m.intents[intent.ID]; exists {
		return ErrAlreadyExists
	}

	if _, exists := m.orderIntents[intent.OrderID]; exists {
		return ErrAlreadyExists
	}

	key := providerKey{provider: intent.Provider, providerID: intent.ProviderID}
	if intent.ProviderID != "" {
		if _, exists := m.providerIDs[key]; exists {
			return ErrAlreadyExists
		}
	}

	intent.Version = 0

	m.intents[intent.ID] = intent
	m.orderIntents[intent.OrderID] = intent.ID

	if intent.ProviderID != "" {
		m.providerIDs[key] = intent.ID
	}

	return nil
}

func (m *Memory) PaymentIntentByOrder(ctx context.Context, orderID string) (PaymentIntent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	intentID, exists := m.orderIntents[orderID]
	if !exists {
		return PaymentIntent{}, ErrNotFound
	}

	return m.intents[intentID], nil
}

func (m *Memory) SavePaymentIntent(ctx context.Context, intent PaymentIntent) (PaymentIntent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	stored, exists := m.intents[intent.ID]
	if !exists {
		return PaymentIntent{}, ErrNotFound
	}

	if stored.Version != intent.Version {
		return PaymentIntent{}, ErrStale
	}

	if err := validatePaymentIntent(intent); err != nil {
		return PaymentIntent{}, err
	}

	key := providerKey{provider: intent.Provider, providerID: intent.ProviderID}
	if intent.ProviderID != "" {
		if owner, taken := m.providerIDs[key]; taken && owner != intent.ID {
			return PaymentIntent{}, ErrAlreadyExists
		}
	}

	delete(m.providerIDs, providerKey{provider: stored.Provider, providerID: stored.ProviderID})

	if intent.ProviderID != "" {
		m.providerIDs[key] = intent.ID
	}

	intent.OrderID = stored.OrderID
	intent.Version = stored.Version + 1
	m.intents[intent.ID] = intent

	return intent, nil
}

func (m *Memory) PendingPaymentIntents(ctx context.Context, createdBefore time.Time, limit int) ([]PaymentIntent, error) {
	m.mu.Lock()
	pending := make([]PaymentIntent, 0)
	for _, intent := range m.intents {
		if intent.Status != PaymentPending || intent.CreatedAt.After(createdBefore) {
			continue
		}

		pending = append(pending, intent)
	}
	m.mu.Unlock()

	sort.Slice(pending, func(i, j int) bool {
		if !pending[i].CreatedAt.Equal(pending[j].CreatedAt) {
			return pending[i].CreatedAt.Before(pending[j].CreatedAt)
		}
		return pending[i].ID < pending[j].ID
	})

	if limit > 0 && len(pending) > limit {
		pending = pending[:limit]
	}

	return pending, nil
}

func validatePaymentIntent(intent PaymentIntent) error {
	if !knownPaymentStatus(intent.Status) {
		return fmt.Errorf("%w: el pago %s tiene el estado %q", ErrInvalid, intent.ID, intent.Status)
	}

	if intent.AmountCents < 0 {
		return fmt.Errorf("%w: el pago %s tiene el importe %d", ErrInvalid, intent.ID, intent.AmountCents)
	}

	if intent.PaidAmountCents < 0 {
		return fmt.Errorf("%w: el pago %s tiene el importe pagado %d", ErrInvalid, intent.ID, intent.PaidAmountCents)
	}

	return nil
}

func knownPaymentStatus(status PaymentStatus) bool {
	switch status {
	case PaymentPending, PaymentApproved, PaymentExpired, PaymentCancelled, PaymentAmountMismatch:
		return true
	}

	return false
}
