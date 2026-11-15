package migrate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"compras-remotas/migrations"
)

func TestVersionsSortsTheFilesAndDropsTheExtension(t *testing.T) {
	files := fstest.MapFS{
		"0010_last.sql":   {Data: []byte("SELECT 1;")},
		"0002_second.sql": {Data: []byte("SELECT 1;")},
		"0001_first.sql":  {Data: []byte("SELECT 1;")},
		"notas.txt":       {Data: []byte("esto no es una migración")},
	}

	got, err := versions(files)
	if err != nil {
		t.Fatalf("no se pudieron listar las versiones: %v", err)
	}

	want := []string{"0001_first", "0002_second", "0010_last"}
	if !slices.Equal(got, want) {
		t.Fatalf("las versiones son %v, se esperaban %v", got, want)
	}
}

func TestMissingKeepsOnlyTheVersionsThatAreNotApplied(t *testing.T) {
	all := []string{"0001_first", "0002_second", "0003_third"}
	applied := map[string]string{"0001_first": "hash1", "0003_third": "hash3"}

	got := missing(all, applied)

	if want := []string{"0002_second"}; !slices.Equal(got, want) {
		t.Fatalf("las pendientes son %v, se esperaban %v", got, want)
	}

	if got := missing(all, map[string]string{"0001_first": "hash1", "0002_second": "hash2", "0003_third": "hash3"}); len(got) != 0 {
		t.Fatalf("con todo aplicado no hay pendientes, se obtuvieron %v", got)
	}
}

func TestApplyRunsEveryMigrationInOrder(t *testing.T) {
	db := openSchema(t, testSchema(t))

	applied, err := Apply(context.Background(), db, migrations.Files)
	if err != nil {
		t.Fatalf("la primera corrida devolvió un error: %v", err)
	}

	want := []string{
		"0001_init",
		"0002_order_failed_status",
		"0003_line_reconcile_until",
		"0004_store_postgres",
		"0005_order_version",
		"0006_notification_sent",
	}

	if !slices.Equal(applied, want) {
		t.Fatalf("se aplicaron %v, se esperaban %v", applied, want)
	}

	if got := countVersions(t, db); got != len(want) {
		t.Fatalf("schema_migrations tiene %d filas, se esperaban %d", got, len(want))
	}
}

func TestApplyOnAnUpToDateSchemaDoesNothing(t *testing.T) {
	db := openSchema(t, testSchema(t))

	if _, err := Apply(context.Background(), db, migrations.Files); err != nil {
		t.Fatalf("la primera corrida devolvió un error: %v", err)
	}

	applied, err := Apply(context.Background(), db, migrations.Files)
	if err != nil {
		t.Fatalf("la segunda corrida devolvió un error: %v", err)
	}

	if len(applied) != 0 {
		t.Fatalf("la segunda corrida aplicó %v, se esperaba nada", applied)
	}
}

func TestApplyRunsOnlyTheFileThatWasAddedLater(t *testing.T) {
	db := openSchema(t, testSchema(t))

	first := fstest.MapFS{
		"0001_alpha.sql": {Data: []byte("CREATE TABLE alpha (id text);")},
		"0002_beta.sql":  {Data: []byte("CREATE TABLE beta (id text);")},
	}

	if _, err := Apply(context.Background(), db, first); err != nil {
		t.Fatalf("la primera corrida devolvió un error: %v", err)
	}

	second := fstest.MapFS{
		"0001_alpha.sql": {Data: []byte("CREATE TABLE alpha (id text);")},
		"0002_beta.sql":  {Data: []byte("CREATE TABLE beta (id text);")},
		"0003_gamma.sql": {Data: []byte("CREATE TABLE gamma (id text);")},
	}

	applied, err := Apply(context.Background(), db, second)
	if err != nil {
		t.Fatalf("la segunda corrida devolvió un error: %v", err)
	}

	if want := []string{"0003_gamma"}; !slices.Equal(applied, want) {
		t.Fatalf("se aplicaron %v, se esperaba %v", applied, want)
	}

	assertTableExists(t, db, "gamma")
}

