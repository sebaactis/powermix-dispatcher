package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"compras-remotas/internal/order"
	"compras-remotas/internal/payments/mercadopago"
)

const mercadoPagoOrderTopic = "order"

type PaymentSyncer interface {
	SyncPayment(ctx context.Context, providerID string) error
}

type MercadoPagoSecrets struct {
	Callback string
	Webhook  string
}

type MercadoPagoWebhook struct {
	syncer  PaymentSyncer
	secrets MercadoPagoSecrets
	now     func() time.Time
	logger  *slog.Logger
}

type MercadoPagoOption func(*MercadoPagoWebhook)

func WithMercadoPagoLogger(logger *slog.Logger) MercadoPagoOption {
	return func(m *MercadoPagoWebhook) {
		if logger != nil {
			m.logger = logger
		}
	}
}

func WithMercadoPagoClock(now func() time.Time) MercadoPagoOption {
	return func(m *MercadoPagoWebhook) {
		if now != nil {
			m.now = now
		}
	}
}

func NewMercadoPagoWebhook(syncer PaymentSyncer, secrets MercadoPagoSecrets, opts ...MercadoPagoOption) *MercadoPagoWebhook {
	webhook := &MercadoPagoWebhook{
		syncer:  syncer,
		secrets: secrets,
		now:     time.Now,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	for _, opt := range opts {
		if opt != nil {
			opt(webhook)
		}
	}

	return webhook
}

func (m *MercadoPagoWebhook) Register(mux *http.ServeMux) {
	if m.syncer == nil {
		return
	}

	mux.HandleFunc("POST /callbacks/mercadopago/{secret}", m.receive)
}

func (m *MercadoPagoWebhook) receive(w http.ResponseWriter, r *http.Request) {
	given := r.PathValue("secret")
	if m.secrets.Callback == "" || given == "" || subtle.ConstantTimeCompare([]byte(given), []byte(m.secrets.Callback)) != 1 {
		writeError(w, http.StatusNotFound, "no existe")
		return
	}

	query := r.URL.Query()
	signedID := query.Get("data.id")
	providerID := signedID
	topic := query.Get("type")

	if providerID == "" {
		body := readWebhookBody(r)
		providerID = body.Data.ID
		if topic == "" {
			topic = body.Type
		}
	}

	if providerID == "" {
		writeError(w, http.StatusBadRequest, "falta data.id")
		return
	}

	err := mercadopago.VerifySignature(
		m.secrets.Webhook,
		r.Header.Get("X-Signature"),
		r.Header.Get("X-Request-Id"),
		signedID,
		m.now(),
	)
	if err != nil {
		m.logger.WarnContext(r.Context(), "webhook de Mercado Pago rechazado por la firma",
			"provider_id", providerID,
			"mp_request_id", r.Header.Get("X-Request-Id"),
			"error", err,
		)

		writeError(w, http.StatusUnauthorized, "firma inválida")
		return
	}

	if signedID == "" {
		m.logger.WarnContext(r.Context(), "webhook de Mercado Pago sin data.id en la query: se usa el del cuerpo",
			"provider_id", providerID,
			"mp_request_id", r.Header.Get("X-Request-Id"),
		)
	}

	if topic != mercadoPagoOrderTopic {
		m.logger.InfoContext(r.Context(), "webhook de Mercado Pago ignorado: no es del tópico order",
			"provider_id", providerID,
			"type", topic,
		)

		w.WriteHeader(http.StatusOK)
		return
	}

	err = m.syncer.SyncPayment(r.Context(), providerID)
	orderID, _ := order.OrderIDFromError(err)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusOK)
	case errors.Is(err, order.ErrUnknownCharge):
		attrs := appendOrderID([]any{
			"provider_id", providerID,
			"error", err,
		}, orderID)
		m.logger.WarnContext(r.Context(), "webhook de Mercado Pago de un cobro que no es nuestro", attrs...)

		w.WriteHeader(http.StatusOK)
	default:
		attrs := appendOrderID([]any{
			"provider_id", providerID,
			"error", err,
		}, orderID)
		m.logger.ErrorContext(r.Context(), "no se pudo procesar el webhook de Mercado Pago: se espera el reintento", attrs...)

		writeError(w, http.StatusInternalServerError, "no se pudo procesar el aviso")
	}
}

type webhookBody struct {
	Type string `json:"type"`
	Data struct {
		ID string `json:"id"`
	} `json:"data"`
}

func readWebhookBody(r *http.Request) webhookBody {
	var body webhookBody
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes)).Decode(&body); err != nil {
		return webhookBody{}
	}
	return body
}
