package order

import (
	"context"
	"errors"
	"fmt"
	"time"

	"compras-remotas/internal/payments"
	"compras-remotas/internal/store"
)

const defaultQRExpiry = 180 * time.Second

var ErrPaymentsDisabled = errors.New("el cobro con QR no está configurado")

var ErrNotPayable = errors.New("la orden no está esperando el pago")

func (e *Engine) RequestQR(ctx context.Context, orderID, posID string) (store.PaymentIntent, error) {
	if e.payments == nil {
		return store.PaymentIntent{}, ErrPaymentsDisabled
	}

	existing, err := e.store.PaymentIntentByOrder(ctx, orderID)
	switch {
	case err == nil:
		return existing, nil
	case !errors.Is(err, store.ErrNotFound):
		return store.PaymentIntent{}, err
	}

	loaded, err := e.store.LoadOrder(ctx, orderID)
	if err != nil {
		return store.PaymentIntent{}, err
	}

	if loaded.Status != store.OrderPendingPayment {
		return store.PaymentIntent{}, fmt.Errorf("%w: la orden %s está en %s", ErrNotPayable, orderID, loaded.Status)
	}

	if loaded.AmountCents <= 0 {
		return store.PaymentIntent{}, fmt.Errorf("%w: la orden %s tiene importe %d", ErrNotPayable, orderID, loaded.AmountCents)
	}

	now := e.now()

	charge, err := e.payments.CreateQR(ctx, payments.QRRequest{
		OrderID:     orderID,
		PosID:       posID,
		AmountCents: loaded.AmountCents,
		Title:       "Combo " + loaded.ComboID,
		ExpiresIn:   e.qrExpiry,
	})
	if err != nil {
		return store.PaymentIntent{}, fmt.Errorf("orden %s: no se pudo crear el QR: %w", orderID, err)
	}

	intent := store.PaymentIntent{
		ID:          e.nextID(),
		OrderID:     orderID,
		Provider:    e.paymentProvider,
		ProviderID:  charge.ProviderID,
		AmountCents: loaded.AmountCents,
		Status:      store.PaymentPending,
		QRData:      charge.QRData,
		CreatedAt:   now,
		ExpiresAt:   now.Add(e.qrExpiry),
	}

	if err := e.store.CreatePaymentIntent(ctx, intent); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			return e.store.PaymentIntentByOrder(ctx, orderID)
		}

		return store.PaymentIntent{}, err
	}

	return intent, nil
}
