package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const TelegramURL = "https://api.telegram.org"

const (
	telegramAttempts       = 3
	telegramAttemptTimeout = 4 * time.Second
	telegramBackoff        = 500 * time.Millisecond

	telegramMaxText          = 4000
	telegramMaxResponseBytes = 64 << 10
	telegramMaxErrorMessage  = 200
)

var kindTitles = map[Kind]string{
	KindOrderInDoubt:    "Orden en duda",
	KindOrderFailed:     "Orden fallida",
	KindPaymentMismatch: "Pago con monto distinto",
	KindPaymentUnknown:  "Pago en estado desconocido",
}

type TelegramOption func(*TelegramNotifier)

func WithTelegramHTTPClient(httpClient *http.Client) TelegramOption {
	return func(t *TelegramNotifier) {
		if httpClient != nil {
			t.httpClient = httpClient
		}
	}
}

func WithTelegramBackoff(backoff time.Duration) TelegramOption {
	return func(t *TelegramNotifier) {
		t.backoff = backoff
	}
}

type TelegramNotifier struct {
	log        *LogNotifier
	logger     *slog.Logger
	baseURL    string
	token      string
	chatID     string
	env        string
	httpClient *http.Client
	backoff    time.Duration
}

func NewTelegram(logger *slog.Logger, baseURL, token, chatID, env string, opts ...TelegramOption) *TelegramNotifier {
	if baseURL == "" {
		baseURL = TelegramURL
	}

	t := &TelegramNotifier{
		log:        NewLog(logger),
		logger:     logger,
		baseURL:    strings.TrimRight(baseURL, "/"),
		token:      token,
		chatID:     chatID,
		env:        env,
		httpClient: &http.Client{Timeout: telegramAttemptTimeout},
		backoff:    telegramBackoff,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(t)
		}
	}

	return t
}

func (t *TelegramNotifier) Notify(ctx context.Context, ev Event) error {
	_ = t.log.Notify(ctx, ev)

	ctx = context.WithoutCancel(ctx)

	var err error
	for attempt := 1; attempt <= telegramAttempts; attempt++ {
		var retry bool
		retry, err = t.send(ctx, t.message(ev))
		if err == nil {
			return nil
		}
		if !retry || attempt == telegramAttempts {
			break
		}
		time.Sleep(t.backoff * time.Duration(attempt))
	}

	t.logger.ErrorContext(ctx, "no se pudo enviar el aviso a Telegram: queda sólo en el log",
		"kind", ev.Kind,
		"order_id", ev.OrderID,
		"error", err,
	)
	return nil
}

func (t *TelegramNotifier) message(ev Event) string {
	title, ok := kindTitles[ev.Kind]
	if !ok {
		title = string(ev.Kind)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "⚠️ compras-remotas [%s]\n%s\nOrden: %s\n", t.env, title, ev.OrderID)
	if ev.LineID != "" {
		fmt.Fprintf(&b, "Línea: %s\n", ev.LineID)
	}
	b.WriteString(ev.Detail)

	text := b.String()
	if len(text) > telegramMaxText {
		text = strings.ToValidUTF8(text[:telegramMaxText], "") + "…"
	}
	return text
}

type sendMessageBody struct {
	ChatID                string `json:"chat_id"`
	Text                  string `json:"text"`
	DisableWebPagePreview bool   `json:"disable_web_page_preview"`
}

type telegramResponse struct {
	OK          bool   `json:"ok"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
}

func (t *TelegramNotifier) send(ctx context.Context, text string) (bool, error) {
	raw, err := json.Marshal(sendMessageBody{ChatID: t.chatID, Text: text, DisableWebPagePreview: true})
	if err != nil {
		return false, fmt.Errorf("telegram: no se pudo serializar el mensaje: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/bot"+t.token+"/sendMessage", bytes.NewReader(raw))
	if err != nil {
		return false, t.redact(fmt.Errorf("telegram: no se pudo armar el request: %w", err))
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return true, t.redact(fmt.Errorf("telegram: sendMessage falló: %w", err))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, telegramMaxResponseBytes))
	if err != nil {
		return true, fmt.Errorf("telegram: no se pudo leer la respuesta: %w", err)
	}

	var parsed telegramResponse
	_ = json.Unmarshal(body, &parsed)

	if resp.StatusCode >= 200 && resp.StatusCode <= 299 && parsed.OK {
		return false, nil
	}

	description := parsed.Description
	if description == "" {
		description = string(body)
	}
	if len(description) > telegramMaxErrorMessage {
		description = description[:telegramMaxErrorMessage] + "…"
	}

	retry := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
	return retry, t.redact(fmt.Errorf("telegram: sendMessage rechazado (http=%d): %s", resp.StatusCode, description))
}

func (t *TelegramNotifier) redact(err error) error {
	if err == nil || t.token == "" {
		return err
	}
	msg := err.Error()
	if !strings.Contains(msg, t.token) {
		return err
	}
	return errors.New(strings.ReplaceAll(msg, t.token, "***"))
}
