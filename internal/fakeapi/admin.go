package fakeapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"compras-remotas/internal/dispenser/batidos"
	"compras-remotas/internal/dispenser/snacks"
)

func NewAdmin(batidos *Batidos, snacks *Snacks) http.Handler {
	return &adminHandler{batidos: batidos, snacks: snacks}
}

const adminMaxBodyBytes = 64 << 10

const (
	adminBatidosGoodsPath      = "/admin/batidos/goods"
	adminBatidosOfflinePath    = "/admin/batidos/offline"
	adminBatidosAbnormalPath   = "/admin/batidos/abnormal"
	adminBatidosOutOfStockPath = "/admin/batidos/out_of_stock"
	adminBatidosAdvancePath    = "/admin/batidos/advance"
	adminSnacksDevicePath      = "/admin/snacks/device"
	adminSnacksSlotPath        = "/admin/snacks/slot"
	adminSnacksOrderPath       = "/admin/snacks/order"
)

type adminHandler struct {
	batidos *Batidos
	snacks  *Snacks
}

func (a *adminHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondAdminError(w, http.StatusMethodNotAllowed, "la administración sólo admite POST")
		return
	}

	switch r.URL.Path {
	case adminBatidosGoodsPath:
		a.setBatidosGoods(w, r)
	case adminBatidosOfflinePath:
		a.setBatidosOffline(w, r)
	case adminBatidosAbnormalPath:
		a.setBatidosAbnormal(w, r)
	case adminBatidosOutOfStockPath:
		a.setBatidosOutOfStock(w, r)
	case adminBatidosAdvancePath:
		a.advanceBatidos(w, r)
	case adminSnacksDevicePath:
		a.setSnacksDevice(w, r)
	case adminSnacksSlotPath:
		a.setSnacksSlot(w, r)
	case adminSnacksOrderPath:
		a.setSnacksOrder(w, r)
	default:
		respondAdminError(w, http.StatusNotFound, "ruta de administración desconocida")
	}
}

type batidosGoodsBody struct {
	DeviceID string `json:"device_id"`
	Goods    []struct {
		ID      string `json:"id"`
		Listing bool   `json:"listing"`
		SoldOut int    `json:"sold_out"`
	} `json:"goods"`
}

func (a *adminHandler) setBatidosGoods(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeAdmin[batidosGoodsBody](w, r)
	if !ok {
		return
	}

	if err := requireDeviceID(body.DeviceID); err != nil {
		respondAdminError(w, http.StatusBadRequest, err.Error())
		return
	}

	if body.Goods == nil {
		respondAdminError(w, http.StatusBadRequest, "hace falta el array goods (vacío para dejar el dispositivo sin productos)")
		return
	}

	goods := make([]batidos.DeviceGoods, 0, len(body.Goods))
	for position, item := range body.Goods {
		if item.ID == "" {
			respondAdminError(w, http.StatusBadRequest, fmt.Sprintf("el producto %d no tiene id", position+1))
			return
		}

		goods = append(goods, batidos.DeviceGoods{ID: item.ID, Listing: item.Listing, SoldOut: item.SoldOut})
	}

	a.batidos.SetDeviceGoods(body.DeviceID, goods)
	respondAdminOK(w)
}

type batidosOfflineBody struct {
	DeviceID string `json:"device_id"`
	Offline  bool   `json:"offline"`
}

func (a *adminHandler) setBatidosOffline(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeAdmin[batidosOfflineBody](w, r)
	if !ok {
		return
	}

	if err := requireDeviceID(body.DeviceID); err != nil {
		respondAdminError(w, http.StatusBadRequest, err.Error())
		return
	}

	if !body.Offline {
		respondAdminError(w, http.StatusNotImplemented, "el falso de batidos no puede volver a poner el dispositivo en línea: no existe un setter para eso")
		return
	}

	a.batidos.SetDeviceOffline(body.DeviceID)
	respondAdminOK(w)
}

type batidosAbnormalBody struct {
	DeviceID string `json:"device_id"`
	Abnormal bool   `json:"abnormal"`
}

func (a *adminHandler) setBatidosAbnormal(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeAdmin[batidosAbnormalBody](w, r)
	if !ok {
		return
	}

	if err := requireDeviceID(body.DeviceID); err != nil {
		respondAdminError(w, http.StatusBadRequest, err.Error())
		return
	}

	if !body.Abnormal {
		respondAdminError(w, http.StatusNotImplemented, "el falso de batidos no puede volver a marcar el dispositivo como normal: no existe un setter para eso")
		return
	}

	a.batidos.SetDeviceAbnormal(body.DeviceID)
	respondAdminOK(w)
}

type batidosOutOfStockBody struct {
	DeviceID   string `json:"device_id"`
	OutOfStock bool   `json:"out_of_stock"`
}

