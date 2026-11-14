package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"compras-remotas/internal/linestate"
	"compras-remotas/internal/machine"
	"compras-remotas/internal/migrate"
	"compras-remotas/internal/store"
	"compras-remotas/internal/store/postgres"
	"compras-remotas/migrations"
)

const (
	kioskID = "kiosk-test"
	comboID = "combo-test"
)

var (
	migrateOnce   sync.Once
	migrateFailed error
)

func moment() time.Time {
	return time.Date(2026, time.March, 10, 15, 30, 0, 0, time.UTC)
}

func dsn(t *testing.T) string {
	t.Helper()

	value := os.Getenv("TEST_DATABASE_URL")
	if value == "" {
		t.Skip("falta la variable TEST_DATABASE_URL: hay que apuntarla a una base PostgreSQL vacía para poder correr los tests del store")
	}

	return value
}

func applyMigrations(target string) error {
	db, err := sql.Open("pgx", target)
	if err != nil {
		return fmt.Errorf("no se pudo abrir la base para migrar: %w", err)
	}
	defer db.Close()

	if _, err := migrate.Apply(context.Background(), db, migrations.Files); err != nil {
		return fmt.Errorf("no se pudieron aplicar las migraciones: %w", err)
	}

	return nil
}

func resetDatabase(t *testing.T, target string) {
	t.Helper()

	db, err := sql.Open("pgx", target)
	if err != nil {
		t.Fatalf("no se pudo abrir la base de prueba: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`TRUNCATE notification_sent, webhook_event, dispatch_attempt, order_line, orders, payment_intent,
		catalog_snapshot, combo_item, combo, machine, kiosk RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("no se pudieron vaciar las tablas: %v", err)
	}

	if _, err := db.Exec(`INSERT INTO kiosk (id, name) VALUES ($1, $2)`, kioskID, "Mueble de prueba"); err != nil {
		t.Fatalf("no se pudo sembrar el mueble: %v", err)
	}

	if _, err := db.Exec(`INSERT INTO combo (id, name, price_cents, currency) VALUES ($1, $2, $3, $4)`,
		comboID, "Combo de prueba", 150000, "ARS"); err != nil {
		t.Fatalf("no se pudo sembrar el combo: %v", err)
	}
}

func newStore(t *testing.T) (*postgres.Store, *sql.DB) {
	t.Helper()

	target := dsn(t)

	migrateOnce.Do(func() { migrateFailed = applyMigrations(target) })
	if migrateFailed != nil {
		t.Fatalf("no se pudieron aplicar las migraciones: %v", migrateFailed)
	}

	resetDatabase(t, target)

	st, err := postgres.Open(target)
	if err != nil {
		t.Fatalf("no se pudo abrir el store: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("no se pudo cerrar el store: %v", err)
		}
	})

	admin, err := sql.Open("pgx", target)
	if err != nil {
		t.Fatalf("no se pudo abrir la base de prueba: %v", err)
	}
	t.Cleanup(func() {
		if err := admin.Close(); err != nil {
			t.Errorf("no se pudo cerrar la base de prueba: %v", err)
		}
	})

	return st, admin
}

func sampleOrder(orderID, clientKey string) store.Order {
	return store.Order{
		ID:          orderID,
		KioskID:     kioskID,
		ComboID:     comboID,
		ClientKey:   clientKey,
		AmountCents: 150000,
		Currency:    "ARS",
		Status:      store.OrderPendingPayment,
		CreatedAt:   moment(),
		Lines: []store.Line{
			{
				ID:       orderID + "-batidos",
				OrderID:  orderID,
				Position: 1,
				Machine: machine.Ref{
					KioskID:      kioskID,
					Kind:         "batidos",
					Name:         "Batidos Powermix",
					DeviceID:     "device-1",
					DeviceNo:     "device-no-1",
					DeviceTypeID: "type-1",
				},
				SKU:         "batido-frutilla",
				AmountCents: 70000,
				State:       linestate.Pending,
				Version:     7,
			},
			{
				ID:       orderID + "-snacks",
				OrderID:  orderID,
				Position: 2,
				Machine: machine.Ref{
					KioskID: kioskID,
					Kind:    "snacks",
					Name:    "Snacks Powermix",
					MID:     "mid-1",
				},
				SKU:            "snack-mixto",
				AmountCents:    80000,
				State:          linestate.Pending,
				Ref:            "SO-1",
				DeadlineAt:     moment().Add(2 * time.Minute),
				ReconcileUntil: moment().Add(4 * time.Minute),
				LastError:      "el proveedor no contestó",
			},
		},
	}
}

func orderWithDeadline(orderID string, deadline time.Time) store.Order {
	return store.Order{
		ID:      orderID,
		KioskID: kioskID,
		ComboID: comboID,
		Status:  store.OrderDispatching,
		Lines: []store.Line{
			{
				ID:         orderID + "-line",
				OrderID:    orderID,
				Position:   1,
				Machine:    machine.Ref{Kind: "snacks", MID: "mid-1"},
				SKU:        "snack-mixto",
				State:      linestate.Sent,
				DeadlineAt: deadline,
			},
		},
	}
}

func lineByID(t *testing.T, order store.Order, lineID string) store.Line {
	t.Helper()

	for _, line := range order.Lines {
		if line.ID == lineID {
			return line
		}
	}

	t.Fatalf("la orden %s no tiene la línea %s", order.ID, lineID)

	return store.Line{}
}

func mustCreateOrder(t *testing.T, st *postgres.Store, order store.Order) {
	t.Helper()

	if err := st.CreateOrder(context.Background(), order); err != nil {
		t.Fatalf("no se pudo crear la orden %s: %v", order.ID, err)
	}
}

func mustLoadOrder(t *testing.T, st *postgres.Store, orderID string) store.Order {
	t.Helper()

	order, err := st.LoadOrder(context.Background(), orderID)
	if err != nil {
		t.Fatalf("no se pudo leer la orden %s: %v", orderID, err)
	}

	return order
}

func TestOpenFailsWhenTheDatabaseIsUnreachable(t *testing.T) {
	if _, err := postgres.Open("postgres://nadie:mal@127.0.0.1:1/nada?connect_timeout=1&sslmode=disable"); err == nil {
		t.Fatal("se esperaba un error al abrir una base inalcanzable")
	}
}

