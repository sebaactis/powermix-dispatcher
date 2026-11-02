package fakeapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"compras-remotas/internal/dispenser/snacks"
)

const snacksShipmentPath = "/open/v7/device/remote/shipment"

type shipment struct {
	paymentOrderID string
	mid            string
	productIDs     []string
	count          int
	results        []shipmentResult
}

type shipmentResult struct {
	productID string
	status    int
	reason    string
}

type Snacks struct {
	accessKey string
	appSecret string

	mu        sync.Mutex
	nonces    map[string]time.Time
	shipments map[string]*shipment
}

func NewSnacks(accessKey, appSecret string) *Snacks {
	return &Snacks{
		accessKey: accessKey,
		appSecret: appSecret,
		nonces:    make(map[string]time.Time),
		shipments: make(map[string]*shipment),
	}
}

func (o *Snacks) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != snacksShipmentPath {
		respondSnacks(w, http.StatusNotFound, 400, "ruta desconocida", nil)
		return
	}

	now := time.Now()
	switch err := snacks.VerifySignature(o.appSecret, r.Method, r.URL.Path, r.Header, now); {
	case errors.Is(err, snacks.ErrMissingHeader):
		respondSnacks(w, http.StatusBadRequest, 40001, "falta un header obligatorio", nil)
		return
	case errors.Is(err, snacks.ErrSkewedTimestamp):
		respondSnacks(w, http.StatusBadRequest, 40003, "timestamp fuera de la ventana", nil)
		return
	case errors.Is(err, snacks.ErrBadSignature):
		respondSnacks(w, http.StatusUnauthorized, 40004, "la firma no coincide", nil)
		return
	case err != nil:
		respondSnacks(w, http.StatusBadRequest, 40001, err.Error(), nil)
		return
	}

	nonce := r.Header.Get("nonce")
	o.mu.Lock()
	_, replayed := o.nonces[nonce]
	if !replayed {
		o.pruneNoncesLocked(now)
		o.nonces[nonce] = now
	}
	o.mu.Unlock()

	if replayed {
		respondSnacks(w, http.StatusBadRequest, 40005, "nonce repetido", nil)
		return
	}

	var req struct {
		PaymentOrderID string `json:"PaymentOrderId"`
		MID            string `json:"Mid"`
		Products       []struct {
			OrderProductID string `json:"OrderProductId"`
			ProductID      string `json:"ProductId"`
			ItemPricing    struct {
				RetailPrice float64 `json:"retailPrice"`
			} `json:"ItemPricing"`
		} `json:"DeviceRemoteShipmentProducts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondSnacks(w, http.StatusBadRequest, 400, "cuerpo inválido", nil)
		return
	}
	if req.PaymentOrderID == "" || req.MID == "" {
		respondSnacks(w, http.StatusBadRequest, 400, "PaymentOrderId y Mid son obligatorios", nil)
		return
	}
	if len(req.Products) == 0 {
		respondSnacks(w, http.StatusBadRequest, 400, "DeviceRemoteShipmentProducts está vacío", nil)
		return
	}
	for _, p := range req.Products {
		if p.ProductID == "" {
			respondSnacks(w, http.StatusBadRequest, 400, "cada producto necesita un ProductId", nil)
			return
		}
	}

	o.mu.Lock()
	record, ok := o.shipments[req.PaymentOrderID]
	if !ok {
		record = &shipment{paymentOrderID: req.PaymentOrderID, mid: req.MID}
		o.shipments[req.PaymentOrderID] = record
	}
	record.count++
	for _, p := range req.Products {
		record.productIDs = append(record.productIDs, p.ProductID)
	}
	o.mu.Unlock()

	respondSnacks(w, http.StatusOK, 200, "ok", map[string]any{})
}

func (o *Snacks) pruneNoncesLocked(now time.Time) {
	if len(o.nonces) < 1000 {
		return
	}
	for nonce, at := range o.nonces {
		if now.Sub(at) > 10*time.Minute {
			delete(o.nonces, nonce)
		}
	}
}

func (o *Snacks) ShipmentCount(paymentOrderID string) int {
	o.mu.Lock()
	defer o.mu.Unlock()

	if record, ok := o.shipments[paymentOrderID]; ok {
		return record.count
	}
	return 0
}

func (o *Snacks) SetResult(paymentOrderID string, status int, reason string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	record, ok := o.shipments[paymentOrderID]
	if !ok {
		return
	}
	record.results = nil
	for _, productID := range record.productIDs {
		record.results = append(record.results, shipmentResult{productID: productID, status: status, reason: reason})
	}
}

func respondSnacks(w http.ResponseWriter, httpStatus, code int, message string, result any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": code == 200,
		"code":    code,
		"message": message,
		"result":  result,
	})
}
