package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/store"
	"compras-remotas/internal/store/postgres"
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
	files, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.sql"))
	if err != nil {
		return fmt.Errorf("no se pudo listar las migraciones: %w", err)
	}

	sort.Strings(files)

	db, err := sql.Open("pgx", target)
	if err != nil {
		return fmt.Errorf("no se pudo abrir la base para migrar: %w", err)
	}
	defer db.Close()

	for _, file := range files {
		statements, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("no se pudo leer la migración %s: %w", filepath.Base(file), err)
		}

		if _, err := db.Exec(string(statements)); err != nil {
			return fmt.Errorf("no se pudo aplicar la migración %s: %w", filepath.Base(file), err)
		}
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

	if _, err := db.Exec(`TRUNCATE webhook_event, dispatch_attempt, order_line, orders, payment_intent,
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
				ID:          orderID + "-batidos",
				OrderID:     orderID,
				Position:    1,
				MachineType: "batidos",
				Machine: dispenser.MachineRef{
					KioskID:      kioskID,
					Type:         "batidos",
					Name:         "Batidos Powermix",
					DeviceID:     "device-1",
					DeviceNo:     "device-no-1",
					DeviceTypeID: "type-1",
				},
				SKU:         "batido-frutilla",
				AmountCents: 70000,
				State:       dispenser.LinePending,
				Version:     7,
			},
			{
				ID:          orderID + "-snacks",
				OrderID:     orderID,
				Position:    2,
				MachineType: "snacks",
				Machine: dispenser.MachineRef{
					KioskID: kioskID,
					Type:    "snacks",
					Name:    "Snacks Powermix",
					MID:     "mid-1",
				},
				SKU:            "snack-mixto",
				AmountCents:    80000,
				State:          dispenser.LinePending,
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
				ID:          orderID + "-line",
				OrderID:     orderID,
				Position:    1,
				MachineType: "snacks",
				Machine:     dispenser.MachineRef{Type: "snacks", MID: "mid-1"},
				SKU:         "snack-mixto",
				State:       dispenser.LineSent,
				DeadlineAt:  deadline,
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
	if batidos.ID != "order-1-batidos" || batidos.Position != 1 || batidos.MachineType != "batidos" {
		t.Fatalf("la primera línea no es la de batidos: %+v", batidos)
	}
	if batidos.Version != 0 {
		t.Fatalf("toda línea tiene que nacer en versión 0 y nació en %d", batidos.Version)
	}
	if batidos.Machine.DeviceID != "device-1" || batidos.Machine.DeviceNo != "device-no-1" ||
		batidos.Machine.DeviceTypeID != "type-1" || batidos.Machine.Name != "Batidos Powermix" ||
		batidos.Machine.KioskID != kioskID || batidos.Machine.Type != "batidos" {
		t.Fatalf("la identidad de la máquina de batidos volvió incompleta: %+v", batidos.Machine)
	}
	if batidos.State != dispenser.LinePending || batidos.SKU != "batido-frutilla" || batidos.AmountCents != 70000 {
		t.Fatalf("la primera línea volvió distinta: %+v", batidos)
	}
	if !batidos.DeadlineAt.IsZero() || !batidos.ReconcileUntil.IsZero() || batidos.Ref != "" || batidos.LastError != "" {
		t.Fatalf("la primera línea tenía que volver sin plazos ni referencia: %+v", batidos)
	}

	snacks := got.Lines[1]
	if snacks.ID != "order-1-snacks" || snacks.Position != 2 || snacks.MachineType != "snacks" {
		t.Fatalf("la segunda línea no es la de snacks: %+v", snacks)
	}
	if snacks.Machine.MID != "mid-1" || snacks.Machine.Name != "Snacks Powermix" || snacks.Machine.Type != "snacks" {
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
	got.Lines[0].State = dispenser.LineDelivered
	got.Lines[0].Ref = "no-se-guarda"
	got.Lines = nil

	if err := st.SaveOrder(ctx, got); err != nil {
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
	if batidos.State != dispenser.LinePending {
		t.Fatalf("el estado de la línea tenía que quedar intacto y quedó %q", batidos.State)
	}
	if batidos.Ref != "" {
		t.Fatalf("la referencia de la línea tenía que quedar intacta y quedó %q", batidos.Ref)
	}
	if batidos.Version != 0 {
		t.Fatalf("la versión de la línea no la mueve SaveOrder y quedó en %d", batidos.Version)
	}

	if err := st.SaveOrder(ctx, sampleOrder("order-que-no-existe", "")); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("guardar una orden inexistente tenía que devolver ErrNotFound y devolvió: %v", err)
	}
}

func TestSaveLineBumpsTheVersion(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()

	mustCreateOrder(t, st, sampleOrder("order-1", ""))
	line := lineByID(t, mustLoadOrder(t, st, "order-1"), "order-1-batidos")

	line.State = dispenser.LineSent
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
	if saved.State != dispenser.LineSent || saved.Attempts != 1 {
		t.Fatalf("la línea devuelta no trae lo guardado: %+v", saved)
	}

	reloaded := lineByID(t, mustLoadOrder(t, st, "order-1"), "order-1-batidos")
	if reloaded.Version != 1 || reloaded.State != dispenser.LineSent || reloaded.Attempts != 1 {
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
	first.State = dispenser.LineDelivered
	if _, err := st.SaveLine(ctx, first); err != nil {
		t.Fatalf("el primer guardado tenía que aceptarse: %v", err)
	}

	stale.Ref = "SEGUNDO"
	stale.State = dispenser.LineFailed
	if _, err := st.SaveLine(ctx, stale); !errors.Is(err, store.ErrStale) {
		t.Fatalf("una versión vieja tenía que devolver ErrStale y devolvió: %v", err)
	}

	reloaded := lineByID(t, mustLoadOrder(t, st, "order-1"), "order-1-batidos")
	if reloaded.Version != 1 {
		t.Fatalf("la línea rechazada no puede haber escrito la versión: está en %d", reloaded.Version)
	}
	if reloaded.Ref != "PRIMERO" || reloaded.State != dispenser.LineDelivered {
		t.Fatalf("la línea rechazada pisó la fila: %+v", reloaded)
	}
}

func TestSaveLineOnAMissingLine(t *testing.T) {
	st, _ := newStore(t)

	_, err := st.SaveLine(context.Background(), store.Line{
		ID:          "linea-que-no-existe",
		OrderID:     "orden-que-no-existe",
		Position:    1,
		MachineType: "batidos",
		SKU:         "batido-frutilla",
		State:       dispenser.LineSent,
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
	if reloaded.Version != 0 || reloaded.State != dispenser.LinePending {
		t.Fatalf("cerrar un intento no puede tocar la línea: %+v", reloaded)
	}

	if err := st.SaveAttempt(ctx, store.Attempt{IdempotencyKey: "clave-que-no-existe", Outcome: "accepted"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cerrar un intento inexistente tenía que devolver ErrNotFound y devolvió: %v", err)
	}
}

func TestRegisterWebhookOnlyAcceptsTheFirstEvent(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()

	first, err := st.RegisterWebhook(ctx, store.Webhook{
		ID:         "webhook-1",
		Source:     "snacks",
		DedupeKey:  "orderNo|subOrderNo|1",
		ReceivedAt: moment(),
	})
	if err != nil {
		t.Fatalf("no se pudo registrar el webhook: %v", err)
	}
	if !first {
		t.Fatal("el primer evento tenía que devolver first=true")
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
	if repeated {
		t.Fatal("el evento repetido tenía que devolver first=false")
	}

	otherSource, err := st.RegisterWebhook(ctx, store.Webhook{
		ID:        "webhook-3",
		Source:    "batidos",
		DedupeKey: "orderNo|subOrderNo|1",
	})
	if err != nil {
		t.Fatalf("no se pudo registrar el webhook de la otra máquina: %v", err)
	}
	if !otherSource {
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
	if due[0].OrderID != "order-pronto" || due[0].MachineType != "snacks" || due[0].State != dispenser.LineSent {
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
