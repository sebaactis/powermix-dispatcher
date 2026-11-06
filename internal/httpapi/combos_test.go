package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"compras-remotas/internal/config"
)

func TestListCombosReturnsOnlyActiveCombos(t *testing.T) {
	fx := newFixture(t, config.EnvSandbox, []config.Kiosk{testKiosk("kiosco-centro")}, testCombos())

	recorder := call(t, fx.mux, http.MethodGet, "/combos", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("se esperaba 200 y llegó %d: %s", recorder.Code, recorder.Body.String())
	}

	payload := decode(t, recorder)
	combos := array(t, payload, "combos")

	if len(combos) != 2 {
		t.Fatalf("se esperaban 2 combos activos y llegaron %d: %v", len(combos), combos)
	}

	ids := make([]string, 0, len(combos))
	for _, entry := range combos {
		combo, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("se esperaba un objeto de combo y llegó %#v", entry)
		}

		ids = append(ids, combo["id"].(string))

		if combo["name"] == "" {
			t.Fatalf("el combo %v llegó sin nombre", combo["id"])
		}

		if combo["price_cents"] == nil || combo["currency"] == nil {
			t.Fatalf("el combo %v llegó sin precio o sin moneda: %v", combo["id"], combo)
		}
	}

	if strings.Join(ids, ",") != "combo-con-maquina-ausente,combo-mixto" {
		t.Fatalf("se esperaban los combos activos ordenados por id y llegaron %v", ids)
	}

	for _, entry := range combos {
		combo := entry.(map[string]any)
		if combo["id"] == "combo-inactivo" {
			t.Fatalf("un combo inactivo no debe listarse")
		}
	}
}
