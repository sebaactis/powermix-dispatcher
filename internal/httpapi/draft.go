package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"compras-remotas/internal/catalog"
	"compras-remotas/internal/config"
	"compras-remotas/internal/machine"
	"compras-remotas/internal/order"
)

type createOrderRequest struct {
	KioskID   string `json:"kiosk_id"`
	ComboID   string `json:"combo_id"`
	ClientKey string `json:"client_key"`
}

func decodeCreateOrder(w http.ResponseWriter, r *http.Request) (createOrderRequest, error) {
	var request createOrderRequest

	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&request); err != nil {
		return createOrderRequest{}, badRequest(fmt.Sprintf("el cuerpo del pedido no es un JSON válido: %v", err))
	}

	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return createOrderRequest{}, badRequest("el cuerpo del pedido debe ser un único objeto JSON")
	}

	if strings.TrimSpace(request.ComboID) == "" {
		return createOrderRequest{}, badRequest("hace falta indicar el combo (combo_id)")
	}

	return request, nil
}

func (h *Handler) resolveKiosk(requested string) (config.Kiosk, error) {
	id := strings.TrimSpace(requested)

	if id == "" {
		if h.onlyKiosk == "" {
			return config.Kiosk{}, badRequest("hace falta indicar el mueble (kiosk_id): hay más de uno configurado")
		}

		id = h.onlyKiosk
	}

	kiosk, found := h.kiosks[id]
	if !found {
		return config.Kiosk{}, apiError{
			status:  http.StatusNotFound,
			message: fmt.Sprintf("el mueble %q no está configurado", id),
		}
	}

	return kiosk, nil
}

func (h *Handler) buildDraft(kiosk config.Kiosk, combo catalog.Combo, clientKey string) (order.Draft, error) {
	draft := order.Draft{
		KioskID:     kiosk.ID,
		ComboID:     combo.ID,
		ClientKey:   strings.TrimSpace(clientKey),
		AmountCents: combo.PriceCents,
		Currency:    combo.Currency,
		Items:       make([]order.Item, 0, len(combo.Items)),
	}

	for _, item := range combo.Items {
		machineConfig, found := kiosk.Machine(item.MachineType)
		if !found {
			return order.Draft{}, apiError{
				status: http.StatusConflict,
				message: fmt.Sprintf(
					"el combo %q necesita una máquina %q y el mueble %q no la tiene",
					combo.ID, item.MachineType, kiosk.ID,
				),
			}
		}

		draft.Items = append(draft.Items, order.Item{
			SKU: item.SKU,
			Machine: machine.Ref{
				KioskID:      kiosk.ID,
				Kind:         machineConfig.Type,
				Name:         machineConfig.Name,
				DeviceID:     machineConfig.DeviceID,
				DeviceNo:     machineConfig.DeviceNo,
				DeviceTypeID: machineConfig.DeviceTypeID,
				MID:          machineConfig.MID,
			},
		})
	}

	return draft, nil
}
