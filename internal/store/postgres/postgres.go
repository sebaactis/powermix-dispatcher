package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"

	"compras-remotas/internal/linestate"
	"compras-remotas/internal/machine"
	"compras-remotas/internal/store"
)

const (
	orderColumns = `id, kiosk_id, combo_id, client_key, amount_cents, currency, status, created_at, paid_at, closed_at, version`

	lineColumns = `id, order_id, position, machine_type, sku, amount_cents, state, external_ref, attempts,
		deadline_at, reconcile_until, last_error, version, machine_kiosk_id, machine_name,
		machine_device_id, machine_device_no, machine_device_type_id, machine_mid`

	lineColumnCount = 19
)

var stalledLineColumns = tableColumns(lineColumns, "order_line")

type Store struct {
	db *sql.DB
}

var _ store.Store = (*Store)(nil)

type rowScanner interface {
	Scan(dest ...any) error
}

func Open(dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("no se pudo abrir la conexión con postgres: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()

		return nil, fmt.Errorf("no se pudo conectar con postgres: %w", err)
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) CreateOrder(ctx context.Context, order store.Order) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("no se pudo abrir la transacción de la orden %s: %w", order.ID, err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `INSERT INTO orders (`+orderColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE($8, now()), $9, $10, $11)`,
		order.ID, order.KioskID, order.ComboID, nullableText(order.ClientKey), order.AmountCents,
		order.Currency, string(order.Status), nullableTime(order.CreatedAt), nullableTime(order.PaidAt),
		nullableTime(order.ClosedAt), order.Version)
	if err != nil {
		return mapWriteError(err, "la orden", order.ID)
	}

	insertLine := `INSERT INTO order_line (` + lineColumns + `) VALUES (` + placeholders(lineColumnCount) + `)`

	for _, line := range order.Lines {
		if _, err := tx.ExecContext(ctx, insertLine, lineInsertArgs(order.ID, line)...); err != nil {
			return mapWriteError(err, "la línea", line.ID)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("no se pudo confirmar la orden %s: %w", order.ID, err)
	}

	return nil
}

func (s *Store) LoadOrder(ctx context.Context, orderID string) (store.Order, error) {
	return s.orderBy(ctx, "id", orderID)
}

func (s *Store) OrderByClientKey(ctx context.Context, key string) (store.Order, error) {
	return s.orderBy(ctx, "client_key", key)
}

func (s *Store) SaveOrder(ctx context.Context, order store.Order) (store.Order, error) {
	var version int64

	err := s.db.QueryRowContext(ctx, `UPDATE orders SET
		kiosk_id = $2,
		combo_id = $3,
		client_key = $4,
		amount_cents = $5,
		currency = $6,
		status = $7,
		created_at = COALESCE($8, now()),
		paid_at = $9,
		closed_at = $10,
		version = version + 1
		WHERE id = $1 AND version = $11
		RETURNING version`,
		order.ID, order.KioskID, order.ComboID, nullableText(order.ClientKey), order.AmountCents,
		order.Currency, string(order.Status), nullableTime(order.CreatedAt), nullableTime(order.PaidAt),
		nullableTime(order.ClosedAt), order.Version).Scan(&version)
	if err == nil {
		order.Version = version

		return order, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return store.Order{}, mapWriteError(err, "la orden", order.ID)
	}

	exists, err := s.orderExists(ctx, order.ID)
	if err != nil {
		return store.Order{}, err
	}
	if exists {
		return store.Order{}, store.ErrStale
	}

	return store.Order{}, store.ErrNotFound
}

func (s *Store) SaveLine(ctx context.Context, line store.Line) (store.Line, error) {
	row := s.db.QueryRowContext(ctx, `UPDATE order_line SET
		position = $3,
		machine_type = $4,
		sku = $5,
		amount_cents = $6,
		state = $7,
		external_ref = $8,
		attempts = $9,
		deadline_at = $10,
		reconcile_until = $11,
		last_error = $12,
		version = version + 1,
		updated_at = now(),
		machine_kiosk_id = $13,
		machine_name = $14,
		machine_device_id = $15,
		machine_device_no = $16,
		machine_device_type_id = $17,
		machine_mid = $18
		WHERE id = $1 AND version = $2
		RETURNING `+lineColumns,
		line.ID, line.Version, line.Position, string(line.Machine.Kind), line.SKU, line.AmountCents, string(line.State),
		nullableText(line.Ref), line.Attempts, nullableTime(line.DeadlineAt), nullableTime(line.ReconcileUntil),
		nullableText(line.LastError), nullableText(line.Machine.KioskID), nullableText(line.Machine.Name),
		nullableText(line.Machine.DeviceID), nullableText(line.Machine.DeviceNo),
		nullableText(line.Machine.DeviceTypeID), nullableText(line.Machine.MID))

	saved, err := scanLine(row)
	if err == nil {
		return saved, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return store.Line{}, mapWriteError(err, "la línea", line.ID)
	}

	exists, err := s.lineExists(ctx, line.ID)
	if err != nil {
		return store.Line{}, err
	}
	if exists {
		return store.Line{}, store.ErrStale
	}

	return store.Line{}, store.ErrNotFound
}

func (s *Store) AppendAttempt(ctx context.Context, attempt store.Attempt) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO dispatch_attempt
		(id, line_id, attempt_no, idempotency_key, request_payload, outcome, error_detail, started_at, finished_at)
		VALUES ($1, $2, $3, $4, '{}'::jsonb, $5, $6, COALESCE($7, now()), $8)`,
		attempt.ID, attempt.LineID, attempt.Number, attempt.IdempotencyKey, string(attempt.Outcome),
		nullableText(attempt.Detail), nullableTime(attempt.StartedAt), nullableTime(attempt.FinishedAt))
	if err != nil {
		return mapWriteError(err, "el intento", attempt.ID)
	}

	return nil
}

