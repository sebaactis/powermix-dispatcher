package notify

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestLogNotifierRecordsTheException(t *testing.T) {
	var buf bytes.Buffer
	notifier := NewLog(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))

	err := notifier.Notify(context.Background(), Event{
		Kind:    "line_in_doubt",
		OrderID: "order-1",
		LineID:  "line-1",
		Detail:  "sin respuesta del proveedor después de 10 minutos",
	})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}

	logged := buf.String()
	for _, want := range []string{"line_in_doubt", "order-1", "line-1", "sin respuesta"} {
		if !strings.Contains(logged, want) {
			t.Fatalf("log %q is missing %q", logged, want)
		}
	}
}