func TestCreateOrderStoresTheHeaderAndTheLines(t *testing.T) {
	st, _ := newStore(t)

	mustCreateOrder(t, st, sampleOrder("order-1", ""))
	got := mustLoadOrder(t, st, "order-1")

	if got.ID != "order-1" || got.KioskID != kioskID || got.ComboID != comboID {
		t.Fatalf("la cabecera volvió distinta: %+v", got)
	}
	if got.AmountCents != 150000 || got.Currency != "ARS" || got.Status != store.OrderPendingPayment {
		t.Fatalf("los datos de cobro volvieron distintos: %+v", got)
	}
	if !got.CreatedAt.Equal(moment()) {
		t.Fatalf("created_at: se esperaba %s y volvió %s", moment(), got.CreatedAt)
	}
	if !got.PaidAt.IsZero() || !got.ClosedAt.IsZero() {
		t.Fatalf("las fechas vacías tenían que volver en cero: pagada %s, cerrada %s", got.PaidAt, got.ClosedAt)
	}
	if len(got.Lines) != 2 {
		t.Fatalf("se esperaban 2 líneas y volvieron %d", len(got.Lines))
	}

	batidos := got.Lines[0]
	if batidos.ID != "order-1-batidos" || batidos.Position != 1 || batidos.Machine.Kind != "batidos" {
		t.Fatalf("la primera línea no es la de batidos: %+v", batidos)
	}
	if batidos.Version != 0 {
		t.Fatalf("toda línea tiene que nacer en versión 0 y nació en %d", batidos.Version)
	}
	if batidos.Machine.DeviceID != "device-1" || batidos.Machine.DeviceNo != "device-no-1" ||
		batidos.Machine.DeviceTypeID != "type-1" || batidos.Machine.Name != "Batidos Powermix" ||
		batidos.Machine.KioskID != kioskID || batidos.Machine.Kind != "batidos" {
		t.Fatalf("la identidad de la máquina de batidos volvió incompleta: %+v", batidos.Machine)
	}
	if batidos.State != linestate.Pending || batidos.SKU != "batido-frutilla" || batidos.AmountCents != 70000 {
		t.Fatalf("la primera línea volvió distinta: %+v", batidos)
	}
	if !batidos.DeadlineAt.IsZero() || !batidos.ReconcileUntil.IsZero() || batidos.Ref != "" || batidos.LastError != "" {
		t.Fatalf("la primera línea tenía que volver sin plazos ni referencia: %+v", batidos)
	}

	snacks := got.Lines[1]
	if snacks.ID != "order-1-snacks" || snacks.Position != 2 || snacks.Machine.Kind != "snacks" {
		t.Fatalf("la segunda línea no es la de snacks: %+v", snacks)
	}
	if snacks.Machine.MID != "mid-1" || snacks.Machine.Name != "Snacks Powermix" || snacks.Machine.Kind != "snacks" {
		t.Fatalf("la identidad de la máquina de snacks volvió incompleta: %+v", snacks.Machine)
	}
	if snacks.Machine.DeviceID != "" || snacks.Machine.DeviceNo != "" || snacks.Machine.DeviceTypeID != "" {
		t.Fatalf("la máquina de snacks no tiene identificadores de batidos: %+v", snacks.Machine)
	}
	if snacks.Ref != "SO-1" {
		t.Fatalf("la referencia externa tenía que guardarse en external_ref y volvió %q", snacks.Ref)
	}
	if !snacks.DeadlineAt.Equal(moment().Add(2 * time.Minute)) {
		t.Fatalf("deadline_at: se esperaba %s y volvió %s", moment().Add(2*time.Minute), snacks.DeadlineAt)
	}
	if !snacks.ReconcileUntil.Equal(moment().Add(4 * time.Minute)) {
		t.Fatalf("reconcile_until: se esperaba %s y volvió %s", moment().Add(4*time.Minute), snacks.ReconcileUntil)
	}
	if snacks.LastError != "el proveedor no contestó" {
		t.Fatalf("last_error volvió %q", snacks.LastError)
	}
}

func TestOrderByClientKeyFindsTheOrderAndNothingElse(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()

	mustCreateOrder(t, st, sampleOrder("order-1", "clave-1"))

	found, err := st.OrderByClientKey(ctx, "clave-1")
	if err != nil {
		t.Fatalf("no se pudo buscar por clave del cliente: %v", err)
	}
	if found.ID != "order-1" || len(found.Lines) != 2 {
		t.Fatalf("la orden por clave del cliente volvió distinta: %+v", found)
	}

	sinClave := sampleOrder("order-2", "")
	mustCreateOrder(t, st, sinClave)

	if _, err := st.OrderByClientKey(ctx, ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("una clave vacía no puede encontrar la orden sin clave, se obtuvo: %v", err)
	}
	if _, err := st.OrderByClientKey(ctx, "clave-que-no-existe"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("se esperaba ErrNotFound y se obtuvo: %v", err)
	}
}

func TestCreateOrderRejectsDuplicatedIdentifiers(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()

	mustCreateOrder(t, st, sampleOrder("order-1", "clave-1"))

	if err := st.CreateOrder(ctx, sampleOrder("order-1", "clave-2")); !errors.Is(err, store.ErrAlreadyExists) {
		t.Fatalf("una orden con el mismo id tenía que devolver ErrAlreadyExists y devolvió: %v", err)
	}
	if err := st.CreateOrder(ctx, sampleOrder("order-2", "clave-1")); !errors.Is(err, store.ErrAlreadyExists) {
		t.Fatalf("una orden con la misma clave del cliente tenía que devolver ErrAlreadyExists y devolvió: %v", err)
	}
	if _, err := st.LoadOrder(ctx, "order-2"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("la orden rechazada no puede haber quedado escrita, se obtuvo: %v", err)
	}
}

