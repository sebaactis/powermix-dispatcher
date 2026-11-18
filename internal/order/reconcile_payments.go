package order

import (
	"context"
	"errors"
	"fmt"
	"time"

	"compras-remotas/internal/payments"
	"compras-remotas/internal/store"
)

const paymentReconcileAge = 10 * time.Second

const paymentCancelGrace = 30 * time.Second

func (e *Engine) ReconcilePayments(ctx context.Context, limit int) error {
	if e.payments == nil {
		return nil
	}

	now := e.now()

	pending, err := e.store.PendingPaymentIntents(ctx, now.Add(-paymentReconcileAge), limit)
	if err != nil {
		return err
	}

	failures := make([]error, 0)

	for _, intent := range pending {
		if err := e.reconcilePayment(ctx, intent, now); err != nil {
			failures = append(failures, fmt.Errorf("pago de la orden %s: %w", intent.OrderID, err))
		}
	}

	return errors.Join(failures...)
}

func (e *Engine) reconcilePayment(ctx context.Context, intent store.PaymentIntent, now time.Time) error {
	if intent.ProviderID == "" {
		return nil
	}

	charge, err := e.payments.Lookup(ctx, intent.ProviderID)
	if err != nil {
		return err
	}

	overdue := !intent.ExpiresAt.IsZero() && now.After(intent.ExpiresAt.Add(paymentCancelGrace))
	if charge.Status == payments.ChargePending && overdue {
		charge, err = e.cancelCharge(ctx, intent.ProviderID)
		if err != nil {
			return err
		}
	}

	if charge.ExternalRef != intent.OrderID {
		return fmt.Errorf("%w: el cobro %s responde por la orden %q", ErrUnknownCharge, intent.ProviderID, charge.ExternalRef)
	}

	return e.applyCharge(ctx, intent, charge)
}

func (e *Engine) cancelCharge(ctx context.Context, providerID string) (payments.Charge, error) {
	cancelled, err := e.payments.Cancel(ctx, providerID)
	if err == nil {
		return cancelled, nil
	}

	if errors.Is(err, payments.ErrNotCancelable) {
		return e.payments.Lookup(ctx, providerID)
	}

	return payments.Charge{}, err
}