func (a *adminHandler) setBatidosOutOfStock(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeAdmin[batidosOutOfStockBody](w, r)
	if !ok {
		return
	}

	if err := requireDeviceID(body.DeviceID); err != nil {
		respondAdminError(w, http.StatusBadRequest, err.Error())
		return
	}

	if !body.OutOfStock {
		respondAdminError(w, http.StatusNotImplemented, "el falso de batidos no puede volver a marcar el dispositivo con insumos: no existe un setter para eso")
		return
	}

	a.batidos.SetDeviceOutOfStock(body.DeviceID)
	respondAdminOK(w)
}

type batidosAdvanceBody struct {
	Code   string `json:"code"`
	Status *int   `json:"status"`
}

func (a *adminHandler) advanceBatidos(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeAdmin[batidosAdvanceBody](w, r)
	if !ok {
		return
	}

	if body.Code == "" {
		respondAdminError(w, http.StatusBadRequest, "hace falta el código de canje (code)")
		return
	}

	if body.Status == nil {
		respondAdminError(w, http.StatusBadRequest, "hace falta el estado de la orden (status)")
		return
	}

	a.batidos.Advance(body.Code, *body.Status)
	respondAdminOK(w)
}

type snacksDeviceBody struct {
	Online        *bool `json:"online"`
	SellStatus    *int  `json:"sell_status"`
	FaultState    *int  `json:"fault_state"`
	LockType      *int  `json:"lock_type"`
	StockoutState *int  `json:"stockout_state"`
}

func (a *adminHandler) setSnacksDevice(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeAdmin[snacksDeviceBody](w, r)
	if !ok {
		return
	}

	if body.Online == nil && body.SellStatus == nil && body.FaultState == nil && body.LockType == nil && body.StockoutState == nil {
		respondAdminError(w, http.StatusBadRequest, "hace falta al menos un campo del dispositivo de snacks")
		return
	}

	if body.Online != nil {
		a.snacks.SetDeviceOnline(*body.Online)
	}
	if body.SellStatus != nil {
		a.snacks.SetSellStatus(snacks.SellStatus(*body.SellStatus))
	}
	if body.FaultState != nil {
		a.snacks.SetFaultState(snacks.FaultState(*body.FaultState))
	}
	if body.LockType != nil {
		a.snacks.SetLockType(snacks.LockType(*body.LockType))
	}
	if body.StockoutState != nil {
		a.snacks.SetStockoutState(snacks.StockoutState(*body.StockoutState))
	}

	respondAdminOK(w)
}

type snacksSlotBody struct {
	SlotNo     string `json:"slot_no"`
	Stock      *int   `json:"stock"`
	SlotStatus *int   `json:"slot_status"`
}

func (a *adminHandler) setSnacksSlot(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeAdmin[snacksSlotBody](w, r)
	if !ok {
		return
	}

	if body.SlotNo == "" {
		respondAdminError(w, http.StatusBadRequest, "hace falta el número de ranura (slot_no)")
		return
	}

	if body.Stock == nil && body.SlotStatus == nil {
		respondAdminError(w, http.StatusBadRequest, "hace falta stock o slot_status")
		return
	}

	if body.Stock != nil {
		a.snacks.SetSlotStock(body.SlotNo, *body.Stock)
	}
	if body.SlotStatus != nil {
		a.snacks.SetSlotStatus(body.SlotNo, snacks.SlotStatus(*body.SlotStatus))
	}

	respondAdminOK(w)
}

type snacksOrderBody struct {
	Status *int    `json:"status"`
	Name   *string `json:"name"`
	Empty  *bool   `json:"empty"`
}

func (a *adminHandler) setSnacksOrder(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeAdmin[snacksOrderBody](w, r)
	if !ok {
		return
	}

	if body.Empty != nil {
		if body.Status != nil || body.Name != nil {
			respondAdminError(w, http.StatusBadRequest, "empty no se combina con status ni con name")
			return
		}

		a.snacks.SetOrdersEmpty(*body.Empty)
		respondAdminOK(w)
		return
	}

	if body.Status == nil || body.Name == nil {
		respondAdminError(w, http.StatusBadRequest, "hace falta status y name, o empty")
		return
	}

	a.snacks.SetOrderStatus(*body.Status, *body.Name)
	respondAdminOK(w)
}

func decodeAdmin[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	var body T

	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, adminMaxBodyBytes))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&body); err != nil {
		respondAdminError(w, http.StatusBadRequest, fmt.Sprintf("el cuerpo de la administración no es un JSON válido: %v", err))
		return body, false
	}

	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		respondAdminError(w, http.StatusBadRequest, "el cuerpo de la administración debe ser un único objeto JSON")
		return body, false
	}

	return body, true
}

func requireDeviceID(deviceID string) error {
	if deviceID == "" {
		return errors.New("hace falta el identificador del dispositivo (device_id)")
	}

	return nil
}

func respondAdminOK(w http.ResponseWriter) {
	respondAdmin(w, http.StatusOK, map[string]any{"ok": true})
}

func respondAdminError(w http.ResponseWriter, status int, message string) {
	respondAdmin(w, status, map[string]any{"ok": false, "error": message})
}

func respondAdmin(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(payload)
}
