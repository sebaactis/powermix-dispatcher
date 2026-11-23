package httpx_test

import (
	"errors"
	"strings"
	"testing"

	"compras-remotas/internal/httpx"
)

func TestReadLimitedReturnsTheWholeBodyWithinTheLimit(t *testing.T) {
	raw, err := httpx.ReadLimited(strings.NewReader("hola"), 4)
	if err != nil {
		t.Fatalf("ReadLimited: %v", err)
	}
	if string(raw) != "hola" {
		t.Fatalf("raw = %q, se esperaba %q", raw, "hola")
	}
}

func TestReadLimitedReportsABodyOverTheLimit(t *testing.T) {
	raw, err := httpx.ReadLimited(strings.NewReader("hola!"), 4)
	if !errors.Is(err, httpx.ErrTooLarge) {
		t.Fatalf("err = %v, se esperaba httpx.ErrTooLarge", err)
	}
	if !strings.Contains(err.Error(), "4 bytes") {
		t.Fatalf("err = %q, se esperaba que nombre el límite", err)
	}
	if string(raw) != "hola" {
		t.Fatalf("raw = %q, se esperaba lo leído hasta el límite", raw)
	}
}
