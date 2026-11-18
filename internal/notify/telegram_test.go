package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testToken = "123456:secret-token"

type sentMessage struct {
	path string
	body sendMessageBody
}

type fakeTelegram struct {
	mu    sync.Mutex
	sent  []sentMessage
	calls atomic.Int32
}

func (f *fakeTelegram) messages() []sentMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sentMessage(nil), f.sent...)
}

func telegramServer(t *testing.T, statuses ...int) (*httptest.Server, *fakeTelegram) {
	t.Helper()

	fake := &fakeTelegram{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(fake.calls.Add(1))

		var body sendMessageBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("cuerpo ilegible: %v", err)
		}
		fake.mu.Lock()
		fake.sent = append(fake.sent, sentMessage{path: r.URL.Path, body: body})
		fake.mu.Unlock()

		status := http.StatusOK
		if n <= len(statuses) {
			status = statuses[n-1]
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":false,"description":"falló"}`))
	}))
	t.Cleanup(server.Close)

	return server, fake
}

func newTestTelegram(baseURL string, logs *bytes.Buffer) *TelegramNotifier {
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelInfo}))
	return NewTelegram(logger, baseURL, testToken, "-100777", "production",
		WithTelegramBackoff(0),
		WithTelegramKiosks(map[string]string{"kiosk-1": "Mueble Centro"}),
		WithTelegramClock(func() time.Time { return time.Date(2026, 11, 18, 22, 31, 0, 0, time.UTC) }),
	)
}

func testEvent() Event {
	return Event{
		Kind:    KindPaymentMismatch,
		OrderID: "ord-1",
		KioskID: "kiosk-1",
		Detail:  "el cobro 456 se pagó con $9,00 y la orden vale $10,00: no se despachó",
	}
}

func TestTelegramSendsTheAlertAndLogsIt(t *testing.T) {
	server, fake := telegramServer(t)
	var logs bytes.Buffer

	if err := newTestTelegram(server.URL, &logs).Notify(context.Background(), testEvent()); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	sent := fake.messages()
	if len(sent) != 1 {
		t.Fatalf("se enviaron %d mensajes, quería 1", len(sent))
	}

	msg := sent[0]
	if msg.path != "/bot"+testToken+"/sendMessage" {
		t.Fatalf("path = %q", msg.path)
	}
	if msg.body.ChatID != "-100777" {
		t.Fatalf("chat_id = %q", msg.body.ChatID)
	}
	if msg.body.ParseMode != "HTML" {
		t.Fatalf("parse_mode = %q, quería HTML", msg.body.ParseMode)
	}
	for _, want := range []string{
		"🟠 <b>Pago con monto distinto</b>",
		"<i>🚨 PRODUCCIÓN · compras-remotas</i>",
		"📍 Mueble Centro (kiosk-1)",
		"🧾 Orden: <code>ord-1</code>",
		"🕒 18/11/2026 19:31 (hora Argentina)",
		"<blockquote>El cobro 456 se pagó con $9,00 y la orden vale $10,00: no se despachó.</blockquote>",
		"👉 Revisar el cobro en Mercado Pago",
	} {
		if !strings.Contains(msg.body.Text, want) {
			t.Fatalf("el texto %q no menciona %q", msg.body.Text, want)
		}
	}

	if !strings.Contains(logs.String(), "excepción en una orden") {
		t.Fatalf("el aviso no quedó en el log: %s", logs.String())
	}
}

func TestTelegramIncludesTheLineWhenThereIsOne(t *testing.T) {
	server, fake := telegramServer(t)
	ev := testEvent()
	ev.LineID = "line-9"

	_ = newTestTelegram(server.URL, &bytes.Buffer{}).Notify(context.Background(), ev)

	if text := fake.messages()[0].body.Text; !strings.Contains(text, "🔹 Línea: <code>line-9</code>") {
		t.Fatalf("el texto no menciona la línea: %q", text)
	}
}

func TestTelegramRetriesTransientFailures(t *testing.T) {
	server, fake := telegramServer(t, http.StatusTooManyRequests, http.StatusBadGateway)
	var logs bytes.Buffer

	if err := newTestTelegram(server.URL, &logs).Notify(context.Background(), testEvent()); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	if fake.calls.Load() != 3 {
		t.Fatalf("hubo %d intentos, quería 3 (dos fallas y un éxito)", fake.calls.Load())
	}
	if strings.Contains(logs.String(), "level=ERROR") {
		t.Fatalf("no debería haber error si el tercer intento salió: %s", logs.String())
	}
}

func TestTelegramGivesUpWithoutFailingTheCaller(t *testing.T) {
	server, fake := telegramServer(t, http.StatusInternalServerError, http.StatusInternalServerError, http.StatusInternalServerError, http.StatusInternalServerError)
	var logs bytes.Buffer

	if err := newTestTelegram(server.URL, &logs).Notify(context.Background(), testEvent()); err != nil {
		t.Fatalf("Notify tiene que devolver nil aunque Telegram falle: %v", err)
	}

	if fake.calls.Load() != telegramAttempts {
		t.Fatalf("hubo %d intentos, quería %d", fake.calls.Load(), telegramAttempts)
	}
	if !strings.Contains(logs.String(), "level=ERROR") || !strings.Contains(logs.String(), "queda sólo en el log") {
		t.Fatalf("faltó el ERROR que avisa que no salió: %s", logs.String())
	}
}

