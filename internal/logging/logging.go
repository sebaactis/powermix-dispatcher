package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

const LevelEnv = "LOG_LEVEL"

type contextKey struct{}

var requestIDKey = contextKey{}

func WithRequestID(ctx context.Context, requestID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if requestID == "" {
		return ctx
	}
	return context.WithValue(ctx, requestIDKey, requestID)
}

func RequestIDFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	id, ok := ctx.Value(requestIDKey).(string)
	return id, ok && id != ""
}

func ParseLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("nivel de log inválido %q: valores permitidos debug, info, warn, error", raw)
	}
}

func LevelFromEnv() (slog.Level, error) {
	return ParseLevel(os.Getenv(LevelEnv))
}

func NewFromEnv(w io.Writer) (*slog.Logger, error) {
	level, err := LevelFromEnv()
	if err != nil {
		return nil, err
	}
	return New(w, level), nil
}

func New(w io.Writer, level slog.Leveler) *slog.Logger {
	return NewWithOptions(w, &slog.HandlerOptions{Level: level})
}

func NewWithOptions(w io.Writer, opts *slog.HandlerOptions) *slog.Logger {
	var merged slog.HandlerOptions
	if opts != nil {
		merged = *opts
	}

	previous := merged.ReplaceAttr
	merged.ReplaceAttr = func(groups []string, a slog.Attr) slog.Attr {
		if previous != nil {
			a = previous(groups, a)
		}
		if a.Key == slog.MessageKey || a.Key == "error" {
			return slog.String(a.Key, Capitalize(a.Value.String()))
		}
		return a
	}

	return slog.New(&requestIDHandler{next: slog.NewJSONHandler(w, &merged)})
}

type requestIDHandler struct {
	next slog.Handler
}

func (h *requestIDHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *requestIDHandler) Handle(ctx context.Context, record slog.Record) error {
	if id, ok := RequestIDFromContext(ctx); ok {
		record = record.Clone()
		record.AddAttrs(slog.String("request_id", id))
	}
	return h.next.Handle(ctx, record)
}

func (h *requestIDHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &requestIDHandler{next: h.next.WithAttrs(attrs)}
}

func (h *requestIDHandler) WithGroup(name string) slog.Handler {
	return &requestIDHandler{next: h.next.WithGroup(name)}
}

func Capitalize(s string) string {
	first, size := utf8.DecodeRuneInString(s)
	if size == 0 || !unicode.IsLower(first) {
		return s
	}
	return string(unicode.ToUpper(first)) + s[size:]
}
