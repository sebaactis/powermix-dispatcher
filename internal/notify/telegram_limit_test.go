package notify

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTelegramTreatsAnOversizedSuccessAsDelivered(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"ok":true,"result":{"text":"` + strings.Repeat("x", telegramMaxResponseBytes) + `"}}`))
	}))
	t.Cleanup(server.Close)
	var logs bytes.Buffer

	if err := newTestTelegram(server.URL, &logs).Notify(context.Background(), testEvent()); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if strings.Contains(logs.String(), "rechazado") || strings.Contains(logs.String(), "level=ERROR") {
		t.Fatalf("un 2xx con respuesta larga no puede contarse como envío fallido: %s", logs.String())
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("Telegram recibió %d envíos, se esperaba 1 (un 2xx largo es un envío hecho)", got)
	}
}

func TestTelegramStillFailsAnOversizedRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(strings.Repeat("y", telegramMaxResponseBytes+10)))
	}))
	t.Cleanup(server.Close)
	var logs bytes.Buffer

	_ = newTestTelegram(server.URL, &logs).Notify(context.Background(), testEvent())

	if !strings.Contains(logs.String(), "http=400") {
		t.Fatalf("un 400 largo tiene que seguir contando como rechazo: %s", logs.String())
	}
}
