package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"compras-remotas/internal/config"
	"compras-remotas/internal/order"
	"compras-remotas/internal/store"
)

const (
	batidosStatusFailed     = 0
	batidosStatusInProgress = 1
	batidosStatusCompleted  = 2

	shipmentFailed    = 0
	shipmentSucceeded = 1

	batidosFailureReason = "el proveedor informó que el canje no se completó"
	batidosAckMessage    = "操作成功"
)

type Receiver interface {
	CloseFromCallback(ctx context.Context, cb order.Callback) (duplicate bool, err error)
	ConfirmFromCallback(ctx context.Context, cb order.Callback) (duplicate bool, err error)
}

type CallbackSecrets struct {
	Batidos string
	Snacks  string
}

type Callbacks struct {
	receiver Receiver
	secrets  CallbackSecrets
	logger   *slog.Logger
}

type CallbacksOption func(*Callbacks)

func WithCallbacksLogger(logger *slog.Logger) CallbacksOption {
	return func(c *Callbacks) {
		if logger != nil {
			c.logger = logger
		}
	}
}

func NewCallbacks(receiver Receiver, secrets CallbackSecrets, opts ...CallbacksOption) *Callbacks {
	callbacks := &Callbacks{
		receiver: receiver,
		secrets:  secrets,
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	for _, opt := range opts {
		if opt != nil {
			opt(callbacks)
		}
	}

	return callbacks
}

func (c *Callbacks) Register(mux *http.ServeMux) {
	if c.receiver == nil {
		return
	}

	mux.HandleFunc("POST /callbacks/batidos/{secret}/{orderId}", c.batidosCallback)
	mux.HandleFunc("POST /callbacks/snacks/{secret}", c.snacksCallback)
}

type batidosCallbackPayload struct {
	DeviceID string `json:"deviceId"`
	Code     string `json:"code"`
	GoodsID  string `json:"goodsId"`
	Status   int    `json:"status"`
	OrderNo  string `json:"orderNo"`
}

type snacksCallbackPayload struct {
	OrderNo             string                     `json:"OrderNo"`
	PaymentOrderID      string                     `json:"PaymentOrderId"`
	Mid                 string                     `json:"Mid"`
	OrderProductResults []snacksOrderProductResult `json:"OrderProductResults"`
}

type snacksOrderProductResult struct {
	SubOrderNo          string `json:"SubOrderNo"`
	ThirdOrderNo        string `json:"ThirdOrderNo"`
	ShipmentStatus      int    `json:"ShipmentStatus"`
	ShipmentErrorReason string `json:"ShipmentErrorReason"`
}

type batidosAck struct {
	Code    int    `json:"code"`
	Success bool   `json:"success"`
	Data    bool   `json:"data"`
	Message string `json:"msg"`
}

type snacksAck struct {
	Success bool   `json:"success"`
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *Callbacks) batidosCallback(w http.ResponseWriter, r *http.Request) {
	ack := batidosAck{Code: http.StatusOK, Success: true, Data: true, Message: batidosAckMessage}

	if !c.secretMatches(r.PathValue("secret"), c.secrets.Batidos) {
		c.callbackNotFound(w)
		return
	}

	var payload batidosCallbackPayload
	if err := decodeCallback(w, r, &payload); err != nil {
		c.acknowledgeBrokenCallback(w, r, err, ack)
		return
	}

	state, known := batidosState(payload.Status)
	if !known {
		c.logger.WarnContext(r.Context(), "el callback de batidos informó un estado que no está en la tabla: se acusa recibo sin tocarlo",
			"status", payload.Status,
			"code", payload.Code,
			"order_no", payload.OrderNo,
		)

		writeJSON(w, http.StatusOK, ack)
		return
	}

	event := order.Callback{
		Source:      "batidos",
		DedupeKey:   fmt.Sprintf("%s|%s|%d", payload.OrderNo, payload.Code, payload.Status),
		OrderID:     r.PathValue("orderId"),
		Ref:         payload.Code,
		MachineType: string(config.MachineBatidos),
		State:       state,
		Reason:      batidosFailureReason,
	}

	c.writeCallbackResult(w, r, c.applyCallback(r.Context(), event, c.receiver.ConfirmFromCallback), ack)
}

func (c *Callbacks) snacksCallback(w http.ResponseWriter, r *http.Request) {
	ack := snacksAck{Success: true, Code: http.StatusOK, Message: "ok"}

	if !c.secretMatches(r.PathValue("secret"), c.secrets.Snacks) {
		c.callbackNotFound(w)
		return
	}

	var payload snacksCallbackPayload
	if err := decodeCallback(w, r, &payload); err != nil {
		c.acknowledgeBrokenCallback(w, r, err, ack)
		return
	}

	if len(payload.OrderProductResults) == 0 {
		c.logger.WarnContext(r.Context(), "el callback de snacks llegó sin resultados de suborden",
			"order_no", payload.OrderNo,
			"payment_order_id", payload.PaymentOrderID,
		)

		writeJSON(w, http.StatusOK, ack)
		return
	}

	failures := make([]error, 0, len(payload.OrderProductResults))

	for _, result := range payload.OrderProductResults {
		state, known := shipmentState(result.ShipmentStatus)
		if !known {
			c.logger.WarnContext(r.Context(), "el callback de snacks informó un estado de despacho que no está en la tabla: se acusa recibo sin tocarlo",
				"shipment_status", result.ShipmentStatus,
				"sub_order_no", result.SubOrderNo,
				"payment_order_id", payload.PaymentOrderID,
			)

			continue
		}

		event := order.Callback{
			Source:       "snacks",
			DedupeKey:    fmt.Sprintf("%s|%s|%d", payload.OrderNo, result.SubOrderNo, result.ShipmentStatus),
			OrderID:      payload.PaymentOrderID,
			Ref:          result.SubOrderNo,
			ThirdOrderNo: result.ThirdOrderNo,
			MachineType:  string(config.MachineSnacks),
			State:        state,
			Reason:       result.ShipmentErrorReason,
		}

		failures = append(failures, c.applyCallback(r.Context(), event, c.receiver.CloseFromCallback))
	}

	c.writeCallbackResult(w, r, errors.Join(failures...), ack)
}

func (c *Callbacks) applyCallback(ctx context.Context, event order.Callback, apply func(context.Context, order.Callback) (bool, error)) error {
	duplicate, err := apply(ctx, event)
	if err != nil {
		return err
	}

	if duplicate {
		c.logger.InfoContext(ctx, "el callback ya se había procesado: se ignora",
			"source", event.Source,
			"key", event.DedupeKey,
			"order_id", event.OrderID,
		)
	}

	return nil
}

func (c *Callbacks) writeCallbackResult(w http.ResponseWriter, r *http.Request, err error, ack any) {
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, ack)

	case errors.Is(err, order.ErrCallbackUnknownLine), errors.Is(err, store.ErrNotFound):
		c.logger.WarnContext(r.Context(), "el callback no corresponde a ninguna línea conocida: se acusa recibo igual para que no se reintente",
			"path", redactCallbackPath(r.URL.Path),
			"error", err,
		)

		writeJSON(w, http.StatusOK, ack)

	case errors.Is(err, order.ErrCallbackAmbiguous):
		c.logger.ErrorContext(r.Context(), "el callback corresponde a más de una línea: se acusa recibo igual para que no se reintente y no se cierra nada",
			"path", redactCallbackPath(r.URL.Path),
			"error", err,
		)

		writeJSON(w, http.StatusOK, ack)

	case errors.Is(err, order.ErrCallbackIncomplete):
		c.logger.ErrorContext(r.Context(), "el callback llegó incompleto: se acusa recibo igual para que no se reintente",
			"path", redactCallbackPath(r.URL.Path),
			"error", err,
		)

		writeJSON(w, http.StatusOK, ack)

	default:
		fail(w, r, c.logger, err)
	}
}

