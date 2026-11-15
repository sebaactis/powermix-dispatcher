package migrate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"
)

const (
	schemaTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
	version    text PRIMARY KEY,
	applied_at timestamptz NOT NULL DEFAULT now(),
	checksum   text
)`

	ensureChecksumColumn = `ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS checksum text`

	advisoryLockID int64 = 5720051926

	lockQuery             = `SELECT pg_advisory_lock($1)`
	unlockQuery           = `SELECT pg_advisory_unlock($1)`
	appliedQuery          = `SELECT version, checksum FROM schema_migrations`
	recordQuery           = `INSERT INTO schema_migrations (version, checksum) VALUES ($1, $2)`
	backfillChecksumQuery = `UPDATE schema_migrations SET checksum = $2 WHERE version = $1 AND checksum IS NULL`

	unlockTimeout = 10 * time.Second
)

func Apply(ctx context.Context, db *sql.DB, files fs.FS) ([]string, error) {
	all, err := versions(files)
	if err != nil {
		return nil, err
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("no se pudo tomar una conexión para migrar: %w", err)
	}
	defer conn.Close()

	if err := acquireLock(ctx, conn); err != nil {
		return nil, err
	}

	applied, applyErr := applyPending(ctx, conn, files, all)

	if unlockErr := releaseLock(conn); unlockErr != nil && applyErr == nil {
		return applied, unlockErr
	}

	return applied, applyErr
}

func versions(files fs.FS) ([]string, error) {
	names, err := fs.Glob(files, "*.sql")
	if err != nil {
		return nil, fmt.Errorf("no se pudieron listar las migraciones: %w", err)
	}

	sort.Strings(names)

	found := make([]string, 0, len(names))

	for _, name := range names {
		found = append(found, strings.TrimSuffix(name, ".sql"))
	}

	return found, nil
}

func checksum(body []byte) string {
	normalized := bytes.ReplaceAll(body, []byte("\r\n"), []byte("\n"))
	sum := sha256.Sum256(normalized)

	return hex.EncodeToString(sum[:])
}

func missing(all []string, applied map[string]string) []string {
	pending := make([]string, 0, len(all))

	for _, version := range all {
		if _, ok := applied[version]; !ok {
			pending = append(pending, version)
		}
	}

	return pending
}

func acquireLock(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, lockQuery, advisoryLockID); err != nil {
		return fmt.Errorf("no se pudo tomar el bloqueo de migraciones: %w", err)
	}

	return nil
}

func releaseLock(conn *sql.Conn) error {
	ctx, cancel := context.WithTimeout(context.Background(), unlockTimeout)
	defer cancel()

	if _, err := conn.ExecContext(ctx, unlockQuery, advisoryLockID); err != nil {
		return fmt.Errorf("no se pudo soltar el bloqueo de migraciones: %w", err)
	}

	return nil
}

func ensureTable(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, schemaTable); err != nil {
		return fmt.Errorf("no se pudo crear la tabla schema_migrations: %w", err)
	}

	if _, err := conn.ExecContext(ctx, ensureChecksumColumn); err != nil {
		return fmt.Errorf("no se pudo agregar la columna checksum a schema_migrations: %w", err)
	}

	return nil
}

func appliedVersions(ctx context.Context, conn *sql.Conn) (map[string]string, error) {
	rows, err := conn.QueryContext(ctx, appliedQuery)
	if err != nil {
		return nil, fmt.Errorf("no se pudieron leer las migraciones aplicadas: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]string)

	for rows.Next() {
		var version string
		var storedChecksum sql.NullString

		if err := rows.Scan(&version, &storedChecksum); err != nil {
			return nil, fmt.Errorf("no se pudo leer una migración aplicada: %w", err)
		}

		applied[version] = storedChecksum.String
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("no se pudieron leer las migraciones aplicadas: %w", err)
	}

	return applied, nil
}

func verifyAndBackfillApplied(ctx context.Context, conn *sql.Conn, files fs.FS, all []string, applied map[string]string) error {
	for _, version := range all {
		stored, ok := applied[version]
		if !ok {
			continue
		}

		name := version + ".sql"

		body, err := fs.ReadFile(files, name)
		if err != nil {
			return fmt.Errorf("no se pudo leer la migración %s: %w", name, err)
		}

		current := checksum(body)

		if stored == "" {
			if _, err := conn.ExecContext(ctx, backfillChecksumQuery, version, current); err != nil {
				return fmt.Errorf("no se pudo actualizar la huella de la migración %s: %w", name, err)
			}

			applied[version] = current

			continue
		}

		if stored != current {
			return fmt.Errorf("la migración %s cambió después de aplicarse: huella registrada %s, huella actual %s", name, stored, current)
		}
	}

	return nil
}

func applyPending(ctx context.Context, conn *sql.Conn, files fs.FS, all []string) ([]string, error) {
	if err := ensureTable(ctx, conn); err != nil {
		return nil, err
	}

	applied, err := appliedVersions(ctx, conn)
	if err != nil {
		return nil, err
	}

	if err := verifyAndBackfillApplied(ctx, conn, files, all, applied); err != nil {
		return nil, err
	}

	done := make([]string, 0, len(all))

	for _, version := range missing(all, applied) {
		if err := applyOne(ctx, conn, files, version); err != nil {
			return done, err
		}

		done = append(done, version)
	}

	return done, nil
}

func applyOne(ctx context.Context, conn *sql.Conn, files fs.FS, version string) error {
	name := version + ".sql"

	body, err := fs.ReadFile(files, name)
	if err != nil {
		return fmt.Errorf("no se pudo leer la migración %s: %w", name, err)
	}

	current := checksum(body)

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("no se pudo abrir la transacción de la migración %s: %w", name, err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		return fmt.Errorf("no se pudo aplicar la migración %s: %w", name, err)
	}

	if _, err := tx.ExecContext(ctx, recordQuery, version, current); err != nil {
		return fmt.Errorf("no se pudo registrar la migración %s: %w", name, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("no se pudo confirmar la migración %s: %w", name, err)
	}

	return nil
}
