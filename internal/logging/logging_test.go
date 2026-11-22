package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func parseLogLine(t *testing.T, raw string) map[string]any {
	t.Helper()

	line := strings.TrimSpace(raw)
	if line == "" {
		t.Fatal("se esperaba una línea de log JSON y estuvo vacía")
	}

	var rec map[string]any
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		t.Fatalf("la salida %q no es JSON válido: %v", raw, err)
	}
	return rec
}

func TestCapitalize(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"vacío", "", ""},
		{"minúscula", "api escuchando", "Api escuchando"},
		{"ya en mayúscula", "API escuchando", "API escuchando"},
		{"acentuada", "ñandú corre", "Ñandú corre"},
		{"empieza con dígito", "404 no encontrado", "404 no encontrado"},
		{"empieza con signo", "«algo»", "«algo»"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Capitalize(tc.in); got != tc.want {
				t.Fatalf("Capitalize(%q) = %q, se esperaba %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestHandlerEmitsJSONAndCapitalizesMessageAndError(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, slog.LevelInfo)

	logger.Error("no se pudo cargar la configuración",
		"error", errors.New("archivo ausente"),
		"path", "config.json",
	)

	rec := parseLogLine(t, buf.String())
	if rec["level"] != "ERROR" {
		t.Fatalf("level = %v, se esperaba ERROR", rec["level"])
	}
	if rec["msg"] != "No se pudo cargar la configuración" {
		t.Fatalf("msg = %v, se esperaba capitalizado", rec["msg"])
	}
	if rec["error"] != "Archivo ausente" {
		t.Fatalf("error = %v, se esperaba capitalizado", rec["error"])
	}
	if rec["path"] != "config.json" {
		t.Fatalf("path = %v, se esperaba config.json", rec["path"])
	}
}

func TestNewWithOptionsKeepsTheCallerReplaceAttr(t *testing.T) {
	var buf bytes.Buffer
	rename := func(groups []string, a slog.Attr) slog.Attr {
		if a.Key == "kiosk" {
			return slog.String(a.Key, "renombrado")
		}
		return a
	}

	logger := NewWithOptions(&buf, &slog.HandlerOptions{ReplaceAttr: rename})
	logger.Info("listo para vender", "kiosk", "k1")

	rec := parseLogLine(t, buf.String())
	if rec["msg"] != "Listo para vender" {
		t.Fatalf("msg = %v, se esperaba %q", rec["msg"], "Listo para vender")
	}
	if rec["kiosk"] != "renombrado" {
		t.Fatalf("kiosk = %v, se esperaba %q", rec["kiosk"], "renombrado")
	}
}

func TestNewWithOptionsDoesNotMutateCallerOptions(t *testing.T) {
	var buf bytes.Buffer
	opts := &slog.HandlerOptions{}

	_ = NewWithOptions(&buf, opts)

	if opts.ReplaceAttr != nil {
		t.Fatal("NewWithOptions modificó el ReplaceAttr del llamador")
	}
}

func TestParseLevel(t *testing.T) {
	valid := []struct {
		raw  string
		want slog.Level
	}{
		{"", slog.LevelInfo},
		{"   ", slog.LevelInfo},
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"Info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"WARN", slog.LevelWarn},
		{"error", slog.LevelError},
		{"Error", slog.LevelError},
	}

	for _, tc := range valid {
		t.Run("valido_"+tc.raw, func(t *testing.T) {
			got, err := ParseLevel(tc.raw)
			if err != nil {
				t.Fatalf("ParseLevel(%q) devolvió error: %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("ParseLevel(%q) = %v, se esperaba %v", tc.raw, got, tc.want)
			}
		})
	}

	for _, invalid := range []string{"trace", "warning", "fatal", "123"} {
		t.Run("invalido_"+invalid, func(t *testing.T) {
			_, err := ParseLevel(invalid)
			if err == nil {
				t.Fatalf("ParseLevel(%q) debía fallar", invalid)
			}
			want := "nivel de log inválido " + `"` + invalid + `": valores permitidos debug, info, warn, error`
			if err.Error() != want {
				t.Fatalf("error = %q, se esperaba %q", err.Error(), want)
			}
		})
	}
}

func TestNewFromEnvFiltersByLogLevel(t *testing.T) {
	t.Setenv("LOG_LEVEL", "WARN")

	var buf bytes.Buffer
	logger, err := NewFromEnv(&buf)
	if err != nil {
		t.Fatalf("NewFromEnv falló: %v", err)
	}

	logger.Debug("detalle oculto")
	logger.Info("info oculta")
	logger.Warn("aviso visible")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("se esperaba exactamente 1 línea en warn, llegaron %d: %s", len(lines), buf.String())
	}

	rec := parseLogLine(t, lines[0])
	if rec["level"] != "WARN" || rec["msg"] != "Aviso visible" {
		t.Fatalf("registro inesperado: %v", rec)
	}
}

func TestNewFromEnvRejectsInvalidLogLevel(t *testing.T) {
	t.Setenv("LOG_LEVEL", "verbose")

	var buf bytes.Buffer
	if _, err := NewFromEnv(&buf); err == nil {
		t.Fatal("NewFromEnv debía rechazar LOG_LEVEL=verbose")
	}
}

func TestLoggerAddsRequestIDFromContext(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, slog.LevelInfo)

	ctx := WithRequestID(context.Background(), "req-abc-123")
	logger.InfoContext(ctx, "orden creada", "order_id", "ord-1")

	rec := parseLogLine(t, buf.String())
	if rec["request_id"] != "req-abc-123" {
		t.Fatalf("request_id = %v, se esperaba req-abc-123", rec["request_id"])
	}
	if rec["order_id"] != "ord-1" {
		t.Fatalf("order_id = %v, se esperaba ord-1", rec["order_id"])
	}

	buf.Reset()
	logger.With("component", "http").InfoContext(ctx, "con attrs")
	recAttrs := parseLogLine(t, buf.String())
	if recAttrs["request_id"] != "req-abc-123" || recAttrs["component"] != "http" {
		t.Fatalf("WithAttrs perdió request_id o component: %v", recAttrs)
	}

	buf.Reset()
	logger.InfoContext(context.Background(), "sin request id")
	recNoID := parseLogLine(t, buf.String())
	if _, exists := recNoID["request_id"]; exists {
		t.Fatalf("no debía incluir request_id cuando el contexto no lo tiene: %v", recNoID)
	}
}