func TestSaveOrderOnlyPersistsTheHeader(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()

	mustCreateOrder(t, st, sampleOrder("order-1", "clave-1"))
	got := mustLoadOrder(t, st, "order-1")

	got.Status = store.OrderPaid
	got.PaidAt = moment().Add(time.Minute)
	got.AmountCents = 175000
	got.Lines[0].State = linestate.Delivered
	got.Lines[0].Ref = "no-se-guarda"
	got.Lines = nil

	if _, err := st.SaveOrder(ctx, got); err != nil {
		t.Fatalf("no se pudo guardar la orden: %v", err)
	}

	saved := mustLoadOrder(t, st, "order-1")
	if saved.Status != store.OrderPaid {
		t.Fatalf("el estado tenía que quedar pagado y quedó %q", saved.Status)
	}
	if !saved.PaidAt.Equal(moment().Add(time.Minute)) {
		t.Fatalf("paid_at: se esperaba %s y volvió %s", moment().Add(time.Minute), saved.PaidAt)
	}
	if saved.AmountCents != 175000 || saved.ClientKey != "clave-1" {
		t.Fatalf("la cabecera no se guardó entera: %+v", saved)
	}
	if len(saved.Lines) != 2 {
		t.Fatalf("las líneas no se pueden tocar al guardar la orden: quedaron %d", len(saved.Lines))
	}

	batidos := lineByID(t, saved, "order-1-batidos")
	if batidos.State != linestate.Pending {
		t.Fatalf("el estado de la línea tenía que quedar intacto y quedó %q", batidos.State)
	}
	if batidos.Ref != "" {
		t.Fatalf("la referencia de la línea tenía que quedar intacta y quedó %q", batidos.Ref)
	}
	if batidos.Version != 0 {
		t.Fatalf("la versión de la línea no la mueve SaveOrder y quedó en %d", batidos.Version)
	}

	if _, err := st.SaveOrder(ctx, sampleOrder("order-que-no-existe", "")); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("guardar una orden inexistente tenía que devolver ErrNotFound y devolvió: %v", err)
	}
}

func TestSaveOrderBumpsTheVersion(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()

	mustCreateOrder(t, st, sampleOrder("order-1", "clave-1"))
	loaded := mustLoadOrder(t, st, "order-1")

	if loaded.Version != 0 {
		t.Fatalf("la orden nació en versión %d, se esperaba 0", loaded.Version)
	}

	loaded.Status = store.OrderDispatching
	loaded.PaidAt = moment().Add(time.Minute)
	loaded.ClosedAt = moment().Add(3 * time.Minute)

	saved, err := st.SaveOrder(ctx, loaded)
	if err != nil {
		t.Fatalf("no se pudo guardar la orden: %v", err)
	}

	if saved.Version != loaded.Version+1 {
		t.Fatalf("la orden guardada tenía que volver en versión %d y volvió en %d", loaded.Version+1, saved.Version)
	}
	if saved.Status != store.OrderDispatching {
		t.Fatalf("la orden devuelta no trae lo guardado: %q", saved.Status)
	}

	reloaded := mustLoadOrder(t, st, "order-1")
	if reloaded.Version != saved.Version {
		t.Fatalf("la orden persistida quedó en versión %d, se esperaba %d", reloaded.Version, saved.Version)
	}
	if reloaded.Status != store.OrderDispatching || !reloaded.PaidAt.Equal(moment().Add(time.Minute)) {
		t.Fatalf("la cabecera no se persistió: %+v", reloaded)
	}
}

func TestSaveOrderRejectsAStaleVersion(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()

	mustCreateOrder(t, st, sampleOrder("order-1", "clave-1"))
	stale := mustLoadOrder(t, st, "order-1")

	first := stale
	first.Status = store.OrderDispatching
	first.PaidAt = moment().Add(time.Minute)
	first.ClosedAt = moment().Add(3 * time.Minute)

	saved, err := st.SaveOrder(ctx, first)
	if err != nil {
		t.Fatalf("el primer guardado tenía que aceptarse: %v", err)
	}

	stale.Status = store.OrderFailed
	stale.PaidAt = moment().Add(9 * time.Minute)
	stale.ClosedAt = moment().Add(9 * time.Minute)

	if _, err := st.SaveOrder(ctx, stale); !errors.Is(err, store.ErrStale) {
		t.Fatalf("una versión vieja tenía que devolver ErrStale y devolvió: %v", err)
	}

	reloaded := mustLoadOrder(t, st, "order-1")
	if reloaded.Version != saved.Version {
		t.Fatalf("la orden rechazada no puede haber movido la versión: está en %d", reloaded.Version)
	}
	if reloaded.Status != store.OrderDispatching {
		t.Fatalf("la orden rechazada pisó el estado: %q", reloaded.Status)
	}
	if !reloaded.PaidAt.Equal(moment().Add(time.Minute)) {
		t.Fatalf("la orden rechazada pisó paid_at: %s", reloaded.PaidAt)
	}
	if !reloaded.ClosedAt.Equal(moment().Add(3 * time.Minute)) {
		t.Fatalf("la orden rechazada pisó closed_at: %s", reloaded.ClosedAt)
	}
}

func TestSaveLineBumpsTheVersion(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()

	mustCreateOrder(t, st, sampleOrder("order-1", ""))
	line := lineByID(t, mustLoadOrder(t, st, "order-1"), "order-1-batidos")

	line.State = linestate.Sent
	line.Attempts = 1
	line.DeadlineAt = moment().Add(2 * time.Minute)
	line.Machine.Name = "Batidos Powermix renombrada"

	saved, err := st.SaveLine(ctx, line)
	if err != nil {
		t.Fatalf("no se pudo guardar la línea: %v", err)
	}
	if saved.Version != 1 {
		t.Fatalf("la línea guardada tenía que volver en versión 1 y volvió en %d", saved.Version)
	}
	if saved.State != linestate.Sent || saved.Attempts != 1 {
		t.Fatalf("la línea devuelta no trae lo guardado: %+v", saved)
	}

	reloaded := lineByID(t, mustLoadOrder(t, st, "order-1"), "order-1-batidos")
	if reloaded.Version != 1 || reloaded.State != linestate.Sent || reloaded.Attempts != 1 {
		t.Fatalf("la línea persistida no coincide con lo devuelto: %+v", reloaded)
	}
	if !reloaded.DeadlineAt.Equal(moment().Add(2 * time.Minute)) {
		t.Fatalf("deadline_at no se persistió: %s", reloaded.DeadlineAt)
	}
	if reloaded.Machine.Name != "Batidos Powermix renombrada" || reloaded.Machine.DeviceID != "device-1" {
		t.Fatalf("la identidad de la máquina no se actualizó entera: %+v", reloaded.Machine)
	}

	second, err := st.SaveLine(ctx, saved)
	if err != nil {
		t.Fatalf("el segundo guardado tenía que aceptarse: %v", err)
	}
	if second.Version != 2 {
		t.Fatalf("el segundo guardado tenía que devolver versión 2 y devolvió %d", second.Version)
	}
}

