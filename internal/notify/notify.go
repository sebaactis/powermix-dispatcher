package notify

import (
	"context"
	"log/slog"
)

type Event struct {
	Kind    string
	OrderID string
	LineID  string
	Detail  string
}

type Notifier interface {
	Notify(ctx context.Context, ev Event) error
}

type LogNotifier struct {
	logger *slog.Logger
}

func NewLog(logger *slog.Logger) *LogNotifier {
	return &LogNotifier{logger: logger}
}

func (l *LogNotifier) Notify(ctx context.Context, ev Event) error {
	l.logger.WarnContext(ctx, "excepción en una orden",
		"kind", ev.Kind,
		"order_id", ev.OrderID,
		"line_id", ev.LineID,
		"detail", ev.Detail,
	)
	return nil
}