func (s *Store) SaveAttempt(ctx context.Context, attempt store.Attempt) error {
	result, err := s.db.ExecContext(ctx, `UPDATE dispatch_attempt SET
		outcome = $2,
		error_detail = $3,
		finished_at = $4
		WHERE idempotency_key = $1`,
		attempt.IdempotencyKey, string(attempt.Outcome), nullableText(attempt.Detail),
		nullableTime(attempt.FinishedAt))
	if err != nil {
		return mapWriteError(err, "el intento", attempt.IdempotencyKey)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("no se pudo saber si se cerró el intento %s: %w", attempt.IdempotencyKey, err)
	}
	if affected == 0 {
		return store.ErrNotFound
	}

	return nil
}

func (s *Store) RegisterWebhook(ctx context.Context, wh store.Webhook) (bool, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO webhook_event (id, source, dedupe_key, payload, received_at)
		VALUES ($1, $2, $3, '{}'::jsonb, COALESCE($4, now()))
		ON CONFLICT (source, dedupe_key) DO NOTHING`,
		wh.ID, wh.Source, wh.DedupeKey, nullableTime(wh.ReceivedAt))
	if err != nil {
		return false, mapWriteError(err, "el webhook", wh.DedupeKey)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("no se pudo saber si se registró el webhook %s: %w", wh.DedupeKey, err)
	}

	return affected == 0, nil
}

func (s *Store) NotificationSent(ctx context.Context, orderID, kind string) (bool, error) {
	var sent, exists bool

	err := s.db.QueryRowContext(ctx, `SELECT
		EXISTS (SELECT 1 FROM notification_sent WHERE order_id = $1 AND kind = $2),
		EXISTS (SELECT 1 FROM orders WHERE id = $1)`, orderID, kind).Scan(&sent, &exists)
	if err != nil {
		return false, fmt.Errorf("no se pudo buscar el aviso %s de la orden %s: %w", kind, orderID, err)
	}

	if !exists {
		return false, store.ErrNotFound
	}

	return sent, nil
}

func (s *Store) RecordNotification(ctx context.Context, orderID, kind string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO notification_sent (order_id, kind, sent_at)
		VALUES ($1, $2, COALESCE($3, now()))
		ON CONFLICT (order_id, kind) DO NOTHING`,
		orderID, kind, nullableTime(at))
	if err != nil {
		if isForeignKeyViolation(err) {
			return store.ErrNotFound
		}

		return fmt.Errorf("no se pudo registrar el aviso %s de la orden %s: %w", kind, orderID, err)
	}

	return nil
}