func TestSaveLineRejectsAStaleVersion(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()

	mustCreateOrder(t, st, sampleOrder("order-1", ""))
	stale := lineByID(t, mustLoadOrder(t, st, "order-1"), "order-1-batidos")

	first := stale
	first.Ref = "PRIMERO"
	first.State = linestate.Delivered
	if _, err := st.SaveLine(ctx, first); err != nil {
		t.Fatalf("el primer guardado tenía que aceptarse: %v", err)
	}

	stale.Ref = "SEGUNDO"
	stale.State = linestate.Failed
	if _, err := st.SaveLine(ctx, stale); !errors.Is(err, store.ErrStale) {
		t.Fatalf("una versión vieja tenía que devolver ErrStale y devolvió: %v", err)
	}

	reloaded := lineByID(t, mustLoadOrder(t, st, "order-1"), "order-1-batidos")
	if reloaded.Version != 1 {
		t.Fatalf("la línea rechazada no puede haber escrito la versión: está en %d", reloaded.Version)
	}
	if reloaded.Ref != "PRIMERO" || reloaded.State != linestate.Delivered {
		t.Fatalf("la línea rechazada pisó la fila: %+v", reloaded)
	}
}

func TestSaveLineOnAMissingLine(t *testing.T) {
	st, _ := newStore(t)

	_, err := st.SaveLine(context.Background(), store.Line{
		ID:       "linea-que-no-existe",
		OrderID:  "orden-que-no-existe",
		Position: 1,
		Machine:  machine.Ref{Kind: "batidos"},
		SKU:      "batido-frutilla",
		State:    linestate.Sent,
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("se esperaba ErrNotFound y se obtuvo: %v", err)
	}
}

func TestAppendAttemptIsIdempotentByKeyAndByAttemptNo(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()

	mustCreateOrder(t, st, sampleOrder("order-1", ""))
	lineID := "order-1-batidos"

	first := store.Attempt{
		ID:             "attempt-1",
		LineID:         lineID,
		Number:         1,
		IdempotencyKey: "clave-intento-1",
		Outcome:        "accepted",
		StartedAt:      moment(),
	}
	if err := st.AppendAttempt(ctx, first); err != nil {
		t.Fatalf("no se pudo registrar el intento: %v", err)
	}

	repeated := first
	repeated.ID = "attempt-2"
	if err := st.AppendAttempt(ctx, repeated); !errors.Is(err, store.ErrAlreadyExists) {
		t.Fatalf("la misma clave de idempotencia tenía que devolver ErrAlreadyExists y devolvió: %v", err)
	}

	sameNumber := first
	sameNumber.ID = "attempt-3"
	sameNumber.IdempotencyKey = "clave-intento-2"
	if err := st.AppendAttempt(ctx, sameNumber); !errors.Is(err, store.ErrAlreadyExists) {
		t.Fatalf("el mismo número de intento en la misma línea tenía que devolver ErrAlreadyExists y devolvió: %v", err)
	}

	reloaded := lineByID(t, mustLoadOrder(t, st, "order-1"), lineID)
	if reloaded.Version != 0 || reloaded.Attempts != 0 {
		t.Fatalf("registrar un intento no puede tocar la línea: %+v", reloaded)
	}
}

func TestSaveAttemptClosesAKnownAttemptOnly(t *testing.T) {
	st, admin := newStore(t)
	ctx := context.Background()

	mustCreateOrder(t, st, sampleOrder("order-1", ""))
	lineID := "order-1-batidos"

	attempt := store.Attempt{
		ID:             "attempt-1",
		LineID:         lineID,
		Number:         1,
		IdempotencyKey: "clave-intento-1",
		Outcome:        "accepted",
		StartedAt:      moment(),
	}
	if err := st.AppendAttempt(ctx, attempt); err != nil {
		t.Fatalf("no se pudo registrar el intento: %v", err)
	}

	attempt.Outcome = "rejected"
	attempt.Detail = "el proveedor rechazó el código"
	attempt.FinishedAt = moment().Add(time.Second)
	if err := st.SaveAttempt(ctx, attempt); err != nil {
		t.Fatalf("no se pudo cerrar el intento: %v", err)
	}

	var (
		outcome  string
		detail   sql.NullString
		finished sql.NullTime
	)
	if err := admin.QueryRow(`SELECT outcome, error_detail, finished_at FROM dispatch_attempt WHERE idempotency_key = $1`,
		"clave-intento-1").Scan(&outcome, &detail, &finished); err != nil {
		t.Fatalf("no se pudo leer el intento guardado: %v", err)
	}
	if outcome != "rejected" || detail.String != "el proveedor rechazó el código" {
		t.Fatalf("el intento no se cerró con lo informado: outcome=%q detail=%q", outcome, detail.String)
	}
	if !finished.Valid || !finished.Time.Equal(moment().Add(time.Second)) {
		t.Fatalf("finished_at no se guardó: %v", finished)
	}

	reloaded := lineByID(t, mustLoadOrder(t, st, "order-1"), lineID)
	if reloaded.Version != 0 || reloaded.State != linestate.Pending {
		t.Fatalf("cerrar un intento no puede tocar la línea: %+v", reloaded)
	}

	if err := st.SaveAttempt(ctx, store.Attempt{IdempotencyKey: "clave-que-no-existe", Outcome: "accepted"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cerrar un intento inexistente tenía que devolver ErrNotFound y devolvió: %v", err)
	}
}

func TestRegisterWebhookOnlyAcceptsTheFirstEvent(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()

	duplicate, err := st.RegisterWebhook(ctx, store.Webhook{
		ID:         "webhook-1",
		Source:     "snacks",
		DedupeKey:  "orderNo|subOrderNo|1",
		ReceivedAt: moment(),
	})
	if err != nil {
		t.Fatalf("no se pudo registrar el webhook: %v", err)
	}
	if duplicate {
		t.Fatal("el primer evento no puede ser un duplicado")
	}

	repeated, err := st.RegisterWebhook(ctx, store.Webhook{
		ID:         "webhook-2",
		Source:     "snacks",
		DedupeKey:  "orderNo|subOrderNo|1",
		ReceivedAt: moment().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("el evento repetido no puede devolver error: %v", err)
	}
	if !repeated {
		t.Fatal("el evento repetido tenía que devolver duplicate=true")
	}

	otherSource, err := st.RegisterWebhook(ctx, store.Webhook{
		ID:        "webhook-3",
		Source:    "batidos",
		DedupeKey: "orderNo|subOrderNo|1",
	})
	if err != nil {
		t.Fatalf("no se pudo registrar el webhook de la otra máquina: %v", err)
	}
	if otherSource {
		t.Fatal("la misma clave en otra máquina es un evento nuevo")
	}
}

func TestDueLinesSelectsAndOrdersByDeadline(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()

	now := moment().Add(5 * time.Minute)
	mustCreateOrder(t, st, orderWithDeadline("order-tarde", moment().Add(9*time.Minute)))
	mustCreateOrder(t, st, orderWithDeadline("order-media", moment().Add(3*time.Minute)))
	mustCreateOrder(t, st, orderWithDeadline("order-pronto", moment().Add(time.Minute)))
	mustCreateOrder(t, st, orderWithDeadline("order-sin-plazo", time.Time{}))

	due, err := st.DueLines(ctx, now, 10)
	if err != nil {
		t.Fatalf("no se pudo consultar las líneas vencidas: %v", err)
	}
	if len(due) != 2 {
		t.Fatalf("se esperaban 2 líneas vencidas y volvieron %d", len(due))
	}
	if due[0].ID != "order-pronto-line" || due[1].ID != "order-media-line" {
		t.Fatalf("las líneas no salieron ordenadas por plazo: %q, %q", due[0].ID, due[1].ID)
	}
	if due[0].OrderID != "order-pronto" || due[0].Machine.Kind != "snacks" || due[0].State != linestate.Sent {
		t.Fatalf("la línea vencida volvió incompleta: %+v", due[0])
	}

	limited, err := st.DueLines(ctx, now, 1)
	if err != nil {
		t.Fatalf("no se pudo consultar con tope: %v", err)
	}
	if len(limited) != 1 || limited[0].ID != "order-pronto-line" {
		t.Fatalf("el tope no se respetó: %+v", limited)
	}

	none, err := st.DueLines(ctx, moment(), 10)
	if err != nil {
		t.Fatalf("no se pudo consultar sin vencimientos: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("no hay líneas vencidas antes del primer plazo y volvieron %d", len(none))
	}
}

func storedLine(lineID string, position int, state linestate.State, deadline time.Time) store.Line {
	return store.Line{
		ID:         lineID,
		Position:   position,
		Machine:    machine.Ref{Kind: "snacks", MID: "mid-1"},
		SKU:        "snack-mixto",
		State:      state,
		DeadlineAt: deadline,
	}
}

func orderWithLine(orderID string, status store.OrderStatus, paidAt time.Time, line store.Line) store.Order {
	line.OrderID = orderID

	return store.Order{
		ID:        orderID,
		KioskID:   kioskID,
		ComboID:   comboID,
		Status:    status,
		CreatedAt: moment(),
		PaidAt:    paidAt,
		Lines:     []store.Line{line},
	}
}

func TestDueLinesIncludesAClaimedPendingLineAndSkipsAnUnclaimedOne(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()

	now := moment().Add(5 * time.Minute)

	mustCreateOrder(t, st, orderWithLine("order-reclamada", store.OrderDispatching, moment(),
		storedLine("line-reclamada", 1, linestate.Pending, moment().Add(time.Minute))))
	mustCreateOrder(t, st, orderWithLine("order-sin-plazo", store.OrderDispatching, moment(),
		storedLine("line-sin-plazo", 1, linestate.Pending, time.Time{})))

	due, err := st.DueLines(ctx, now, 10)
	if err != nil {
		t.Fatalf("no se pudo consultar las líneas vencidas: %v", err)
	}
	if len(due) != 1 || due[0].ID != "line-reclamada" {
		t.Fatalf("DueLines devolvió %d líneas, se esperaba sólo la pendiente con plazo vencido: %+v", len(due), due)
	}
	if due[0].State != linestate.Pending {
		t.Fatalf("la línea reclamada volvió en %q, se esperaba %q", due[0].State, linestate.Pending)
	}
}

func TestStalledLinesSelectsUnclaimedPendingLinesOfPaidOrders(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()

	cut := moment().Add(5 * time.Minute)

	mustCreateOrder(t, st, orderWithLine("order-paga", store.OrderPaid, moment(),
		storedLine("line-paga", 1, linestate.Pending, time.Time{})))
	mustCreateOrder(t, st, orderWithLine("order-despachando", store.OrderDispatching, moment().Add(time.Minute),
		storedLine("line-despachando", 1, linestate.Pending, time.Time{})))
	mustCreateOrder(t, st, orderWithLine("order-reclamada", store.OrderPaid, moment(),
		storedLine("line-reclamada", 1, linestate.Pending, moment().Add(time.Minute))))
	mustCreateOrder(t, st, orderWithLine("order-reciente", store.OrderPaid, moment().Add(10*time.Minute),
		storedLine("line-reciente", 1, linestate.Pending, time.Time{})))
	mustCreateOrder(t, st, orderWithLine("order-sin-pago", store.OrderPendingPayment, time.Time{},
		storedLine("line-sin-pago", 1, linestate.Pending, time.Time{})))
	mustCreateOrder(t, st, orderWithLine("order-entregada", store.OrderDelivered, moment(),
		storedLine("line-entregada", 1, linestate.Pending, time.Time{})))

	stalled, err := st.StalledLines(ctx, cut, 10)
	if err != nil {
		t.Fatalf("no se pudo consultar las líneas trabadas: %v", err)
	}
	if len(stalled) != 2 || stalled[0].ID != "line-paga" || stalled[1].ID != "line-despachando" {
		t.Fatalf("StalledLines devolvió %d líneas, se esperaban la paga y la que quedó despachando: %+v", len(stalled), stalled)
	}

	limited, err := st.StalledLines(ctx, cut, 1)
	if err != nil {
		t.Fatalf("no se pudo consultar con tope: %v", err)
	}
	if len(limited) != 1 || limited[0].ID != "line-paga" {
		t.Fatalf("el tope no se respetó: %+v", limited)
	}

	none, err := st.StalledLines(ctx, moment().Add(-time.Minute), 10)
	if err != nil {
		t.Fatalf("no se pudo consultar antes del corte: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("no hay líneas trabadas antes del corte y volvieron %d", len(none))
	}
}

func TestNotificationSentStartsFalseAndThenStaysTrue(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()

	mustCreateOrder(t, st, sampleOrder("order-notice", ""))

	sent, err := st.NotificationSent(ctx, "order-notice", "order_in_doubt")
	if err != nil {
		t.Fatalf("no se pudo preguntar por el aviso: %v", err)
	}
	if sent {
		t.Fatal("una orden recién creada informa un aviso enviado")
	}

	if err := st.RecordNotification(ctx, "order-notice", "order_in_doubt", moment()); err != nil {
		t.Fatalf("no se pudo registrar el aviso: %v", err)
	}

	sent, err = st.NotificationSent(ctx, "order-notice", "order_in_doubt")
	if err != nil {
		t.Fatalf("no se pudo preguntar por el aviso: %v", err)
	}
	if !sent {
		t.Fatal("el aviso grabado no se informa como enviado")
	}

	if err := st.RecordNotification(ctx, "order-notice", "order_in_doubt", moment().Add(time.Minute)); err != nil {
		t.Fatalf("grabar dos veces el mismo aviso devolvió error: %v", err)
	}

	sent, err = st.NotificationSent(ctx, "order-notice", "order_in_doubt")
	if err != nil {
		t.Fatalf("no se pudo preguntar por el aviso: %v", err)
	}
	if !sent {
		t.Fatal("la segunda grabación borró el aviso")
	}
}

func TestNotificationSentKeepsTheKindsAndOrdersIndependent(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()

	mustCreateOrder(t, st, sampleOrder("order-uno", ""))
	mustCreateOrder(t, st, sampleOrder("order-dos", ""))

	if err := st.RecordNotification(ctx, "order-uno", "order_in_doubt", moment()); err != nil {
		t.Fatalf("no se pudo registrar el aviso: %v", err)
	}

	cases := []struct {
		name    string
		orderID string
		kind    string
		want    bool
	}{
		{name: "el aviso grabado está enviado", orderID: "order-uno", kind: "order_in_doubt", want: true},
		{name: "el otro tipo de aviso sigue pendiente", orderID: "order-uno", kind: "order_failed", want: false},
		{name: "el aviso de la otra orden sigue pendiente", orderID: "order-dos", kind: "order_in_doubt", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sent, err := st.NotificationSent(ctx, tc.orderID, tc.kind)
			if err != nil {
				t.Fatalf("no se pudo preguntar por el aviso: %v", err)
			}
			if sent != tc.want {
				t.Fatalf("NotificationSent(%q, %q)=%v, se esperaba %v", tc.orderID, tc.kind, sent, tc.want)
			}
		})
	}
}

func TestNotificationsReturnNotFoundForAnUnknownOrder(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()

	if _, err := st.NotificationSent(ctx, "order-fantasma", "order_in_doubt"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("NotificationSent devolvió %v, se esperaba %v", err, store.ErrNotFound)
	}

	if err := st.RecordNotification(ctx, "order-fantasma", "order_in_doubt", moment()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("RecordNotification devolvió %v, se esperaba %v", err, store.ErrNotFound)
	}
}

func TestCreateOrderRejectsLinesThatSQLRejects(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()

	cases := []struct {
		name    string
		orderID string
		mutate  func(*store.Line)
		want    error
	}{
		{
			name:    "una línea con una familia de máquina desconocida",
			orderID: "order-kind-unknown",
			mutate: func(line *store.Line) {
				line.Machine.Kind = "express"
			},
			want: store.ErrInvalid,
		},
		{
			name:    "una línea sin familia de máquina",
			orderID: "order-kind-empty",
			mutate: func(line *store.Line) {
				line.Machine.Kind = ""
			},
			want: store.ErrInvalid,
		},
		{
			name:    "una línea con importe negativo",
			orderID: "order-line-negative",
			mutate: func(line *store.Line) {
				line.AmountCents = -1
			},
			want: store.ErrInvalid,
		},
		{
			name:    "una línea con un estado que el esquema no conoce",
			orderID: "order-state-unknown",
			mutate: func(line *store.Line) {
				line.State = "cancelled"
			},
			want: store.ErrInvalid,
		},
		{
			name:    "dos líneas en la misma posición",
			orderID: "order-position-taken",
			mutate: func(line *store.Line) {
				line.Position = 1
			},
			want: store.ErrAlreadyExists,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			order := sampleOrder(tc.orderID, "")
			tc.mutate(&order.Lines[1])

			if err := st.CreateOrder(ctx, order); !errors.Is(err, tc.want) {
				t.Fatalf("CreateOrder devolvió %v, se esperaba %v", err, tc.want)
			}

			if _, err := st.LoadOrder(ctx, tc.orderID); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("una orden rechazada no puede quedar guardada: LoadOrder devolvió %v", err)
			}
		})
	}
}

func TestCreateOrderRejectsHeadersThatSQLRejects(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()

	cases := []struct {
		name    string
		orderID string
		mutate  func(*store.Order)
	}{
		{
			name:    "una orden con importe negativo",
			orderID: "order-header-negative",
			mutate: func(order *store.Order) {
				order.AmountCents = -1
			},
		},
		{
			name:    "una orden con un estado que el esquema no conoce",
			orderID: "order-header-unknown",
			mutate: func(order *store.Order) {
				order.Status = "cart"
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			order := sampleOrder(tc.orderID, "")
			tc.mutate(&order)

			if err := st.CreateOrder(ctx, order); !errors.Is(err, store.ErrInvalid) {
				t.Fatalf("CreateOrder devolvió %v, se esperaba %v", err, store.ErrInvalid)
			}

			if _, err := st.LoadOrder(ctx, tc.orderID); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("una orden rechazada no puede quedar guardada: LoadOrder devolvió %v", err)
			}
		})
	}
}

func TestSaveLineRejectsLinesThatSQLRejects(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()

	cases := []struct {
		name    string
		orderID string
		mutate  func(*store.Line)
		want    error
	}{
		{
			name:    "una familia de máquina desconocida",
			orderID: "order-save-kind-unknown",
			mutate: func(line *store.Line) {
				line.Machine.Kind = "express"
			},
			want: store.ErrInvalid,
		},
		{
			name:    "una línea sin familia de máquina",
			orderID: "order-save-kind-empty",
			mutate: func(line *store.Line) {
				line.Machine.Kind = ""
			},
			want: store.ErrInvalid,
		},
		{
			name:    "un importe negativo",
			orderID: "order-save-negative",
			mutate: func(line *store.Line) {
				line.AmountCents = -1
			},
			want: store.ErrInvalid,
		},
		{
			name:    "un estado que el esquema no conoce",
			orderID: "order-save-state-unknown",
			mutate: func(line *store.Line) {
				line.State = "cancelled"
			},
			want: store.ErrInvalid,
		},
		{
			name:    "una posición que ya usa otra línea de la orden",
			orderID: "order-save-position-taken",
			mutate: func(line *store.Line) {
				line.Position = 2
			},
			want: store.ErrAlreadyExists,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mustCreateOrder(t, st, sampleOrder(tc.orderID, ""))

			before := lineByID(t, mustLoadOrder(t, st, tc.orderID), tc.orderID+"-batidos")

			changed := before
			tc.mutate(&changed)

			if _, err := st.SaveLine(ctx, changed); !errors.Is(err, tc.want) {
				t.Fatalf("SaveLine devolvió %v, se esperaba %v", err, tc.want)
			}

			after := lineByID(t, mustLoadOrder(t, st, tc.orderID), before.ID)
			if after != before {
				t.Fatalf("una línea rechazada no puede quedar guardada: %+v, se esperaba %+v", after, before)
			}
		})
	}
}