func TestApplyRollsBackABrokenFile(t *testing.T) {
	db := openSchema(t, testSchema(t))

	files := fstest.MapFS{
		"0001_ok.sql":     {Data: []byte("CREATE TABLE ok_table (id text);")},
		"0002_broken.sql": {Data: []byte("CREATE TABLE broken_table (id text);\nSELECT esto no es sql;")},
	}

	applied, err := Apply(context.Background(), db, files)
	if err == nil {
		t.Fatal("un archivo roto tiene que devolver un error")
	}

	if !strings.Contains(err.Error(), "0002_broken.sql") {
		t.Fatalf("el error no nombra el archivo roto: %v", err)
	}

	if want := []string{"0001_ok"}; !slices.Equal(applied, want) {
		t.Fatalf("se aplicaron %v, se esperaba %v", applied, want)
	}

	assertTableMissing(t, db, "broken_table")
	assertTableExists(t, db, "ok_table")

	for _, version := range versionsOf(t, db) {
		if version == "0002_broken" {
			t.Fatal("el archivo roto quedó registrado en schema_migrations")
		}
	}
}

func TestApplyRunsEachVersionOnceWithTwoCallersAtTheSameTime(t *testing.T) {
	db := openSchema(t, testSchema(t))

	files := fstest.MapFS{
		"0001_alpha.sql": {Data: []byte("CREATE TABLE alpha (id text);")},
		"0002_beta.sql":  {Data: []byte("CREATE TABLE beta (id text);")},
	}

	var group sync.WaitGroup

	results := make([][]string, 2)
	errs := make([]error, 2)

	for index := range results {
		group.Add(1)

		go func(index int) {
			defer group.Done()

			results[index], errs[index] = Apply(context.Background(), db, files)
		}(index)
	}

	group.Wait()

	for index, err := range errs {
		if err != nil {
			t.Fatalf("el runner %d devolvió un error: %v", index, err)
		}
	}

	if got := len(results[0]) + len(results[1]); got != 2 {
		t.Fatalf("los dos runners aplicaron %d versiones en total: %v y %v", got, results[0], results[1])
	}

	if got := countVersions(t, db); got != 2 {
		t.Fatalf("schema_migrations tiene %d filas, se esperaban 2", got)
	}
}

func TestApplyStoresChecksumInSchemaMigrations(t *testing.T) {
	db := openSchema(t, testSchema(t))

	files := fstest.MapFS{
		"0001_alpha.sql": {Data: []byte("CREATE TABLE alpha (id text);")},
		"0002_beta.sql":  {Data: []byte("CREATE TABLE beta (id text);")},
	}

	applied, err := Apply(context.Background(), db, files)
	if err != nil {
		t.Fatalf("Apply devolvió un error inesperado: %v", err)
	}

	want := []string{"0001_alpha", "0002_beta"}
	if !slices.Equal(applied, want) {
		t.Fatalf("se aplicaron %v, se esperaban %v", applied, want)
	}

	for _, version := range want {
		expected := sha256Hex(files[version+".sql"].Data)
		stored := checksumOf(t, db, version)

		if stored != expected {
			t.Fatalf("para %s el checksum es %s, se esperaba %s", version, stored, expected)
		}
	}
}

func TestApplyNormalizesCRLFBeforeComputingChecksum(t *testing.T) {
	db := openSchema(t, testSchema(t))

	crlfData := []byte("CREATE TABLE crlf_sample (\r\n\tid text\r\n);\r\n")
	lfData := []byte("CREATE TABLE crlf_sample (\n\tid text\n);\n")

	files := fstest.MapFS{
		"0001_crlf.sql": {Data: crlfData},
	}

	if _, err := Apply(context.Background(), db, files); err != nil {
		t.Fatalf("la corrida devolvió un error: %v", err)
	}

	expected := fmt.Sprintf("%x", sha256.Sum256(lfData))
	stored := checksumOf(t, db, "0001_crlf")

	if stored != expected {
		t.Fatalf("el checksum almacenado es %s, se esperaba %s", stored, expected)
	}
}

