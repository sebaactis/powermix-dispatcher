package payments

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestMockRefunderNeverMovesMoney(t *testing.T) {
	var buf bytes.Buffer
	refunder := NewMockRefunder(slog.New(slog.NewTextHandler(&buf, nil)))

	result, err := refunder.Refund(context.Background(), RefundRequest{
		OrderID:     "order-1",
		LineID:      "line-1",
		AmountCents: 1500,
		Reason:      "el batido no respondió",
	})
	if err != nil {
		t.Fatalf("Refund: %v", err)
	}
	if result.Moved {
		t.Fatal("the mock reported money moved")
	}
	if !result.Accepted {
		t.Fatal("the mock must record the refund intention")
	}
	if result.Note == "" {
		t.Fatal("the mock must explain that no money moved")
	}

	logged := buf.String()
	for _, want := range []string{"order-1", "line-1", "1500", "el batido no respondió"} {
		if !strings.Contains(logged, want) {
			t.Fatalf("log %q is missing %q", logged, want)
		}
	}
}
