package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"compras-remotas/internal/store"
)

const intentColumns = `id, order_id, provider, provider_intent_id, amount_cents, paid_amount_cents, status,
	qr_payload, created_at, expires_at, approved_at, version`

func (s *Store) CreatePaymentIntent(ctx context.Context, intent store.PaymentIntent) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO payment_intent (`+intentColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, COALESCE($9, now()), $10, $11, 0)`,
		intent.ID, intent.OrderID, intent.Provider, nullableText(intent.ProviderID), intent.AmountCents,
		nullableAmount(intent.PaidAmountCents), string(intent.Status), nullableText(intent.QRData),
		nullableTime(intent.CreatedAt), nullableTime(intent.ExpiresAt), nullableTime(intent.ApprovedAt))
	if err != nil {
		if isForeignKeyViolation(err) {
			return store.ErrNotFound
		}

		return mapWriteError(err, "el pago", intent.ID)
	}

	return nil
}

func (s *Store) PaymentIntentByOrder(ctx context.Context, orderID string) (store.PaymentIntent, error) {
	intent, err := scanIntent(s.db.QueryRowContext(ctx,
		`SELECT `+intentColumns+` FROM payment_intent WHERE order_id = $1`, orderID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.PaymentIntent{}, store.ErrNotFound
		}

		return store.PaymentIntent{}, fmt.Errorf("no se pudo leer el pago de la orden %s: %w", orderID, err)
	}

	return intent, nil
}

func (s *Store) SavePaymentIntent(ctx context.Context, intent store.PaymentIntent) (store.PaymentIntent, error) {
	row := s.db.QueryRowContext(ctx, `UPDATE payment_intent SET
		provider = $3,
		provider_intent_id = $4,
		amount_cents = $5,
		paid_amount_cents = $6,
		status = $7,
		qr_payload = $8,
		created_at = COALESCE($9, created_at),
		expires_at = $10,
		approved_at = $11,
		version = version + 1
		WHERE id = $1 AND version = $2
		RETURNING `+intentColumns,
		intent.ID, intent.Version, intent.Provider, nullableText(intent.ProviderID), intent.AmountCents,
		nullableAmount(intent.PaidAmountCents), string(intent.Status), nullableText(intent.QRData),
		nullableTime(intent.CreatedAt), nullableTime(intent.ExpiresAt), nullableTime(intent.ApprovedAt))

	saved, err := scanIntent(row)
	if err == nil {
		return saved, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return store.PaymentIntent{}, mapWriteError(err, "el pago", intent.ID)
	}

	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM payment_intent WHERE id = $1)`,
		intent.ID).Scan(&exists); err != nil {
		return store.PaymentIntent{}, fmt.Errorf("no se pudo buscar el pago %s: %w", intent.ID, err)
	}
	if exists {
		return store.PaymentIntent{}, store.ErrStale
	}

	return store.PaymentIntent{}, store.ErrNotFound
}

func (s *Store) PendingPaymentIntents(ctx context.Context, createdBefore time.Time, limit int) ([]store.PaymentIntent, error) {
	query := `SELECT ` + intentColumns + ` FROM payment_intent
		WHERE status = 'pending' AND created_at <= $1
		ORDER BY created_at, id`
	args := []any{createdBefore}

	if limit > 0 {
		query += ` LIMIT $2`
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("no se pudieron leer los pagos pendientes: %w", err)
	}
	defer rows.Close()

	intents := make([]store.PaymentIntent, 0)
	for rows.Next() {
		intent, err := scanIntent(rows)
		if err != nil {
			return nil, fmt.Errorf("no se pudo leer un pago pendiente: %w", err)
		}

		intents = append(intents, intent)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("no se pudieron leer los pagos pendientes: %w", err)
	}

	return intents, nil
}

func scanIntent(row rowScanner) (store.PaymentIntent, error) {
	var (
		intent     store.PaymentIntent
		providerID sql.NullString
		paid       sql.NullInt64
		status     string
		qr         sql.NullString
		expiresAt  sql.NullTime
		approvedAt sql.NullTime
	)

	err := row.Scan(&intent.ID, &intent.OrderID, &intent.Provider, &providerID, &intent.AmountCents, &paid,
		&status, &qr, &intent.CreatedAt, &expiresAt, &approvedAt, &intent.Version)
	if err != nil {
		return store.PaymentIntent{}, err
	}

	intent.ProviderID = providerID.String
	intent.PaidAmountCents = paid.Int64
	intent.Status = store.PaymentStatus(status)
	intent.QRData = qr.String
	intent.ExpiresAt = expiresAt.Time
	intent.ApprovedAt = approvedAt.Time

	return intent, nil
}

func nullableAmount(value int64) sql.NullInt64 {
	return sql.NullInt64{Int64: value, Valid: value != 0}
}
