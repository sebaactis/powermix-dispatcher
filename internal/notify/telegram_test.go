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
	return NewTelegram(logger, baseURL, testToken, "-100777", "production", WithTelegramBackoff(0))
}

func testEvent() Event {
	return Event{
		Kind:    KindPaymentMismatch,
		OrderID: "ord-1",
		Detail:  "el cobro 456 se pagó con 900 centavos y la orden vale 1000: no se despachó",
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
	for _, want := range []string{"[production]", "Pago con monto distinto", "Orden: ord-1", "900 centavos"} {
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

	if text := fake.messages()[0].body.Text; !strings.Contains(text, "Línea: line-9") {
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

func TestTelegramTruncatesLongMessages(t *testing.T) {
	server, fake := telegramServer(t)
	ev := testEvent()
	ev.Detail = strings.Repeat("ñ", 5000)

	_ = newTestTelegram(server.URL, &bytes.Buffer{}).Notify(context.Background(), ev)

	text := fake.messages()[0].body.Text
	if len(text) > telegramMaxText+len("…") {
		t.Fatalf("el texto mide %d bytes, el tope es %d", len(text), telegramMaxText)
	}
	if !strings.HasSuffix(text, "…") {
		t.Fatal("el texto recortado tiene que terminar en …")
	}
}