func (s *Store) DueLines(ctx context.Context, now time.Time, limit int) ([]store.Line, error) {
	query := `SELECT ` + lineColumns + ` FROM order_line
		WHERE deadline_at IS NOT NULL AND deadline_at <= $1
			AND state IN ('pending', 'sent', 'brewing', 'in_doubt')
		ORDER BY deadline_at, id`
	args := []any{now}

	if limit > 0 {
		query += ` LIMIT $2`
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("no se pudieron leer las líneas vencidas: %w", err)
	}
	defer rows.Close()

	lines := make([]store.Line, 0)
	for rows.Next() {
		line, err := scanLine(rows)
		if err != nil {
			return nil, fmt.Errorf("no se pudo leer una línea vencida: %w", err)
		}

		lines = append(lines, line)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("no se pudieron leer las líneas vencidas: %w", err)
	}

	return lines, nil
}

func (s *Store) StalledLines(ctx context.Context, paidBefore time.Time, limit int) ([]store.Line, error) {
	query := `SELECT ` + stalledLineColumns + ` FROM order_line
		JOIN orders ON orders.id = order_line.order_id
		WHERE order_line.state = 'pending' AND order_line.deadline_at IS NULL
			AND orders.status IN ('paid', 'dispatching')
			AND orders.paid_at IS NOT NULL AND orders.paid_at <= $1
		ORDER BY orders.paid_at, order_line.id`
	args := []any{paidBefore}

	if limit > 0 {
		query += ` LIMIT $2`
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("no se pudieron leer las líneas trabadas: %w", err)
	}
	defer rows.Close()

	lines := make([]store.Line, 0)
	for rows.Next() {
		line, err := scanLine(rows)
		if err != nil {
			return nil, fmt.Errorf("no se pudo leer una línea trabada: %w", err)
		}

		lines = append(lines, line)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("no se pudieron leer las líneas trabadas: %w", err)
	}

	return lines, nil
}

func tableColumns(columns, table string) string {
	parts := strings.Split(columns, ",")
	for i, part := range parts {
		parts[i] = table + "." + strings.TrimSpace(part)
	}

	return strings.Join(parts, ", ")
}

func (s *Store) orderBy(ctx context.Context, column string, value any) (store.Order, error) {
	order, err := scanOrder(s.db.QueryRowContext(ctx, `SELECT `+orderColumns+` FROM orders WHERE `+column+` = $1`, value))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.Order{}, store.ErrNotFound
		}

		return store.Order{}, fmt.Errorf("no se pudo leer la orden: %w", err)
	}

	lines, err := s.loadLines(ctx, order.ID)
	if err != nil {
		return store.Order{}, err
	}
	order.Lines = lines

	return order, nil
}

func (s *Store) loadLines(ctx context.Context, orderID string) ([]store.Line, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+lineColumns+` FROM order_line WHERE order_id = $1 ORDER BY position`, orderID)
	if err != nil {
		return nil, fmt.Errorf("no se pudieron leer las líneas de la orden %s: %w", orderID, err)
	}
	defer rows.Close()

	lines := make([]store.Line, 0)
	for rows.Next() {
		line, err := scanLine(rows)
		if err != nil {
			return nil, fmt.Errorf("no se pudo leer una línea de la orden %s: %w", orderID, err)
		}

		lines = append(lines, line)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("no se pudieron leer las líneas de la orden %s: %w", orderID, err)
	}

	return lines, nil
}

func (s *Store) orderExists(ctx context.Context, orderID string) (bool, error) {
	var exists bool

	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM orders WHERE id = $1)`, orderID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("no se pudo buscar la orden %s: %w", orderID, err)
	}

	return exists, nil
}

func (s *Store) lineExists(ctx context.Context, lineID string) (bool, error) {
	var exists bool

	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM order_line WHERE id = $1)`, lineID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("no se pudo buscar la línea %s: %w", lineID, err)
	}

	return exists, nil
}

