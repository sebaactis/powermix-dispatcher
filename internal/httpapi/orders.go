package httpapi

import (
	"errors"
	"net/http"

	"compras-remotas/internal/store"
)

func (h *Handler) createOrder(w http.ResponseWriter, r *http.Request) {
	request, err := decodeCreateOrder(w, r)
	if err != nil {
		fail(w, r, h.logger, err)
		return
	}

	kiosk, err := h.resolveKiosk(request.KioskID)
	if err != nil {
		fail(w, r, h.logger, err)
		return
	}

	combo, err := h.catalog.Combo(r.Context(), request.ComboID)
	if err != nil {
		fail(w, r, h.logger, err)
		return
	}

	draft, err := h.buildDraft(kiosk, combo, request.ClientKey)
	if err != nil {
		fail(w, r, h.logger, err)
		return
	}

	placed, err := h.creator.CreateOrder(r.Context(), draft)
	if err != nil {
		fail(w, r, h.logger, err)
		return
	}

	response := newOrderResponse(placed)

	if h.qrRequester != nil {
		intent, err := h.qrRequester.RequestQR(r.Context(), placed.ID, kiosk.MPPosID)
		if err != nil {
			fail(w, r, h.logger, err)
			return
		}

		response.Payment = newPaymentResponse(intent)
	}

	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) readOrder(w http.ResponseWriter, r *http.Request) {
	loaded, err := h.orders.LoadOrder(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, r, h.logger, err)
		return
	}

	response := newOrderResponse(loaded)

	if h.payments != nil {
		intent, err := h.payments.PaymentIntentByOrder(r.Context(), loaded.ID)
		switch {
		case err == nil:
			response.Payment = newPaymentResponse(intent)
		case !errors.Is(err, store.ErrNotFound):
			fail(w, r, h.logger, err)
			return
		}
	}

	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) payOrder(w http.ResponseWriter, r *http.Request) {
	if !h.simulated {
		writeError(w, http.StatusForbidden, "el pago simulado no está disponible en producción")
		return
	}

	orderID := r.PathValue("id")

	if err := h.paidMarker.MarkPaid(r.Context(), orderID, h.now()); err != nil {
		loaded, readErr := h.orders.LoadOrder(r.Context(), orderID)
		if readErr != nil {
			fail(w, r, h.logger, errors.Join(err, readErr))
			return
		}

		if loaded.Status == store.OrderPendingPayment {
			fail(w, r, h.logger, err)
			return
		}

		h.logger.WarnContext(r.Context(), "el pago quedó registrado con líneas con problemas",
			"order_id", orderID,
			"status", loaded.Status,
			"error", err,
		)

		writeJSON(w, http.StatusOK, newOrderResponse(loaded))
		return
	}

	loaded, err := h.orders.LoadOrder(r.Context(), orderID)
	if err != nil {
		fail(w, r, h.logger, err)
		return
	}

	writeJSON(w, http.StatusOK, newOrderResponse(loaded))
}
