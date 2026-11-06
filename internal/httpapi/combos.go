package httpapi

import (
	"net/http"
)

func (h *Handler) listCombos(w http.ResponseWriter, r *http.Request) {
	available, err := h.catalog.Combos(r.Context())
	if err != nil {
		fail(w, r, h.logger, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"combos": available})
}