func (c *Callbacks) acknowledgeBrokenCallback(w http.ResponseWriter, r *http.Request, err error, ack any) {
	c.logger.ErrorContext(r.Context(), "el callback llegó con un cuerpo que no se puede leer: se acusa recibo igual para que no se reintente",
		"path", redactCallbackPath(r.URL.Path),
		"error", err,
	)

	writeJSON(w, http.StatusOK, ack)
}

func (c *Callbacks) secretMatches(given, expected string) bool {
	if expected == "" || given == "" {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(given), []byte(expected)) == 1
}

func (c *Callbacks) callbackNotFound(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "no existe")
}

func decodeCallback(w http.ResponseWriter, r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))

	if err := decoder.Decode(target); err != nil {
		return badRequest(fmt.Sprintf("el cuerpo del callback no es un JSON válido: %v", err))
	}

	return nil
}

func batidosState(status int) (order.CallbackState, bool) {
	switch status {
	case batidosStatusCompleted:
		return order.CallbackDelivered, true
	case batidosStatusInProgress:
		return order.CallbackProgress, true
	case batidosStatusFailed:
		return order.CallbackFailed, true
	default:
		return "", false
	}
}

func shipmentState(status int) (order.CallbackState, bool) {
	switch status {
	case shipmentSucceeded:
		return order.CallbackDelivered, true
	case shipmentFailed:
		return order.CallbackFailed, true
	default:
		return "", false
	}
}
