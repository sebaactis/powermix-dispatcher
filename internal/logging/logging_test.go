package logging

import (
	"bytes"
	"errors"
	"log/slog"
	"testing"
)

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

// El mensaje y el valor del error salen con mayúscula inicial, pero el resto de
// los campos no se toca.
func TestHandlerCapitalizesMessageAndError(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, slog.LevelInfo)

	logger.Error("no se pudo cargar la configuración",
		"error", errors.New("archivo ausente"),
		"path", "config.json",
	)

	out := buf.String()
	for _, want := range []string{
		`msg="No se pudo cargar la configuración"`,
		`error="Archivo ausente"`,
		"path=config.json",
	} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Fatalf("la salida %q no contiene %q", out, want)
		}
	}
}

// Un ReplaceAttr propio del llamador debe seguir ejecutándose: el capitalizador
// se compone, no reemplaza.
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

	out := buf.String()
	for _, want := range []string{`msg="Listo para vender"`, "kiosk=renombrado"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Fatalf("la salida %q no contiene %q", out, want)
		}
	}
}

// NewWithOptions no debe mutar las opciones del llamador.
func TestNewWithOptionsDoesNotMutateCallerOptions(t *testing.T) {
	var buf bytes.Buffer
	opts := &slog.HandlerOptions{}

	_ = NewWithOptions(&buf, opts)

	if opts.ReplaceAttr != nil {
		t.Fatal("NewWithOptions modificó el ReplaceAttr del llamador")
	}
}
