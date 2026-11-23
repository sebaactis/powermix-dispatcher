package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"compras-remotas/internal/httpx"
)

const TelegramURL = "https://api.telegram.org"

const (
	telegramAttempts       = 3
	telegramAttemptTimeout = 4 * time.Second
	telegramBackoff        = 500 * time.Millisecond

	telegramMaxDetail        = 700
	telegramMaxResponseBytes = 64 << 10
	telegramMaxErrorMessage  = 200
)

var argentina = time.FixedZone("ART", -3*60*60)

type kindStyle struct {
	icon   string
	title  string
	action string
}

var kindStyles = map[Kind]kindStyle{
	KindOrderFailed:     {"🔴", "Orden fallida", "El proveedor informó que no entregó: contactar al cliente."},
	KindOrderInDoubt:    {"🟡", "Orden en duda", "No se pudo confirmar la entrega: verificar en la máquina si salió el producto."},
	KindOrderPartial:    {"🟠", "Entrega parcial", "Contactar al cliente y compensar el monto de lo que falló."},
	KindDispatchPanic:   {"🔴", "Panic en despacho", "El despacho se interrumpió por un panic: verificar en la máquina si salió el producto."},
	KindPaymentMismatch: {"🟠", "Pago con monto distinto", "Revisar el cobro en Mercado Pago y devolver o completar la diferencia a mano."},
	KindPaymentUnknown:  {"🟣", "Pago en estado desconocido", "Revisar el cobro en el panel de Mercado Pago: no se despachó ni se canceló."},
}

type TelegramOption func(*TelegramNotifier)

func WithTelegramHTTPClient(httpClient *http.Client) TelegramOption {
	return func(t *TelegramNotifier) {
		if httpClient != nil {
			t.httpClient = httpClient
		}
	}
}

func WithTelegramKiosks(names map[string]string) TelegramOption {
	return func(t *TelegramNotifier) {
		t.kioskNames = names
	}
}

func WithTelegramClock(now func() time.Time) TelegramOption {
	return func(t *TelegramNotifier) {
		if now != nil {
			t.now = now
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
	kioskNames map[string]string
	now        func() time.Time
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
		now:        time.Now,
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
	style, ok := kindStyles[ev.Kind]
	if !ok {
		style = kindStyle{icon: "⚠️", title: string(ev.Kind)}
	}

	env := "🧪 " + t.env
	if t.env == "production" {
		env = "🚨 PRODUCCIÓN"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s <b>%s</b>\n", style.icon, esc(style.title))
	fmt.Fprintf(&b, "<i>%s · compras-remotas</i>\n\n", esc(env))

	if kiosk := t.kioskLabel(ev.KioskID); kiosk != "" {
		fmt.Fprintf(&b, "📍 %s\n", esc(kiosk))
	}
	fmt.Fprintf(&b, "🧾 Orden: <code>%s</code>\n", esc(ev.OrderID))
	if ev.LineID != "" {
		fmt.Fprintf(&b, "🔹 Línea: <code>%s</code>\n", esc(ev.LineID))
	}
	fmt.Fprintf(&b, "🕒 %s (hora Argentina)\n", t.now().In(argentina).Format("02/01/2006 15:04"))

	if detail := sentence(ev.Detail); detail != "" {
		fmt.Fprintf(&b, "\n<blockquote>%s</blockquote>\n", esc(detail))
	}

	if style.action != "" {
		fmt.Fprintf(&b, "\n👉 %s", esc(style.action))
	}

	return b.String()
}

func (t *TelegramNotifier) kioskLabel(id string) string {
	if id == "" {
		return ""
	}
	if name := t.kioskNames[id]; name != "" {
		return name + " (" + id + ")"
	}
	return id
}

func sentence(detail string) string {
	detail = strings.TrimSpace(detail)
	if len(detail) > telegramMaxDetail {
		detail = strings.ToValidUTF8(detail[:telegramMaxDetail], "") + "…"
	}
	if detail == "" {
		return ""
	}

	first, size := utf8.DecodeRuneInString(detail)
	detail = string(unicode.ToUpper(first)) + detail[size:]

	if !strings.HasSuffix(detail, ".") && !strings.HasSuffix(detail, "…") {
		detail += "."
	}
	return detail
}

func esc(s string) string {
	return html.EscapeString(s)
}

type sendMessageBody struct {
	ChatID                string `json:"chat_id"`
	Text                  string `json:"text"`
	ParseMode             string `json:"parse_mode"`
	DisableWebPagePreview bool   `json:"disable_web_page_preview"`
}

type telegramResponse struct {
	OK          bool   `json:"ok"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
}

func (t *TelegramNotifier) send(ctx context.Context, text string) (bool, error) {
	raw, err := json.Marshal(sendMessageBody{ChatID: t.chatID, Text: text, ParseMode: "HTML", DisableWebPagePreview: true})
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

	delivered := resp.StatusCode >= 200 && resp.StatusCode <= 299

	body, err := httpx.ReadLimited(resp.Body, telegramMaxResponseBytes)
	if errors.Is(err, httpx.ErrTooLarge) && delivered {
		return false, nil
	}
	if err != nil && !errors.Is(err, httpx.ErrTooLarge) {
		return true, fmt.Errorf("telegram: no se pudo leer la respuesta: %w", err)
	}

	var parsed telegramResponse
	_ = json.Unmarshal(body, &parsed)

	if delivered && parsed.OK {
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