func TestSaveOrderRejectsHeadersThatSQLRejects(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()

	cases := []struct {
		name    string
		orderID string
		mutate  func(*store.Order)
	}{
		{
			name:    "un importe negativo",
			orderID: "order-save-header-negative",
			mutate: func(order *store.Order) {
				order.AmountCents = -1
			},
		},
		{
			name:    "un estado que el esquema no conoce",
			orderID: "order-save-header-unknown",
			mutate: func(order *store.Order) {
				order.Status = "cart"
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mustCreateOrder(t, st, sampleOrder(tc.orderID, ""))

			before := mustLoadOrder(t, st, tc.orderID)

			changed := before
			tc.mutate(&changed)

			if _, err := st.SaveOrder(ctx, changed); !errors.Is(err, store.ErrInvalid) {
				t.Fatalf("SaveOrder devolvió %v, se esperaba %v", err, store.ErrInvalid)
			}

			after := mustLoadOrder(t, st, tc.orderID)
			if after.Status != before.Status || after.AmountCents != before.AmountCents || after.Version != before.Version {
				t.Fatalf("una orden rechazada no puede quedar guardada: %+v, se esperaba %+v", after, before)
			}
		})
	}
}

func TestAcceptsTheValuesSQLAccepts(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()

	order := sampleOrder("order-limites", "")
	order.AmountCents = 0
	order.Lines[0].AmountCents = 0
	order.Lines[0].State = linestate.State("refunded")
	order.Lines[0].Machine = machine.Ref{KioskID: kioskID, Kind: "batidos", Name: "Batidos Powermix"}

	mustCreateOrder(t, st, order)

	got := lineByID(t, mustLoadOrder(t, st, "order-limites"), "order-limites-batidos")
	if got.State != linestate.State("refunded") || got.AmountCents != 0 {
		t.Fatalf("el estado refunded y el importe cero son legales en el esquema: %+v", got)
	}
	if got.Machine.DeviceID != "" || got.Machine.DeviceNo != "" || got.Machine.DeviceTypeID != "" {
		t.Fatalf("la línea no lleva los identificadores en la tabla y no tiene por qué traerlos: %+v", got.Machine)
	}

	got.Attempts = 1
	if _, err := st.SaveLine(ctx, got); err != nil {
		t.Fatalf("una línea de batidos sin identificadores es legal en el esquema: %v", err)
	}
}

