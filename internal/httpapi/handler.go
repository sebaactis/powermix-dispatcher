package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"compras-remotas/internal/catalog"
	"compras-remotas/internal/config"
	"compras-remotas/internal/order"
	"compras-remotas/internal/store"
)

type Catalog interface {
	Combo(ctx context.Context, id string) (catalog.Combo, error)
	Combos(ctx context.Context) ([]catalog.Combo, error)
}

type Placer interface {
	Place(ctx context.Context, draft order.Draft) (store.Order, error)
}

type Confirmer interface {
	ConfirmPayment(ctx context.Context, orderID string, at time.Time) error
}

type OrderReader interface {
	LoadOrder(ctx context.Context, orderID string) (store.Order, error)
}

type Deps struct {
	Catalog   Catalog
	Placer    Placer
	Confirmer Confirmer
	Orders    OrderReader
	Kiosks    []config.Kiosk
	Env       string
}

type Handler struct {
	catalog   Catalog
	placer    Placer
	confirmer Confirmer
	orders    OrderReader

	kiosks    map[string]config.Kiosk
	onlyKiosk string
	simulated bool

	now    func() time.Time
	logger *slog.Logger
}

type Option func(*Handler)

func WithClock(now func() time.Time) Option {
	return func(h *Handler) {
		if now != nil {
			h.now = now
		}
	}
}

func WithLogger(logger *slog.Logger) Option {
	return func(h *Handler) {
		if logger != nil {
			h.logger = logger
		}
	}
}

func New(deps Deps, opts ...Option) (*Handler, error) {
	if deps.Catalog == nil || deps.Placer == nil || deps.Confirmer == nil || deps.Orders == nil {
		return nil, errors.New("el handler HTTP necesita catálogo, motor y store")
	}

	if len(deps.Kiosks) == 0 {
		return nil, errors.New("el handler HTTP necesita al menos un mueble configurado")
	}

	byID := make(map[string]config.Kiosk, len(deps.Kiosks))

	for _, kiosk := range deps.Kiosks {
		if kiosk.ID == "" {
			return nil, errors.New("hay un mueble configurado sin id")
		}

		if _, duplicate := byID[kiosk.ID]; duplicate {
			return nil, fmt.Errorf("el mueble %q está declarado dos veces", kiosk.ID)
		}

		byID[kiosk.ID] = kiosk
	}

	handler := &Handler{
		catalog:   deps.Catalog,
		placer:    deps.Placer,
		confirmer: deps.Confirmer,
		orders:    deps.Orders,
		kiosks:    byID,
		simulated: deps.Env == config.EnvSandbox,
		now:       time.Now,
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	if len(byID) == 1 {
		for id := range byID {
			handler.onlyKiosk = id
		}
	}

	for _, opt := range opts {
		if opt != nil {
			opt(handler)
		}
	}

	return handler, nil
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /combos", h.listCombos)
	mux.HandleFunc("POST /orders", h.createOrder)
	mux.HandleFunc("GET /orders/{id}", h.readOrder)
	mux.HandleFunc("POST /orders/{id}/pay", h.payOrder)
}