func scanOrder(row rowScanner) (store.Order, error) {
	var (
		order     store.Order
		clientKey sql.NullString
		status    string
		paidAt    sql.NullTime
		closedAt  sql.NullTime
	)

	err := row.Scan(&order.ID, &order.KioskID, &order.ComboID, &clientKey, &order.AmountCents,
		&order.Currency, &status, &order.CreatedAt, &paidAt, &closedAt, &order.Version)
	if err != nil {
		return store.Order{}, err
	}

	order.ClientKey = clientKey.String
	order.Status = store.OrderStatus(status)
	order.PaidAt = paidAt.Time
	order.ClosedAt = closedAt.Time

	return order, nil
}

func scanLine(row rowScanner) (store.Line, error) {
	var (
		line           store.Line
		machineType    string
		state          string
		ref            sql.NullString
		deadlineAt     sql.NullTime
		reconcileUntil sql.NullTime
		lastError      sql.NullString
		kioskID        sql.NullString
		machineName    sql.NullString
		deviceID       sql.NullString
		deviceNo       sql.NullString
		deviceTypeID   sql.NullString
		mid            sql.NullString
	)

	err := row.Scan(&line.ID, &line.OrderID, &line.Position, &machineType, &line.SKU, &line.AmountCents,
		&state, &ref, &line.Attempts, &deadlineAt, &reconcileUntil, &lastError, &line.Version,
		&kioskID, &machineName, &deviceID, &deviceNo, &deviceTypeID, &mid)
	if err != nil {
		return store.Line{}, err
	}

	line.State = linestate.State(state)
	line.Ref = ref.String
	line.DeadlineAt = deadlineAt.Time
	line.ReconcileUntil = reconcileUntil.Time
	line.LastError = lastError.String
	line.Machine = machine.Ref{
		KioskID:      kioskID.String,
		Kind:         machine.Kind(machineType),
		Name:         machineName.String,
		DeviceID:     deviceID.String,
		DeviceNo:     deviceNo.String,
		DeviceTypeID: deviceTypeID.String,
		MID:          mid.String,
	}

	return line, nil
}

func lineInsertArgs(orderID string, line store.Line) []any {
	return []any{
		line.ID, orderID, line.Position, string(line.Machine.Kind), line.SKU, line.AmountCents, string(line.State),
		nullableText(line.Ref), line.Attempts, nullableTime(line.DeadlineAt), nullableTime(line.ReconcileUntil),
		nullableText(line.LastError), 0,
		nullableText(line.Machine.KioskID), nullableText(line.Machine.Name), nullableText(line.Machine.DeviceID),
		nullableText(line.Machine.DeviceNo), nullableText(line.Machine.DeviceTypeID), nullableText(line.Machine.MID),
	}
}

func nullableText(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

func nullableTime(value time.Time) sql.NullTime {
	return sql.NullTime{Time: value, Valid: !value.IsZero()}
}

func placeholders(count int) string {
	placeholders := make([]byte, 0, count*4)

	for i := 1; i <= count; i++ {
		if i > 1 {
			placeholders = append(placeholders, ',', ' ')
		}

		placeholders = append(placeholders, fmt.Sprintf("$%d", i)...)
	}

	return string(placeholders)
}

func mapWriteError(err error, kind, id string) error {
	if isUniqueViolation(err) {
		return store.ErrAlreadyExists
	}
	if isCheckViolation(err) {
		return fmt.Errorf("%w: %s %s: %w", store.ErrInvalid, kind, id, err)
	}

	return fmt.Errorf("no se pudo guardar %s %s: %w", kind, id, err)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError

	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}

	return false
}

func isCheckViolation(err error) bool {
	var pgErr *pgconn.PgError

	if errors.As(err, &pgErr) {
		return pgErr.Code == "23514"
	}

	return false
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError

	if errors.As(err, &pgErr) {
		return pgErr.Code == "23503"
	}

	return false
}