func TestChecksTheSchemaBeforeTheConflictAndTheVersionBeforeTheSchema(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()

	mustCreateOrder(t, st, sampleOrder("order-precedencia", ""))

	repeated := sampleOrder("order-precedencia", "")
	repeated.AmountCents = -1

	if err := st.CreateOrder(ctx, repeated); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("una orden repetida y además inválida devolvió %v, se esperaba %v: el CHECK va antes que la clave", err, store.ErrInvalid)
	}

	before := mustLoadOrder(t, st, "order-precedencia")

	staleLine := before.Lines[0]
	staleLine.Version = 7
	staleLine.State = "cancelled"

	if _, err := st.SaveLine(ctx, staleLine); !errors.Is(err, store.ErrStale) {
		t.Fatalf("una línea con la versión vieja y un estado inválido devolvió %v, se esperaba %v: la versión va antes que el CHECK", err, store.ErrStale)
	}

	staleOrder := before
	staleOrder.Version = 7
	staleOrder.Status = "cart"

	if _, err := st.SaveOrder(ctx, staleOrder); !errors.Is(err, store.ErrStale) {
		t.Fatalf("una orden con la versión vieja y un estado inválido devolvió %v, se esperaba %v: la versión va antes que el CHECK", err, store.ErrStale)
	}
}