func TestApplyRejectsModifiedMigrationAndDoesNotApplyPending(t *testing.T) {
	db := openSchema(t, testSchema(t))

	initial := fstest.MapFS{
		"0001_init.sql": {Data: []byte("CREATE TABLE init_table (id text);")},
	}

	if _, err := Apply(context.Background(), db, initial); err != nil {
		t.Fatalf("la primera corrida devolvió un error: %v", err)
	}

	initialChecksum := checksumOf(t, db, "0001_init")

	modified := fstest.MapFS{
		"0001_init.sql": {Data: []byte("CREATE TABLE init_table (id text, extra int);")},
		"0002_next.sql": {Data: []byte("CREATE TABLE next_table (id text);")},
	}

	applied, err := Apply(context.Background(), db, modified)
	if err == nil {
		t.Fatal("Apply con archivo modificado debía fallar")
	}

	if !strings.Contains(err.Error(), "0001_init.sql") {
		t.Fatalf("el error %q no nombra el archivo 0001_init.sql", err.Error())
	}

	if len(applied) != 0 {
		t.Fatalf("se devolvieron versiones aplicadas %v, se esperaba lista vacía", applied)
	}

	assertTableMissing(t, db, "next_table")

	var pendingCount int
	if err := db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE version = '0002_next'`).Scan(&pendingCount); err != nil {
		t.Fatalf("no se pudo consultar schema_migrations: %v", err)
	}
	if pendingCount != 0 {
		t.Fatalf("0002_next no debía registrarse en schema_migrations")
	}

	if got := checksumOf(t, db, "0001_init"); got != initialChecksum {
		t.Fatalf("el checksum de 0001_init cambió a %s, debía permanecer %s", got, initialChecksum)
	}
}

func TestApplyWithUnchangedFilesReturnsNoErrorAndAppliesNothing(t *testing.T) {
	db := openSchema(t, testSchema(t))

	files := fstest.MapFS{
		"0001_first.sql":  {Data: []byte("CREATE TABLE first_table (id text);")},
		"0002_second.sql": {Data: []byte("CREATE TABLE second_table (id text);")},
	}

	if _, err := Apply(context.Background(), db, files); err != nil {
		t.Fatalf("la primera corrida devolvió un error: %v", err)
	}

	applied, err := Apply(context.Background(), db, files)
	if err != nil {
		t.Fatalf("la segunda corrida devolvió un error: %v", err)
	}

	if len(applied) != 0 {
		t.Fatalf("la segunda corrida aplicó %v, se esperaba lista vacía", applied)
	}
}

func TestApplyUpgradesOldSchemaMigrationsAndBackfillsChecksum(t *testing.T) {
	db := openSchema(t, testSchema(t))

	legacySchemaTable := `CREATE TABLE schema_migrations (
		version text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`
	if _, err := db.Exec(legacySchemaTable); err != nil {
		t.Fatalf("no se pudo crear la tabla previa: %v", err)
	}

	if _, err := db.Exec(`CREATE TABLE old_table (id text)`); err != nil {
		t.Fatalf("no se pudo crear la tabla ya aplicada: %v", err)
	}

	if _, err := db.Exec(`INSERT INTO schema_migrations (version) VALUES ('0001_old')`); err != nil {
		t.Fatalf("no se pudo insertar la versión vieja: %v", err)
	}

	files := fstest.MapFS{
		"0001_old.sql": {Data: []byte("CREATE TABLE old_table (id text);")},
		"0002_new.sql": {Data: []byte("CREATE TABLE new_table (id text);")},
	}

	applied, err := Apply(context.Background(), db, files)
	if err != nil {
		t.Fatalf("Apply con tabla previa devolvió un error: %v", err)
	}

	if want := []string{"0002_new"}; !slices.Equal(applied, want) {
		t.Fatalf("se aplicaron %v, se esperaba %v", applied, want)
	}

	expectedOld := sha256Hex(files["0001_old.sql"].Data)
	if got := checksumOf(t, db, "0001_old"); got != expectedOld {
		t.Fatalf("el checksum de 0001_old es %s, se esperaba backfill a %s", got, expectedOld)
	}

	expectedNew := sha256Hex(files["0002_new.sql"].Data)
	if got := checksumOf(t, db, "0002_new"); got != expectedNew {
		t.Fatalf("el checksum de 0002_new es %s, se esperaba %s", got, expectedNew)
	}

	assertTableExists(t, db, "new_table")

	tamperedFiles := fstest.MapFS{
		"0001_old.sql": {Data: []byte("CREATE TABLE old_table (id text, tampered int);")},
		"0002_new.sql": {Data: []byte("CREATE TABLE new_table (id text);")},
	}

	appliedTampered, errTampered := Apply(context.Background(), db, tamperedFiles)
	if errTampered == nil {
		t.Fatal("segunda corrida con archivo histórico modificado debía fallar")
	}

	if !strings.Contains(errTampered.Error(), "0001_old.sql") {
		t.Fatalf("el error %q no nombra 0001_old.sql", errTampered.Error())
	}

	if len(appliedTampered) != 0 {
		t.Fatalf("la corrida con error devolvió aplicados %v, se esperaba lista vacía", appliedTampered)
	}
}

func testSchema(t *testing.T) string {
	t.Helper()

	target := testDSN(t)

	name := "mig_test_" + strconv.FormatInt(time.Now().UnixNano(), 36)

	admin, err := sql.Open("pgx", target)
	if err != nil {
		t.Fatalf("no se pudo abrir la base de prueba: %v", err)
	}

	if _, err := admin.Exec(`CREATE SCHEMA ` + name); err != nil {
		admin.Close()
		t.Fatalf("no se pudo crear el esquema de prueba: %v", err)
	}

	if err := admin.Close(); err != nil {
		t.Fatalf("no se pudo cerrar la base de prueba: %v", err)
	}

	t.Cleanup(func() {
		cleaner, err := sql.Open("pgx", target)
		if err != nil {
			t.Errorf("no se pudo abrir la base para limpiar: %v", err)

			return
		}
		defer cleaner.Close()

		if _, err := cleaner.Exec(`DROP SCHEMA IF EXISTS ` + name + ` CASCADE`); err != nil {
			t.Errorf("no se pudo borrar el esquema de prueba: %v", err)
		}
	})

	return name
}

func openSchema(t *testing.T, schema string) *sql.DB {
	t.Helper()

	db, err := sql.Open("pgx", withSearchPath(testDSN(t), schema))
	if err != nil {
		t.Fatalf("no se pudo abrir la base de prueba: %v", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		t.Fatalf("no se pudo conectar con la base de prueba: %v", err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("no se pudo cerrar la base de prueba: %v", err)
		}
	})

	return db
}

func testDSN(t *testing.T) string {
	t.Helper()

	value := os.Getenv("TEST_DATABASE_URL")
	if value == "" {
		t.Skip("falta la variable TEST_DATABASE_URL: hay que apuntarla a una base PostgreSQL vacía para poder correr los tests del runner")
	}

	return value
}

func withSearchPath(target, schema string) string {
	if strings.Contains(target, "?") {
		return target + "&search_path=" + schema
	}

	return target + "?search_path=" + schema
}

func countVersions(t *testing.T, db *sql.DB) int {
	t.Helper()

	var total int

	if err := db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&total); err != nil {
		t.Fatalf("no se pudo contar las versiones aplicadas: %v", err)
	}

	return total
}

func versionsOf(t *testing.T, db *sql.DB) []string {
	t.Helper()

	rows, err := db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("no se pudieron leer las versiones aplicadas: %v", err)
	}
	defer rows.Close()

	var found []string

	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			t.Fatalf("no se pudo leer una versión aplicada: %v", err)
		}

		found = append(found, version)
	}

	if err := rows.Err(); err != nil {
		t.Fatalf("no se pudieron leer las versiones aplicadas: %v", err)
	}

	return found
}

func assertTableExists(t *testing.T, db *sql.DB, table string) {
	t.Helper()

	var name string

	err := db.QueryRow(`SELECT to_regclass($1)::text`, table).Scan(&name)
	if err != nil {
		t.Fatalf("no se pudo buscar la tabla %s: %v", table, err)
	}

	if name == "" {
		t.Fatalf("la tabla %s no existe", table)
	}
}

func assertTableMissing(t *testing.T, db *sql.DB, table string) {
	t.Helper()

	var name sql.NullString

	if err := db.QueryRow(`SELECT to_regclass($1)::text`, table).Scan(&name); err != nil {
		t.Fatalf("no se pudo buscar la tabla %s: %v", table, err)
	}

	if name.Valid {
		t.Fatalf("la tabla %s quedó creada y tenía que revertirse", table)
	}
}

func checksumOf(t *testing.T, db *sql.DB, version string) string {
	t.Helper()

	var checksum sql.NullString

	if err := db.QueryRow(`SELECT checksum FROM schema_migrations WHERE version = $1`, version).Scan(&checksum); err != nil {
		t.Fatalf("no se pudo leer el checksum de %s: %v", version, err)
	}

	if !checksum.Valid {
		t.Fatalf("el checksum de %s es nulo", version)
	}

	return checksum.String
}

func sha256Hex(data []byte) string {
	normalized := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	sum := sha256.Sum256(normalized)

	return fmt.Sprintf("%x", sum[:])
}
