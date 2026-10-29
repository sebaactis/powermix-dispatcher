// Package notify aísla cómo llega una excepción hasta una persona.
//
// El transporte sigue abierto (S18): hoy escribe una línea de log estructurada,
// y más adelante se puede enchufar un emisor de Telegram o WhatsApp sin tocar el
// motor de órdenes.
package notify

import (
	"context"
	"log/slog"
)

// Event es una excepción que merece la atención de una persona.
type Event struct {
	Kind    string // por ejemplo "line_in_doubt", "line_failed", "partial_delivery"
	OrderID string
	LineID  string
	Detail  string
}

type Notifier interface {
	Notify(ctx context.Context, ev Event) error
}

// LogNotifier escribe el evento como una línea de log estructurada.
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