func TestAppendAttemptRejectsInvalidOutcome(t *testing.T) {
	cases := []struct {
		name    string
		outcome store.AttemptOutcome
	}{
		{name: "outcome desconocido", outcome: "maybe"},
		{name: "outcome vacío", outcome: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, _ := newStore(t)
			ctx := context.Background()
			orderID := "order-attempt-invalid"
			mustCreateOrder(t, st, sampleOrder(orderID, ""))
			lineID := orderID + "-batidos"

			invalid := store.Attempt{
				ID:             "attempt-invalid",
				LineID:         lineID,
				Number:         1,
				IdempotencyKey: "key-invalid",
				Outcome:        tc.outcome,
				StartedAt:      moment(),
			}

			err := st.AppendAttempt(ctx, invalid)
			if !errors.Is(err, store.ErrInvalid) {
				t.Fatalf("AppendAttempt con outcome %q devolvió %v, se esperaba %v", tc.outcome, err, store.ErrInvalid)
			}

			valid := invalid
			valid.ID = "attempt-valid"
			valid.Outcome = "accepted"
			if err := st.AppendAttempt(ctx, valid); err != nil {
				t.Fatalf("un intento posterior válido con la misma clave y número falló: %v", err)
			}
		})
	}
}

func TestRegisterWebhookRejectsInvalidSource(t *testing.T) {
	cases := []struct {
		name   string
		source string
	}{
		{name: "fuente desconocida", source: "stripe"},
		{name: "fuente vacía", source: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, _ := newStore(t)
			ctx := context.Background()

			invalid := store.Webhook{
				ID:         "wh-invalid",
				Source:     tc.source,
				DedupeKey:  "dedupe-1",
				ReceivedAt: moment(),
			}

			_, err := st.RegisterWebhook(ctx, invalid)
			if !errors.Is(err, store.ErrInvalid) {
				t.Fatalf("RegisterWebhook con fuente %q devolvió %v, se esperaba %v", tc.source, err, store.ErrInvalid)
			}

			valid := store.Webhook{
				ID:         "wh-valid",
				Source:     "payment",
				DedupeKey:  "dedupe-1",
				ReceivedAt: moment(),
			}
			duplicate, err := st.RegisterWebhook(ctx, valid)
			if err != nil {
				t.Fatalf("RegisterWebhook con fuente válida devolvió error: %v", err)
			}
			if duplicate {
				t.Fatalf("el intento rechazado dejó la clave de deduplicación tomada: duplicate=%v, se esperaba false", duplicate)
			}
		})
	}
}

