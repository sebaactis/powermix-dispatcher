package httpapi

import (
	"context"
	"net/http"
	"testing"

	"compras-remotas/internal/catalog"
	"compras-remotas/internal/config"
	"compras-remotas/internal/order"
	"compras-remotas/internal/store"
)

func TestNewRejectsAnEmptyTopology(t *testing.T) {
	comboCatalog, err := catalog.NewMemory(testCombos())
	if err != nil {
		t.Fatalf("no se pudo construir el catálogo: %v", err)
	}

	memory := store.NewMemory()

	if _, err := New(Deps{
		Catalog:   comboCatalog,
		Placer:    order.New(memory, testMachines()),
		Confirmer: &fakeConfirmer{orders: memory},
		Orders:    memory,
		Env:       config.EnvSandbox,
	}); err == nil {
		t.Fatalf("sin muebles configurados el handler no debe construirse")
	}
}

func TestNewRejectsADuplicateKiosk(t *testing.T) {
	comboCatalog, err := catalog.NewMemory(testCombos())
	if err != nil {
		t.Fatalf("no se pudo construir el catálogo: %v", err)
	}

	memory := store.NewMemory()

	kiosks := []config.Kiosk{testKiosk("kiosco-centro"), testKiosk("kiosco-centro")}

	if _, err := New(Deps{
		Catalog:   comboCatalog,
		Placer:    order.New(memory, testMachines()),
		Confirmer: &fakeConfirmer{orders: memory},
		Orders:    memory,
		Kiosks:    kiosks,
		Env:       config.EnvSandbox,
	}); err == nil {
		t.Fatalf("un mueble repetido debe impedir construir el handler")
	}
}

func TestWrongMethodIsRejectedWithoutTouchingTheStore(t *testing.T) {
	fx := newFixture(t, config.EnvSandbox, []config.Kiosk{testKiosk("kiosco-centro")}, testCombos())

	recorder := call(t, fx.mux, http.MethodDelete, "/orders", "")
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("se esperaba 405 y llegó %d: %s", recorder.Code, recorder.Body.String())
	}

	if _, err := fx.orders.LoadOrder(context.Background(), "id-1"); err == nil {
		t.Fatalf("un método no soportado no debe crear nada")
	}
}
