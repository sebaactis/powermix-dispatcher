package order

import (
	"context"
	"errors"
	"fmt"

	"compras-remotas/internal/notify"
	"compras-remotas/internal/payments"
	"compras-remotas/internal/store"
)

var ErrUnknownCharge = errors.New("el cobro no corresponde a ninguna orden de este sistema")

type orderError struct {
	orderID string
	err     error
}

func (e *orderError) Error() string {
	return e.err.Error()
}

func (e *orderError) Unwrap() error {
	return e.err
}

func wrapOrderError(orderID string, err error) error {
	if err == nil || orderID == "" {
		return err
	}
	return &orderError{orderID: orderID, err: err}
}

func OrderIDFromError(err error) (string, bool) {
	var target *orderError
	if errors.As(err, &target) && target.orderID != "" {
		return target.orderID, true
	}
	return "", false
}

func (e *Engine) SyncPayment(ctx context.Context, providerID string) error {
	if e.payments == nil {
		return ErrPaymentsDisabled
	}

	charge, err := e.payments.Lookup(ctx, providerID)
	if err != nil {
		if errors.Is(err, payments.ErrNotFound) {
			return fmt.Errorf("%w: %s no existe en el proveedor", ErrUnknownCharge, providerID)
		}

		return err
	}

	intent, err := e.store.PaymentIntentByOrder(ctx, charge.ExternalRef)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("%w: %s apunta a la orden %q", ErrUnknownCharge, providerID, charge.ExternalRef)
		}

		return err
	}

	if intent.ProviderID != charge.ProviderID {
		return wrapOrderError(intent.OrderID, fmt.Errorf("%w: la orden %s tiene el cobro %s y no %s", ErrUnknownCharge, intent.OrderID, intent.ProviderID, charge.ProviderID))
	}

	return wrapOrderError(intent.OrderID, e.applyCharge(ctx, intent, charge))
}

func (e *Engine) applyCharge(ctx context.Context, intent store.PaymentIntent, charge payments.Charge) error {
	if intent.Status != store.PaymentPending {
		return nil
	}

	switch charge.Status {
	case payments.ChargePending:
		return nil
	case payments.ChargeApproved:
		if charge.PaidAmountCents != intent.AmountCents {
			return e.rejectPaidAmount(ctx, intent, charge)
		}

		return e.acceptPayment(ctx, intent, charge)
	case payments.ChargeExpired:
		return e.closeUnpaid(ctx, intent, store.PaymentExpired)
	case payments.ChargeCancelled:
		return e.closeUnpaid(ctx, intent, store.PaymentCancelled)
	default:
		return e.notifyOnce(ctx, notify.Event{
			Kind:    notify.KindPaymentUnknown,
			OrderID: intent.OrderID,
			Detail: fmt.Sprintf("el cobro %s quedó en un estado que el sistema no sabe tratar (%s/%s): no se despachó ni se canceló",
				charge.ProviderID, charge.ProviderStatus, charge.ProviderStatusDetail),
		})
	}
}

func (e *Engine) acceptPayment(ctx context.Context, intent store.PaymentIntent, charge payments.Charge) error {
	now := e.now()

	loaded, started, err := e.claimPaid(ctx, intent.OrderID, now)
	if err != nil {
		return err
	}

	if started {
		detached := context.WithoutCancel(ctx)
		e.spawn(intent.OrderID, func() error {
			return e.dispatchLines(detached, intent.OrderID, loaded.Lines)
		})
	} else if loaded.Status == store.OrderCancelled {
		if err := e.notifyOnce(ctx, notify.Event{
			Kind:    notify.KindPaymentUnknown,
			OrderID: intent.OrderID,
			Detail:  fmt.Sprintf("el cobro %s figura pagado pero la orden ya estaba cancelada: no se despachó", charge.ProviderID),
		}); err != nil {
			return err
		}
	}

	intent.Status = store.PaymentApproved
	intent.PaidAmountCents = charge.PaidAmountCents
	intent.ApprovedAt = now

	return e.savePaymentIntent(ctx, intent)
}

func (e *Engine) rejectPaidAmount(ctx context.Context, intent store.PaymentIntent, charge payments.Charge) error {
	if err := e.notifyOnce(ctx, notify.Event{
		Kind:    notify.KindPaymentMismatch,
		OrderID: intent.OrderID,
		Detail: fmt.Sprintf("el cobro %s se pagó con %s y la orden vale %s: no se despachó",
			charge.ProviderID, notify.Pesos(charge.PaidAmountCents), notify.Pesos(intent.AmountCents)),
	}); err != nil {
		return err
	}

	intent.Status = store.PaymentAmountMismatch
	intent.PaidAmountCents = charge.PaidAmountCents

	return e.savePaymentIntent(ctx, intent)
}

func (e *Engine) closeUnpaid(ctx context.Context, intent store.PaymentIntent, status store.PaymentStatus) error {
	loaded, err := e.store.LoadOrder(ctx, intent.OrderID)
	if err != nil {
		return err
	}

	if loaded.Status == store.OrderPendingPayment {
		loaded.Status = store.OrderCancelled
		e.markClosed(&loaded)

		if _, err := e.store.SaveOrder(ctx, loaded); err != nil {
			return err
		}
	}

	intent.Status = status

	return e.savePaymentIntent(ctx, intent)
}

func (e *Engine) savePaymentIntent(ctx context.Context, intent store.PaymentIntent) error {
	if _, err := e.store.SavePaymentIntent(ctx, intent); err != nil && !errors.Is(err, store.ErrStale) {
		return err
	}

	return nil
}
