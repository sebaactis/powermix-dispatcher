package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"compras-remotas/internal/catalog"
	"compras-remotas/internal/order"
	"compras-remotas/internal/store"
)

type apiError struct {
	status  int
	message string
}

func (e apiError) Error() string {
	return e.message
}

func badRequest(message string) apiError {
	return apiError{status: http.StatusBadRequest, message: message}
}

func appendOrderID(attrs []any, orderID string) []any {
	if orderID == "" {
		return attrs
	}
	return append(attrs, "order_id", orderID)
}

func fail(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	failWithOrder(w, r, logger, err, "")
}

func failWithOrder(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error, orderID string) {
	var apiErr apiError
	if errors.As(err, &apiErr) {
		writeError(w, apiErr.status, apiErr.message)
		return
	}

	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, catalog.ErrNotFound.Error())
		return
	}

	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "la orden no existe")
		return
	}

	if errors.Is(err, order.ErrNotPayable) {
		writeError(w, http.StatusConflict, order.ErrNotPayable.Error())
		return
	}

	if errors.Is(err, order.ErrClientKeyConflict) {
		writeError(w, http.StatusConflict, order.ErrClientKeyConflict.Error())
		return
	}

	if orderID == "" {
		orderID = r.PathValue("id")
	}
	if orderID == "" {
		orderID = r.PathValue("orderId")
	}

	logError := func(msg string) {
		attrs := appendOrderID([]any{
			"method", r.Method,
			"path", redactCallbackPath(r.URL.Path),
			"error", err,
		}, orderID)
		logger.ErrorContext(r.Context(), msg, attrs...)
	}

	if errors.Is(err, order.ErrAvailabilityUnavailable) {
		logError("no se pudo verificar la disponibilidad de las máquinas")

		writeError(w, http.StatusBadGateway, order.ErrAvailabilityUnavailable.Error())
		return
	}

	if errors.Is(err, order.ErrQRUnavailable) {
		logError("el proveedor de pagos no pudo crear el QR")

		writeError(w, http.StatusBadGateway, order.ErrQRUnavailable.Error())
		return
	}

	var notSellable *order.NotSellableError
	if errors.As(err, &notSellable) {
		reasons := notSellable.Reasons
		if reasons == nil {
			reasons = []string{}
		}

		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   order.ErrNotSellable.Error(),
			"reasons": reasons,
		})
		return
	}

	logError("no se pudo atender el pedido HTTP")

	writeError(w, http.StatusInternalServerError, "no se pudo completar la operación")
}
