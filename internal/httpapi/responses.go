package httpapi

import (
	"time"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/machine"
	"compras-remotas/internal/store"
)

type orderResponse struct {
	ID          string            `json:"id"`
	KioskID     string            `json:"kiosk_id"`
	ComboID     string            `json:"combo_id"`
	AmountCents int64             `json:"amount_cents"`
	Currency    string            `json:"currency"`
	Status      store.OrderStatus `json:"status"`
	CreatedAt   *time.Time        `json:"created_at,omitempty"`
	PaidAt      *time.Time        `json:"paid_at,omitempty"`
	ClosedAt    *time.Time        `json:"closed_at,omitempty"`
	Lines       []lineResponse    `json:"lines"`
}

type lineResponse struct {
	ID          string              `json:"id"`
	Position    int                 `json:"position"`
	MachineType machine.Kind        `json:"machine_type"`
	SKU         string              `json:"sku"`
	State       dispenser.LineState `json:"state"`
	Attempts    int                 `json:"attempts"`
	LastError   string              `json:"last_error,omitempty"`
}

func newOrderResponse(loaded store.Order) orderResponse {
	response := orderResponse{
		ID:          loaded.ID,
		KioskID:     loaded.KioskID,
		ComboID:     loaded.ComboID,
		AmountCents: loaded.AmountCents,
		Currency:    loaded.Currency,
		Status:      loaded.Status,
		Lines:       make([]lineResponse, 0, len(loaded.Lines)),
	}

	if !loaded.CreatedAt.IsZero() {
		response.CreatedAt = &loaded.CreatedAt
	}

	if !loaded.PaidAt.IsZero() {
		response.PaidAt = &loaded.PaidAt
	}

	if !loaded.ClosedAt.IsZero() {
		response.ClosedAt = &loaded.ClosedAt
	}

	for _, line := range loaded.Lines {
		response.Lines = append(response.Lines, lineResponse{
			ID:          line.ID,
			Position:    line.Position,
			MachineType: line.Machine.Kind,
			SKU:         line.SKU,
			State:       line.State,
			Attempts:    line.Attempts,
			LastError:   line.LastError,
		})
	}

	return response
}
