package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"compras-remotas/internal/catalog"
	"compras-remotas/internal/config"
	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/order"
	"compras-remotas/internal/store"
)

const maxBodyBytes = 64 << 10

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

	callbacks       Receiver
	callbackSecrets CallbackSecrets

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

	if h.callbacks != nil {
		mux.HandleFunc("POST /callbacks/batidos/{secret}/{orderId}", h.batidosCallback)
		mux.HandleFunc("POST /callbacks/snacks/{secret}", h.snacksCallback)
	}
}

func (h *Handler) listCombos(w http.ResponseWriter, r *http.Request) {
	available, err := h.catalog.Combos(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"combos": available})
}

func (h *Handler) createOrder(w http.ResponseWriter, r *http.Request) {
	request, err := decodeCreateOrder(w, r)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	kiosk, err := h.resolveKiosk(request.KioskID)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	combo, err := h.catalog.Combo(r.Context(), request.ComboID)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	draft, err := h.buildDraft(kiosk, combo, request.ClientKey)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	placed, err := h.placer.Place(r.Context(), draft)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, newOrderResponse(placed))
}

func (h *Handler) readOrder(w http.ResponseWriter, r *http.Request) {
	loaded, err := h.orders.LoadOrder(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, newOrderResponse(loaded))
}

func (h *Handler) payOrder(w http.ResponseWriter, r *http.Request) {
	if !h.simulated {
		writeError(w, http.StatusForbidden, "el pago simulado no está disponible en producción")
		return
	}

	orderID := r.PathValue("id")

	if err := h.confirmer.ConfirmPayment(r.Context(), orderID, h.now()); err != nil {
		loaded, readErr := h.orders.LoadOrder(r.Context(), orderID)
		if readErr != nil {
			h.fail(w, r, errors.Join(err, readErr))
			return
		}

		if loaded.Status == store.OrderPendingPayment {
			h.fail(w, r, err)
			return
		}

		h.logger.WarnContext(r.Context(), "el pago quedó registrado con líneas con problemas",
			"order_id", orderID,
			"status", loaded.Status,
			"error", err,
		)

		writeJSON(w, http.StatusOK, newOrderResponse(loaded))
		return
	}

	loaded, err := h.orders.LoadOrder(r.Context(), orderID)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, newOrderResponse(loaded))
}

type createOrderRequest struct {
	KioskID   string `json:"kiosk_id"`
	ComboID   string `json:"combo_id"`
	ClientKey string `json:"client_key"`
}

func decodeCreateOrder(w http.ResponseWriter, r *http.Request) (createOrderRequest, error) {
	var request createOrderRequest

	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&request); err != nil {
		return createOrderRequest{}, badRequest(fmt.Sprintf("el cuerpo del pedido no es un JSON válido: %v", err))
	}

	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return createOrderRequest{}, badRequest("el cuerpo del pedido debe ser un único objeto JSON")
	}

	if strings.TrimSpace(request.ComboID) == "" {
		return createOrderRequest{}, badRequest("hace falta indicar el combo (combo_id)")
	}

	return request, nil
}

func (h *Handler) resolveKiosk(requested string) (config.Kiosk, error) {
	id := strings.TrimSpace(requested)

	if id == "" {
		if h.onlyKiosk == "" {
			return config.Kiosk{}, badRequest("hace falta indicar el mueble (kiosk_id): hay más de uno configurado")
		}

		id = h.onlyKiosk
	}

	kiosk, found := h.kiosks[id]
	if !found {
		return config.Kiosk{}, apiError{
			status:  http.StatusNotFound,
			message: fmt.Sprintf("el mueble %q no está configurado", id),
		}
	}

	return kiosk, nil
}

