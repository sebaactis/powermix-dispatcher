package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

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

func WithCallbacks(receiver Receiver, secrets CallbackSecrets) Option {
	return func(h *Handler) {
		if receiver == nil {
			return
		}

		h.callbacks = receiver
		h.callbackSecrets = secrets
	}
}

func BatidosCallbackURL(baseURL, secret, orderID string) string {
	if baseURL == "" || secret == "" || orderID == "" {
		return ""
	}

	return strings.TrimSuffix(baseURL, "/") +
		"/callbacks/batidos/" + url.PathEscape(secret) + "/" + url.PathEscape(orderID)
}

func redactCallbackPath(path string) string {
	const prefix = "/callbacks/"

	if !strings.HasPrefix(path, prefix) {
		return path
	}

	source, _, found := strings.Cut(path[len(prefix):], "/")
	if !found || source == "" {
		return path
	}

	return prefix + source + "/***"
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

func (h *Handler) batidosCallback(w http.ResponseWriter, r *http.Request) {
	ack := batidosAck{Code: http.StatusOK, Success: true, Data: true, Message: batidosAckMessage}

	if !h.secretMatches(r.PathValue("secret"), h.callbackSecrets.Batidos) {
		h.callbackNotFound(w)
		return
	}

	var payload batidosCallbackPayload
	if err := decodeCallback(w, r, &payload); err != nil {
		h.acknowledgeBrokenCallback(w, r, err, ack)
		return
	}

	state, known := batidosState(payload.Status)
	if !known {
		h.logger.WarnContext(r.Context(), "el callback de batidos informó un estado que no está en la tabla: se acusa recibo sin tocarlo",
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

	h.writeCallbackResult(w, r, h.applyCallback(r.Context(), event, h.callbacks.ConfirmFromCallback), ack)
}

func (h *Handler) snacksCallback(w http.ResponseWriter, r *http.Request) {
	ack := snacksAck{Success: true, Code: http.StatusOK, Message: "ok"}

	if !h.secretMatches(r.PathValue("secret"), h.callbackSecrets.Snacks) {
		h.callbackNotFound(w)
		return
	}

	var payload snacksCallbackPayload
	if err := decodeCallback(w, r, &payload); err != nil {
		h.acknowledgeBrokenCallback(w, r, err, ack)
		return
	}

	if len(payload.OrderProductResults) == 0 {
		h.logger.WarnContext(r.Context(), "el callback de snacks llegó sin resultados de suborden",
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
			h.logger.WarnContext(r.Context(), "el callback de snacks informó un estado de despacho que no está en la tabla: se acusa recibo sin tocarlo",
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

		failures = append(failures, h.applyCallback(r.Context(), event, h.callbacks.CloseFromCallback))
	}

	h.writeCallbackResult(w, r, errors.Join(failures...), ack)
}

func (h *Handler) applyCallback(ctx context.Context, event order.Callback, apply func(context.Context, order.Callback) (bool, error)) error {
	duplicate, err := apply(ctx, event)
	if err != nil {
		return err
	}

	if duplicate {
		h.logger.InfoContext(ctx, "el callback ya se había procesado: se ignora",
			"source", event.Source,
			"key", event.DedupeKey,
			"order_id", event.OrderID,
		)
	}

	return nil
}

func (h *Handler) writeCallbackResult(w http.ResponseWriter, r *http.Request, err error, ack any) {
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, ack)

	case errors.Is(err, order.ErrCallbackUnknownLine), errors.Is(err, store.ErrNotFound):
		h.logger.WarnContext(r.Context(), "el callback no corresponde a ninguna línea conocida: se acusa recibo igual para que no se reintente",
			"path", redactCallbackPath(r.URL.Path),
			"error", err,
		)

		writeJSON(w, http.StatusOK, ack)

	case errors.Is(err, order.ErrCallbackAmbiguous):
		h.logger.ErrorContext(r.Context(), "el callback corresponde a más de una línea: se acusa recibo igual para que no se reintente y no se cierra nada",
			"path", redactCallbackPath(r.URL.Path),
			"error", err,
		)

		writeJSON(w, http.StatusOK, ack)

	case errors.Is(err, order.ErrCallbackIncomplete):
		h.logger.ErrorContext(r.Context(), "el callback llegó incompleto: se acusa recibo igual para que no se reintente",
			"path", redactCallbackPath(r.URL.Path),
			"error", err,
		)

		writeJSON(w, http.StatusOK, ack)

	default:
		h.fail(w, r, err)
	}
}

func (h *Handler) acknowledgeBrokenCallback(w http.ResponseWriter, r *http.Request, err error, ack any) {
	h.logger.ErrorContext(r.Context(), "el callback llegó con un cuerpo que no se puede leer: se acusa recibo igual para que no se reintente",
		"path", redactCallbackPath(r.URL.Path),
		"error", err,
	)

	writeJSON(w, http.StatusOK, ack)
}

func (h *Handler) secretMatches(given, expected string) bool {
	if expected == "" || given == "" {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(given), []byte(expected)) == 1
}

func (h *Handler) callbackNotFound(w http.ResponseWriter) {
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
