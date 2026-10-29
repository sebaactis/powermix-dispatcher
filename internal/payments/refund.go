// Package payments aísla el lado del dinero detrás de un puerto.
//
// El proveedor de QR todavía no está elegido y la política de reembolso sigue
// abierta (S17). Las dos cosas viven detrás de esta interfaz para que el motor
// de órdenes se pueda construir y probar sin ninguna de ellas: el mock registra
// la intención y no mueve nada.
package payments

import (
	"context"
	"log/slog"
)

// RefundRequest es una línea a devolver, nunca una orden completa: el combo
// puede quedar entregado a medias.
type RefundRequest struct {
	OrderID     string
	LineID      string
	AmountCents int64
	Reason      string
}

// RefundResult informa por separado qué se aceptó y, explícitamente, si el
// dinero se movió.
type RefundResult struct {
	Accepted bool
	Moved    bool
	Note     string
}

type Refunder interface {
	Refund(ctx context.Context, req RefundRequest) (RefundResult, error)
}

// MockRefunder registra la intención y nunca mueve dinero. Mientras no haya
// proveedor elegido y política definida, esta es la única implementación
// honesta.
type MockRefunder struct {
	logger *slog.Logger
}

func NewMockRefunder(logger *slog.Logger) *MockRefunder {
	return &MockRefunder{logger: logger}
}

func (m *MockRefunder) Refund(ctx context.Context, req RefundRequest) (RefundResult, error) {
	m.logger.InfoContext(ctx, "devolución registrada: mock, no se movió dinero",
		"order_id", req.OrderID,
		"line_id", req.LineID,
		"amount_cents", req.AmountCents,
		"reason", req.Reason,
	)

	return RefundResult{
		Accepted: true,
		Moved:    false,
		Note:     "mock: la devolución quedó registrada, pero no se movió dinero",
	}, nil
}
