package logging

import (
	"io"
	"log/slog"
	"unicode"
	"unicode/utf8"
)

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

	return slog.New(slog.NewTextHandler(w, &merged))
}

func Capitalize(s string) string {
	first, size := utf8.DecodeRuneInString(s)
	if size == 0 || !unicode.IsLower(first) {
		return s
	}
	return string(unicode.ToUpper(first)) + s[size:]
}
