package httpapi

import (
	"context"
	"crypto/subtle"
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
	providerID := query.Get("data.id")
	topic := query.Get("type")

	if providerID == "" {
		writeError(w, http.StatusBadRequest, "falta data.id")
		return
	}

	err := mercadopago.VerifySignature(
		m.secrets.Webhook,
		r.Header.Get("X-Signature"),
		r.Header.Get("X-Request-Id"),
		providerID,
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

	if topic != mercadoPagoOrderTopic {
		m.logger.InfoContext(r.Context(), "webhook de Mercado Pago ignorado: no es del tópico order",
			"provider_id", providerID,
			"type", topic,
		)

		w.WriteHeader(http.StatusOK)
		return
	}

	err = m.syncer.SyncPayment(r.Context(), providerID)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusOK)
	case errors.Is(err, order.ErrUnknownCharge):
		m.logger.WarnContext(r.Context(), "webhook de Mercado Pago de un cobro que no es nuestro",
			"provider_id", providerID,
			"error", err,
		)

		w.WriteHeader(http.StatusOK)
	default:
		m.logger.ErrorContext(r.Context(), "no se pudo procesar el webhook de Mercado Pago: se espera el reintento",
			"provider_id", providerID,
			"error", err,
		)

		writeError(w, http.StatusInternalServerError, "no se pudo procesar el aviso")
	}
}