func (h *Handler) buildDraft(kiosk config.Kiosk, combo catalog.Combo, clientKey string) (order.Draft, error) {
	draft := order.Draft{
		KioskID:     kiosk.ID,
		ComboID:     combo.ID,
		ClientKey:   strings.TrimSpace(clientKey),
		AmountCents: combo.PriceCents,
		Currency:    combo.Currency,
		Items:       make([]order.Item, 0, len(combo.Items)),
	}

	for _, item := range combo.Items {
		machine, found := kiosk.Machine(config.MachineType(item.MachineType))
		if !found {
			return order.Draft{}, apiError{
				status: http.StatusConflict,
				message: fmt.Sprintf(
					"el combo %q necesita una máquina %q y el mueble %q no la tiene",
					combo.ID, item.MachineType, kiosk.ID,
				),
			}
		}

		draft.Items = append(draft.Items, order.Item{
			SKU: item.SKU,
			Machine: dispenser.MachineRef{
				KioskID:      kiosk.ID,
				Type:         string(machine.Type),
				Name:         machine.Name,
				DeviceID:     machine.DeviceID,
				DeviceNo:     machine.DeviceNo,
				DeviceTypeID: machine.DeviceTypeID,
				MID:          machine.MID,
			},
		})
	}

	return draft, nil
}

type apiError struct {
	status  int
	message string
}

func (e apiError) Error() string {
	return e.message
}

func badRequest(message string) apiError {
	return apiError{status: http.StatusBadRequest, message: message}
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	var apiErr apiError
	if errors.As(err, &apiErr) {
		writeError(w, apiErr.status, apiErr.message)
		return
	}

	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, catalog.ErrNotFound.Error())
		return
	}

	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "la orden no existe")
		return
	}

	var notSellable *order.NotSellableError
	if errors.As(err, &notSellable) {
		reasons := notSellable.Reasons
		if reasons == nil {
			reasons = []string{}
		}

		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   order.ErrNotSellable.Error(),
			"reasons": reasons,
		})
		return
	}

	h.logger.ErrorContext(r.Context(), "no se pudo atender el pedido HTTP",
		"method", r.Method,
		"path", redactCallbackPath(r.URL.Path),
		"error", err,
	)

	writeError(w, http.StatusInternalServerError, "no se pudo completar la operación")
}

type orderResponse struct {
	ID          string            `json:"id"`
	KioskID     string            `json:"kiosk_id"`
	ComboID     string            `json:"combo_id"`
	AmountCents int64             `json:"amount_cents"`
	Currency    string            `json:"currency"`
	Status      store.OrderStatus `json:"status"`
	CreatedAt   *time.Time        `json:"created_at,omitempty"`
	PaidAt      *time.Time        `json:"paid_at,omitempty"`
	ClosedAt    *time.Time        `json:"closed_at,omitempty"`
	Lines       []lineResponse    `json:"lines"`
}

type lineResponse struct {
	ID          string              `json:"id"`
	Position    int                 `json:"position"`
	MachineType string              `json:"machine_type"`
	SKU         string              `json:"sku"`
	State       dispenser.LineState `json:"state"`
	Attempts    int                 `json:"attempts"`
	LastError   string              `json:"last_error,omitempty"`
}

func newOrderResponse(loaded store.Order) orderResponse {
	response := orderResponse{
		ID:          loaded.ID,
		KioskID:     loaded.KioskID,
		ComboID:     loaded.ComboID,
		AmountCents: loaded.AmountCents,
		Currency:    loaded.Currency,
		Status:      loaded.Status,
		Lines:       make([]lineResponse, 0, len(loaded.Lines)),
	}

	if !loaded.CreatedAt.IsZero() {
		response.CreatedAt = &loaded.CreatedAt
	}

	if !loaded.PaidAt.IsZero() {
		response.PaidAt = &loaded.PaidAt
	}

	if !loaded.ClosedAt.IsZero() {
		response.ClosedAt = &loaded.ClosedAt
	}

	for _, line := range loaded.Lines {
		response.Lines = append(response.Lines, lineResponse{
			ID:          line.ID,
			Position:    line.Position,
			MachineType: line.MachineType,
			SKU:         line.SKU,
			State:       line.State,
			Attempts:    line.Attempts,
			LastError:   line.LastError,
		})
	}

	return response
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
