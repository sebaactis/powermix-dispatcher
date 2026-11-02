package fakeapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"compras-remotas/internal/dispenser/snacks"
)

const snacksShipmentPath = "/open/v7/device/remote/shipment"

const snacksDevicesPath = "/open/v7/device/page"

const snacksSlotsPath = "/open/v7/device/slotlist"

const defaultSnacksDeviceCode = "snacks-dev-1"

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

type deviceState struct {
	deviceCode          string
	deviceName          string
	deviceTypeName      string
	onlineStatus        int
	sellStatus          int
	faultState          int
	lockType            int
	stockoutState       int
	shortageAlarmNumber int
}

type slotState struct {
	slotID      string
	slotNo      string
	slotStatus  int
	stock       int
	capacity    int
	productID   string
	productName string
	price       float64
}

type Snacks struct {
	accessKey string
	appSecret string

	mu        sync.Mutex
	nonces    map[string]time.Time
	shipments map[string]*shipment
	device    deviceState
	slots     []slotState
}

func NewSnacks(accessKey, appSecret string) *Snacks {
	return &Snacks{
		accessKey: accessKey,
		appSecret: appSecret,
		nonces:    make(map[string]time.Time),
		shipments: make(map[string]*shipment),
		device: deviceState{
			deviceCode:     defaultSnacksDeviceCode,
			deviceName:     "Snack Powermix",
			deviceTypeName: "Powermix Snacks",
			onlineStatus:   1,
			sellStatus:     1,
		},
		slots: []slotState{
			{
				slotID:      "slot-1",
				slotNo:      "1",
				slotStatus:  1,
				stock:       5,
				capacity:    10,
				productID:   "prod_snack_1",
				productName: "Papas clásicas",
				price:       0.01,
			},
			{
				slotID:      "slot-2",
				slotNo:      "2",
				slotStatus:  1,
				stock:       3,
				capacity:    10,
				productID:   "prod_snack_2",
				productName: "Maní salado",
				price:       0.01,
			},
		},
	}
}

func (o *Snacks) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case snacksShipmentPath:
		o.handleShipment(w, r)
	case snacksDevicesPath:
		o.handleDevices(w, r)
	case snacksSlotsPath:
		o.handleSlots(w, r)
	default:
		respondSnacks(w, http.StatusNotFound, 400, "ruta desconocida", nil)
	}
}

func (o *Snacks) authorize(w http.ResponseWriter, r *http.Request) bool {
	now := time.Now()
	switch err := snacks.VerifySignature(o.appSecret, r.Method, r.URL.Path, r.Header, now); {
	case errors.Is(err, snacks.ErrMissingHeader):
		respondSnacks(w, http.StatusBadRequest, 40001, "falta un header obligatorio", nil)
		return false
	case errors.Is(err, snacks.ErrSkewedTimestamp):
		respondSnacks(w, http.StatusBadRequest, 40003, "timestamp fuera de la ventana", nil)
		return false
	case errors.Is(err, snacks.ErrBadSignature):
		respondSnacks(w, http.StatusUnauthorized, 40004, "la firma no coincide", nil)
		return false
	case err != nil:
		respondSnacks(w, http.StatusBadRequest, 40001, err.Error(), nil)
		return false
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
		return false
	}
	return true
}

func (o *Snacks) handleShipment(w http.ResponseWriter, r *http.Request) {
	if !o.authorize(w, r) {
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

func (o *Snacks) handleDevices(w http.ResponseWriter, r *http.Request) {
	if !o.authorize(w, r) {
		return
	}

	query := r.URL.Query()
	page, err := strconv.Atoi(query.Get("Page"))
	if err != nil || page < 1 {
		respondSnacks(w, http.StatusBadRequest, 400, "Page es obligatorio y debe ser un entero positivo", nil)
		return
	}
	pageSize, err := strconv.Atoi(query.Get("PageSize"))
	if err != nil || pageSize < 1 {
		respondSnacks(w, http.StatusBadRequest, 400, "PageSize es obligatorio y debe ser un entero positivo", nil)
		return
	}

	o.mu.Lock()
	device := o.device
	o.mu.Unlock()

	items := make([]map[string]any, 0, 1)
	if code := query.Get("DeviceCode"); code == "" || code == device.deviceCode {
		items = append(items, map[string]any{
			"deviceCode":          device.deviceCode,
			"deviceName":          device.deviceName,
			"deviceTypeName":      device.deviceTypeName,
			"onlineStatus":        device.onlineStatus,
			"sellStatus":          device.sellStatus,
			"faultState":          device.faultState,
			"lockType":            device.lockType,
			"stockoutState":       device.stockoutState,
			"shortageAlarmNumber": device.shortageAlarmNumber,
		})
	}

	total := len(items)
	totalPages := 0
	if total > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	if page > totalPages {
		items = make([]map[string]any, 0)
	}

	respondSnacks(w, http.StatusOK, 200, "ok", map[string]any{
		"page":        page,
		"pageSize":    pageSize,
		"total":       total,
		"totalPages":  totalPages,
		"items":       items,
		"hasPrevPage": page > 1,
		"hasNextPage": page < totalPages,
	})
}

func (o *Snacks) handleSlots(w http.ResponseWriter, r *http.Request) {
	if !o.authorize(w, r) {
		return
	}

	o.mu.Lock()
	deviceCode := o.device.deviceCode
	slots := make([]slotState, len(o.slots))
	copy(slots, o.slots)
	o.mu.Unlock()

	items := make([]map[string]any, 0, len(slots))
	if code := r.URL.Query().Get("DeviceCode"); code == "" || code == deviceCode {
		for _, slot := range slots {
			items = append(items, map[string]any{
				"slotId":      slot.slotID,
				"slotNo":      slot.slotNo,
				"slotStatus":  slot.slotStatus,
				"stock":       slot.stock,
				"capacity":    slot.capacity,
				"productId":   slot.productID,
				"productName": slot.productName,
				"price":       slot.price,
			})
		}
	}

	respondSnacks(w, http.StatusOK, 200, "ok", items)
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

func (o *Snacks) SetDeviceOnline(online bool) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if online {
		o.device.onlineStatus = 1
		return
	}
	o.device.onlineStatus = 0
}

func (o *Snacks) SetSellStatus(status snacks.SellStatus) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.device.sellStatus = int(status)
}

func (o *Snacks) SetFaultState(state snacks.FaultState) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.device.faultState = int(state)
}

func (o *Snacks) SetLockType(lockType snacks.LockType) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.device.lockType = int(lockType)
}

func (o *Snacks) SetStockoutState(state snacks.StockoutState) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.device.stockoutState = int(state)
}

func (o *Snacks) SetSlotStock(slotNo string, stock int) {
	o.mu.Lock()
	defer o.mu.Unlock()

	for i := range o.slots {
		if o.slots[i].slotNo == slotNo {
			o.slots[i].stock = stock
		}
	}
}

func (o *Snacks) SetSlotStatus(slotNo string, status snacks.SlotStatus) {
	o.mu.Lock()
	defer o.mu.Unlock()

	for i := range o.slots {
		if o.slots[i].slotNo == slotNo {
			o.slots[i].slotStatus = int(status)
		}
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