func TestAppendAttemptAndWebhookAcceptValidBoundaries(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()
	orderID := "order-attempt-boundaries"
	mustCreateOrder(t, st, sampleOrder(orderID, ""))
	lineID := orderID + "-batidos"

	outcomes := []store.AttemptOutcome{"accepted", "rejected", "ambiguous", "error"}
	for i, outcome := range outcomes {
		attempt := store.Attempt{
			ID:             fmt.Sprintf("attempt-boundary-%d", i+1),
			LineID:         lineID,
			Number:         i + 1,
			IdempotencyKey: fmt.Sprintf("key-boundary-%d", i+1),
			Outcome:        outcome,
			StartedAt:      moment(),
		}
		if err := st.AppendAttempt(ctx, attempt); err != nil {
			t.Fatalf("AppendAttempt con outcome válido %q falló: %v", outcome, err)
		}
	}

	sources := []string{"payment", "batidos", "snacks"}
	for i, source := range sources {
		wh := store.Webhook{
			ID:         fmt.Sprintf("wh-boundary-%d", i+1),
			Source:     source,
			DedupeKey:  fmt.Sprintf("dedupe-boundary-%d", i+1),
			ReceivedAt: moment(),
		}
		duplicate, err := st.RegisterWebhook(ctx, wh)
		if err != nil {
			t.Fatalf("RegisterWebhook con fuente válida %q falló: %v", source, err)
		}
		if duplicate {
			t.Fatalf("RegisterWebhook con fuente válida %q devolvió duplicate=true", source)
		}
	}
}

func TestAppendAttemptRejectsDuplicateAttemptNumberOnSameLine(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()
	orderID := "order-attempt-dup-no"
	mustCreateOrder(t, st, sampleOrder(orderID, ""))
	lineID := orderID + "-batidos"

	first := store.Attempt{
		ID:             "attempt-num-1",
		LineID:         lineID,
		Number:         1,
		IdempotencyKey: "key-attempt-1",
		Outcome:        "accepted",
		StartedAt:      moment(),
	}
	if err := st.AppendAttempt(ctx, first); err != nil {
		t.Fatalf("AppendAttempt: %v", err)
	}

	sameNumber := store.Attempt{
		ID:             "attempt-num-2",
		LineID:         lineID,
		Number:         1,
		IdempotencyKey: "key-attempt-2",
		Outcome:        "error",
		StartedAt:      moment(),
	}
	if err := st.AppendAttempt(ctx, sameNumber); !errors.Is(err, store.ErrAlreadyExists) {
		t.Fatalf("el mismo número de intento en la misma línea con clave distinta devolvió %v, se esperaba %v", err, store.ErrAlreadyExists)
	}

	fresh := store.Attempt{
		ID:             "attempt-num-3",
		LineID:         lineID,
		Number:         2,
		IdempotencyKey: "key-attempt-2",
		Outcome:        "accepted",
		StartedAt:      moment(),
	}
	if err := st.AppendAttempt(ctx, fresh); err != nil {
		t.Fatalf("AppendAttempt con la clave del intento rechazado y un número nuevo falló: %v", err)
	}
}

func TestSaveAttemptRejectsInvalidOutcome(t *testing.T) {
	cases := []struct {
		name    string
		outcome store.AttemptOutcome
	}{
		{name: "outcome desconocido", outcome: "maybe"},
		{name: "outcome vacío", outcome: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, admin := newStore(t)
			ctx := context.Background()
			orderID := "order-save-invalid"
			mustCreateOrder(t, st, sampleOrder(orderID, ""))
			lineID := orderID + "-batidos"

			attempt := store.Attempt{
				ID:             "attempt-1",
				LineID:         lineID,
				Number:         1,
				IdempotencyKey: "key-1",
				Outcome:        "accepted",
				StartedAt:      moment(),
			}
			if err := st.AppendAttempt(ctx, attempt); err != nil {
				t.Fatalf("AppendAttempt: %v", err)
			}

			invalid := attempt
			invalid.Outcome = tc.outcome
			invalid.FinishedAt = moment().Add(time.Second)

			err := st.SaveAttempt(ctx, invalid)
			if !errors.Is(err, store.ErrInvalid) {
				t.Fatalf("SaveAttempt con outcome %q devolvió %v, se esperaba %v", tc.outcome, err, store.ErrInvalid)
			}

			if err := st.AppendAttempt(ctx, attempt); !errors.Is(err, store.ErrAlreadyExists) {
				t.Fatalf("el intento rechazado alteró la clave de idempotencia: %v", err)
			}

			var outcome string
			if err := admin.QueryRow(`SELECT outcome FROM dispatch_attempt WHERE idempotency_key = $1`, "key-1").Scan(&outcome); err != nil {
				t.Fatalf("no se pudo leer el intento: %v", err)
			}
			if outcome != "accepted" {
				t.Fatalf("el outcome del intento cambió a %q", outcome)
			}

			valid := attempt
			valid.Outcome = "rejected"
			valid.Detail = "rechazo posterior válido"
			valid.FinishedAt = moment().Add(2 * time.Second)
			if err := st.SaveAttempt(ctx, valid); err != nil {
				t.Fatalf("SaveAttempt posterior con outcome válido falló: %v", err)
			}
		})
	}
}

func TestAppendAttemptChecksTheSchemaBeforeTheConflict(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()
	orderID := "order-attempt-precedence"
	mustCreateOrder(t, st, sampleOrder(orderID, ""))
	lineID := orderID + "-batidos"

	first := store.Attempt{
		ID:             "attempt-1",
		LineID:         lineID,
		Number:         1,
		IdempotencyKey: "key-1",
		Outcome:        "accepted",
		StartedAt:      moment(),
	}
	if err := st.AppendAttempt(ctx, first); err != nil {
		t.Fatalf("AppendAttempt: %v", err)
	}

	repeatedKey := store.Attempt{
		ID:             "attempt-2",
		LineID:         lineID,
		Number:         2,
		IdempotencyKey: "key-1",
		Outcome:        "maybe",
		StartedAt:      moment(),
	}
	if err := st.AppendAttempt(ctx, repeatedKey); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("un intento con clave repetida y outcome inválido devolvió %v, se esperaba %v", err, store.ErrInvalid)
	}

	repeatedNumber := store.Attempt{
		ID:             "attempt-3",
		LineID:         lineID,
		Number:         1,
		IdempotencyKey: "key-2",
		Outcome:        "",
		StartedAt:      moment(),
	}
	if err := st.AppendAttempt(ctx, repeatedNumber); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("un intento con número repetido y outcome vacío devolvió %v, se esperaba %v", err, store.ErrInvalid)
	}
}
