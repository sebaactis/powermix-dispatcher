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
	"testing"
	"time"
)

func TestTelegramRendersDispatchPanicAlert(t *testing.T) {
	style, ok := kindStyles[KindDispatchPanic]
	if !ok || style.icon == "" || style.title == "" || style.action == "" {
		t.Fatalf("KindDispatchPanic missing style (icon/title/action): %+v", style)
	}

	var (
		mu    sync.Mutex
		texts []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body sendMessageBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode sendMessage body: %v", err)
		}
		mu.Lock()
		texts = append(texts, body.Text)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))
	notifier := NewTelegram(logger, server.URL, "123456:panic-test-token", "-100777", "production",
		WithTelegramBackoff(0),
		WithTelegramKiosks(map[string]string{"kiosk-1": "Mueble Centro"}),
		WithTelegramClock(func() time.Time { return time.Date(2026, 11, 21, 21, 0, 0, 0, time.UTC) }),
	)

	ev := Event{
		Kind:    KindDispatchPanic,
		OrderID: "ord-panic-1",
		KioskID: "kiosk-1",
		LineID:  "line-1",
		Detail:  "el despacho de batidos/BATIDO-1 sufrió un panic y la línea quedó en duda: verificar si salió el producto",
	}

	if err := notifier.Notify(context.Background(), ev); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(texts) != 1 {
		t.Fatalf("got %d Telegram messages, want 1", len(texts))
	}

	text := texts[0]
	for _, want := range []string{
		"<b>" + style.title + "</b>",
		"📍 Mueble Centro (kiosk-1)",
		"🧾 Orden: <code>ord-panic-1</code>",
		"🔹 Línea: <code>line-1</code>",
		"<blockquote>El despacho de batidos/BATIDO-1 sufrió un panic y la línea quedó en duda: verificar si salió el producto.</blockquote>",
		"👉 " + style.action,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("rendered text %q missing %q", text, want)
		}
	}
	if strings.Contains(text, "⚠️ <b>dispatch_panic</b>") {
		t.Fatalf("alert used generic fallback style: %q", text)
	}
	if !strings.Contains(logs.String(), "dispatch_panic") || !strings.Contains(logs.String(), "order_id=ord-panic-1") {
		t.Fatalf("log missing dispatch_panic/order_id correlation: %s", logs.String())
	}
}
