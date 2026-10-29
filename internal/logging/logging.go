// Package logging centraliza cómo se construyen los loggers de la aplicación.
//
// Regla de estilo del proyecto: el mensaje del log y el valor del atributo
// "error" se muestran con mayúscula inicial, para que la lectura y la búsqueda
// sean directas. Las cadenas de error del código se mantienen en minúscula,
// como manda la convención de Go (ST1005), porque los errores se envuelven
// dentro de otras frases: "no se pudo abrir: archivo ausente" se lee bien,
// "no se pudo abrir: Archivo ausente" no.
//
// Por eso la mayúscula se aplica acá, en el borde de salida, y no en el origen.
package logging

import (
	"io"
	"log/slog"
	"unicode"
	"unicode/utf8"
)

// New devuelve un logger de texto para consola, con el nivel indicado.
func New(w io.Writer, level slog.Leveler) *slog.Logger {
	return NewWithOptions(w, &slog.HandlerOptions{Level: level})
}

// NewWithOptions devuelve un logger de texto que capitaliza el mensaje y el
// error, conservando el ReplaceAttr que haya definido el llamador.
func NewWithOptions(w io.Writer, opts *slog.HandlerOptions) *slog.Logger {
	var merged slog.HandlerOptions
	if opts != nil {
		merged = *opts // copia: nunca se mutan las opciones del llamador
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

// Capitalize pone en mayúscula la primera letra de una cadena y respeta UTF-8.
// Si la cadena está vacía o empieza con algo que no es una letra minúscula
// (un dígito, un signo, una mayúscula) la devuelve sin cambios.
func Capitalize(s string) string {
	first, size := utf8.DecodeRuneInString(s)
	if size == 0 || !unicode.IsLower(first) {
		return s
	}
	return string(unicode.ToUpper(first)) + s[size:]
}
