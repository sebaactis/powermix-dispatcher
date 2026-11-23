package sweep_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"compras-remotas/internal/logging"
	"compras-remotas/internal/sweep"
)

func TestRunSurvivesAPanicInAStage(t *testing.T) {
	sweeper := newFakeSweeper()
	sweeper.hook = func(_ context.Context, call int) error {
		if call == 1 {
			panic("dato roto en la base")
		}
		return nil
	}

	var logs bytes.Buffer
	cancel, done := startRunner(t, sweeper, logging.New(&logs, slog.LevelInfo), sweep.WithInterval(5*time.Millisecond))

	receive(t, sweeper.limits)
	receive(t, sweeper.limits)

	cancel()
	if err := receive(t, done); err != nil {
		t.Fatalf("Run devolvió %v, se esperaba nil", err)
	}

	var found bool
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		msg, _ := rec["msg"].(string)
		if rec["level"] == "ERROR" && strings.Contains(strings.ToLower(msg), "panic en el barrido de reconciliación") &&
			rec["panic"] == "dato roto en la base" &&
			strings.Contains(rec["stack"].(string), "goroutine") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no se registró el panic del barrido con su stack:\n%s", logs.String())
	}
}
