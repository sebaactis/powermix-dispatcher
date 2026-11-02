package payments

import (
	"context"
	"log/slog"
)

type RefundRequest struct {
	OrderID     string
	LineID      string
	AmountCents int64
	Reason      string
}

type RefundResult struct {
	Accepted bool
	Moved    bool
	Note     string
}

type Refunder interface {
	Refund(ctx context.Context, req RefundRequest) (RefundResult, error)
}

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