func TestTelegramDoesNotRetryPermanentRejections(t *testing.T) {
	server, fake := telegramServer(t, http.StatusBadRequest)

	_ = newTestTelegram(server.URL, &bytes.Buffer{}).Notify(context.Background(), testEvent())

	if fake.calls.Load() != 1 {
		t.Fatalf("hubo %d intentos; un 400 (chat inexistente) no se arregla reintentando", fake.calls.Load())
	}
}

func TestTelegramTreatsOkFalseAsFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"description":"algo raro"}`))
	}))
	t.Cleanup(server.Close)
	var logs bytes.Buffer

	_ = newTestTelegram(server.URL, &logs).Notify(context.Background(), testEvent())

	if !strings.Contains(logs.String(), "algo raro") {
		t.Fatalf("un 200 con ok=false tiene que contar como falla: %s", logs.String())
	}
}

func TestTelegramNeverLogsTheToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	baseURL := server.URL
	server.Close()
	var logs bytes.Buffer

	_ = newTestTelegram(baseURL, &logs).Notify(context.Background(), testEvent())

	if !strings.Contains(logs.String(), "level=ERROR") {
		t.Fatalf("con el servidor caído tenía que loguear un ERROR: %s", logs.String())
	}
	if strings.Contains(logs.String(), "secret-token") {
		t.Fatalf("el log filtra el token: %s", logs.String())
	}
}

func TestTelegramSendsEvenIfTheCallerContextIsCancelled(t *testing.T) {
	server, fake := telegramServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_ = newTestTelegram(server.URL, &bytes.Buffer{}).Notify(ctx, testEvent())

	if len(fake.messages()) != 1 {
		t.Fatal("el aviso tiene que salir aunque el request que lo disparó se haya cortado")
	}
}

func TestTelegramTruncatesLongDetails(t *testing.T) {
	server, fake := telegramServer(t)
	ev := testEvent()
	ev.Detail = strings.Repeat("ñ", 5000)

	_ = newTestTelegram(server.URL, &bytes.Buffer{}).Notify(context.Background(), ev)

	text := fake.messages()[0].body.Text
	if len(text) > 4096 {
		t.Fatalf("el texto mide %d bytes, Telegram acepta hasta 4096", len(text))
	}
	if !strings.Contains(text, "…</blockquote>") {
		t.Fatal("el detalle recortado tiene que terminar en … dentro de la cita")
	}
}

func TestTelegramEscapesHTML(t *testing.T) {
	server, fake := telegramServer(t)
	ev := testEvent()
	ev.OrderID = "ord<1>"
	ev.Detail = "falló <b>todo</b> & más"

	_ = newTestTelegram(server.URL, &bytes.Buffer{}).Notify(context.Background(), ev)

	text := fake.messages()[0].body.Text
	if strings.Contains(text, "<b>todo</b>") || strings.Contains(text, "ord<1>") {
		t.Fatalf("el contenido del evento no se escapó: %q", text)
	}
	for _, want := range []string{"ord&lt;1&gt;", "&lt;b&gt;todo&lt;/b&gt; &amp; más"} {
		if !strings.Contains(text, want) {
			t.Fatalf("el texto %q no tiene %q", text, want)
		}
	}
}

func TestTelegramMarksSandboxAndUnknownKiosks(t *testing.T) {
	server, fake := telegramServer(t)
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	notifier := NewTelegram(logger, server.URL, testToken, "-1", "sandbox", WithTelegramBackoff(0))
	ev := testEvent()
	ev.Kind = KindOrderInDoubt
	ev.KioskID = "kiosk-x"

	_ = notifier.Notify(context.Background(), ev)

	text := fake.messages()[0].body.Text
	for _, want := range []string{"🟡 <b>Orden en duda</b>", "<i>🧪 sandbox · compras-remotas</i>", "📍 kiosk-x\n", "👉 No se pudo confirmar la entrega"} {
		if !strings.Contains(text, want) {
			t.Fatalf("el texto %q no tiene %q", text, want)
		}
	}
}

func TestTelegramOmitsTheKioskWhenTheEventHasNone(t *testing.T) {
	server, fake := telegramServer(t)
	ev := testEvent()
	ev.KioskID = ""

	_ = newTestTelegram(server.URL, &bytes.Buffer{}).Notify(context.Background(), ev)

	if text := fake.messages()[0].body.Text; strings.Contains(text, "📍") {
		t.Fatalf("sin mueble no tiene que aparecer la línea del mueble: %q", text)
	}
}

func TestEveryKindHasAStyle(t *testing.T) {
	for _, kind := range []Kind{KindOrderFailed, KindOrderInDoubt, KindPaymentMismatch, KindPaymentUnknown} {
		style, ok := kindStyles[kind]
		if !ok || style.icon == "" || style.title == "" || style.action == "" {
			t.Fatalf("%s no tiene icono, título y qué hacer: %+v", kind, style)
		}
	}
}

func TestPesos(t *testing.T) {
	cases := map[int64]string{
		0:         "$0,00",
		50:        "$0,50",
		100:       "$1,00",
		150050:    "$1.500,50",
		123456789: "$1.234.567,89",
		-150050:   "-$1.500,50",
		100000000: "$1.000.000,00",
	}
	for cents, want := range cases {
		if got := Pesos(cents); got != want {
			t.Fatalf("Pesos(%d) = %q, quería %q", cents, got, want)
		}
	}
}
