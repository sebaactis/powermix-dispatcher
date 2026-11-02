package fakeapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"compras-remotas/internal/dispenser/batidos"
)

const (
	batidosDeviceDetailPath = "/coffee/newThird/device/detail/"
	batidosDeviceGoodsPath  = "/coffee/newThird/goods/deviceGoodsDetail/"
	batidosDeviceTypeID     = "dt-1"
)

type batidosCode struct {
	code     string
	goodsID  string
	deviceID string
	deviceNo string
	orderNo  string
	status   int
	used     bool
}

type batidosDeviceState struct {
	offline    bool
	abnormal   bool
	outOfStock bool
}

type Batidos struct {
	key    string
	secret string

	mu      sync.Mutex
	next    int
	orders  int
	codes   map[string]*batidosCode
	devices map[string]*batidosDeviceState
	goods   map[string][]batidos.DeviceGoods
}

func NewBatidos(key, secret string) *Batidos {
	return &Batidos{
		key:     key,
		secret:  secret,
		codes:   make(map[string]*batidosCode),
		devices: make(map[string]*batidosDeviceState),
		goods:   make(map[string][]batidos.DeviceGoods),
	}
}

func (g *Batidos) SetDeviceOffline(deviceID string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.deviceState(deviceID).offline = true
}

func (g *Batidos) SetDeviceAbnormal(deviceID string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.deviceState(deviceID).abnormal = true
}

func (g *Batidos) SetDeviceOutOfStock(deviceID string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.deviceState(deviceID).outOfStock = true
}

func (g *Batidos) SetDeviceGoods(deviceID string, goods []batidos.DeviceGoods) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.goods[deviceID] = append([]batidos.DeviceGoods(nil), goods...)
}

func (g *Batidos) deviceState(deviceID string) *batidosDeviceState {
	state, ok := g.devices[deviceID]
	if !ok {
		state = &batidosDeviceState{}
		g.devices[deviceID] = state
	}
	return state
}

func (g *Batidos) Advance(code string, status int) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if c, ok := g.codes[code]; ok {
		c.status = status
	}
}

func (g *Batidos) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := g.checkSignature(r); err != nil {
		respondBatidos(w, http.StatusUnauthorized, 400, err.Error(), nil)
		return
	}

	switch {
	case r.URL.Path == "/coffee/newThird/active/v2/submit":
		g.submitCampaign(w, r)
	case r.URL.Path == "/coffee/newThird/order/produceCoffee":
		g.produceCoffee(w, r)
	case r.URL.Path == "/coffee/newThird/order/getOrderInfo":
		g.getOrderInfo(w, r)
	case strings.HasPrefix(r.URL.Path, batidosDeviceDetailPath):
		if !requireGet(w, r) {
			return
		}
		g.deviceDetail(w, r)
	case strings.HasPrefix(r.URL.Path, batidosDeviceGoodsPath):
		if !requireGet(w, r) {
			return
		}
		g.deviceGoodsDetail(w, r)
	default:
		respondBatidos(w, http.StatusNotFound, 400, "ruta desconocida", nil)
	}
}

func requireGet(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet {
		return true
	}
	respondBatidos(w, http.StatusMethodNotAllowed, 405, "el endpoint sólo admite GET", nil)
	return false
}

func (g *Batidos) checkSignature(r *http.Request) error {
	key := r.Header.Get("key")
	timestamp := r.Header.Get("timestamp")
	keyMD5 := r.Header.Get("key-md5")

	if key == "" || timestamp == "" || keyMD5 == "" {
		return fmt.Errorf("faltan los headers key, timestamp o key-md5")
	}
	if key != g.key {
		return fmt.Errorf("key desconocida")
	}
	if want := batidos.KeyMD5(g.key, g.secret, timestamp); keyMD5 != want {
		return fmt.Errorf("el key-md5 no coincide")
	}
	return nil
}

type campaignRequest struct {
	ActiveName    string   `json:"activeName"`
	StartTime     string   `json:"startTime"`
	EndTime       string   `json:"endTime"`
	ExchangeType  int      `json:"exchangeType"`
	DeviceTypeID  string   `json:"deviceTypeId"`
	DeviceIDs     []string `json:"deviceIds"`
	NotifyURL     string   `json:"notifyUrl"`
	GoodsItemDtos []struct {
		GoodsID string `json:"goodsId"`
		Qty     int    `json:"qty"`
	} `json:"goodsItemDtos"`
}

func (g *Batidos) submitCampaign(w http.ResponseWriter, r *http.Request) {
	var req campaignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondBatidos(w, http.StatusBadRequest, 400, "cuerpo inválido", nil)
		return
	}

	if req.ActiveName == "" || req.DeviceTypeID == "" {
		respondBatidos(w, http.StatusBadRequest, 400, "activeName y deviceTypeId son obligatorios", nil)
		return
	}

	start, err := batidos.ParseCampaignTime(req.StartTime)
	if err != nil {
		respondBatidos(w, http.StatusBadRequest, 400, "startTime debe tener el formato yyyy-MM-dd HH:mm:ss", nil)
		return
	}
	end, err := batidos.ParseCampaignTime(req.EndTime)
	if err != nil {
		respondBatidos(w, http.StatusBadRequest, 400, "endTime debe tener el formato yyyy-MM-dd HH:mm:ss", nil)
		return
	}
	if !end.After(start) {
		respondBatidos(w, http.StatusBadRequest, 400, "endTime debe ser posterior a startTime", nil)
		return
	}

	var goodsID string
	switch req.ExchangeType {
	case 1:
		if len(req.GoodsItemDtos) == 0 || req.GoodsItemDtos[0].GoodsID == "" {
			respondBatidos(w, http.StatusBadRequest, 400, "exchangeType 1 requiere goodsItemDtos[0].goodsId", nil)
			return
		}
		goodsID = req.GoodsItemDtos[0].GoodsID
	case 2:
	default:
		respondBatidos(w, http.StatusBadRequest, 400, "exchangeType debe ser 1 o 2", nil)
		return
	}

	g.mu.Lock()
	g.next++
	code := fmt.Sprintf("%06d", g.next)
	g.codes[code] = &batidosCode{code: code, goodsID: goodsID}
	activeNo := fmt.Sprintf("AD%06d", g.next)
	g.mu.Unlock()

	respondBatidos(w, http.StatusOK, 200, "操作成功", map[string]any{
		"exchangeCodes": []string{code},
		"activeInfo": map[string]any{
			"activeNo":     activeNo,
			"activeName":   req.ActiveName,
			"startTime":    req.StartTime,
			"endTime":      req.EndTime,
			"exchangeType": req.ExchangeType,
		},
	})
}

type produceRequest struct {
	Code     string `json:"code"`
	DeviceID string `json:"deviceId"`
	GoodsID  string `json:"goodsId"`
	DeviceNo string `json:"deviceNo"`
}

func (g *Batidos) produceCoffee(w http.ResponseWriter, r *http.Request) {
	var req produceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondBatidos(w, http.StatusBadRequest, 400, "cuerpo inválido", nil)
		return
	}
	if req.Code == "" || req.DeviceID == "" || req.GoodsID == "" || req.DeviceNo == "" {
		respondBatidos(w, http.StatusBadRequest, 400, "code, deviceId, goodsId y deviceNo son obligatorios", nil)
		return
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	c, ok := g.codes[req.Code]
	if !ok {
		respondBatidos(w, http.StatusBadRequest, 400, "código de canje desconocido", nil)
		return
	}
	if c.used {
		respondBatidos(w, http.StatusBadRequest, 400, "el código de canje ya fue utilizado", nil)
		return
	}
	if c.goodsID != "" && c.goodsID != req.GoodsID {
		respondBatidos(w, http.StatusBadRequest, 400, "el goodsId no coincide con la campaña", nil)
		return
	}

	g.orders++
	c.used = true
	c.status = 1
	c.deviceID = req.DeviceID
	c.deviceNo = req.DeviceNo
	c.orderNo = fmt.Sprintf("A%06d", g.orders)

	respondBatidos(w, http.StatusOK, 200, "操作成功", true)
}

type orderInfoRequest struct {
	Code     string `json:"code"`
	DeviceID string `json:"deviceId"`
}

func (g *Batidos) getOrderInfo(w http.ResponseWriter, r *http.Request) {
	var req orderInfoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondBatidos(w, http.StatusBadRequest, 400, "cuerpo inválido", nil)
		return
	}
	if req.Code == "" || req.DeviceID == "" {
		respondBatidos(w, http.StatusBadRequest, 400, "code y deviceId son obligatorios", nil)
		return
	}

	g.mu.Lock()
	c, ok := g.codes[req.Code]
	var payload map[string]any
	if ok {
		payload = map[string]any{
			"orderNo":        c.orderNo,
			"deviceNo":       c.deviceNo,
			"deviceName":     c.deviceID,
			"goodsName":      c.goodsID,
			"payAmt":         0,
			"payWay":         6,
			"status":         c.status,
			"outStockStatus": 0,
		}
	}
	g.mu.Unlock()

	if !ok {
		respondBatidos(w, http.StatusBadRequest, 400, "código de canje desconocido", nil)
		return
	}
	respondBatidos(w, http.StatusOK, 200, "操作成功", payload)
}

func (g *Batidos) deviceDetail(w http.ResponseWriter, r *http.Request) {
	deviceID := strings.TrimPrefix(r.URL.Path, batidosDeviceDetailPath)
	if deviceID == "" {
		respondBatidos(w, http.StatusBadRequest, 400, "falta el deviceId en la ruta", nil)
		return
	}

	g.mu.Lock()
	state := g.deviceState(deviceID)
	status, runStatus, scarceStatus := 1, 1, 1
	if state.offline {
		status = 2
	}
	if state.abnormal {
		runStatus = 2
	}
	if state.outOfStock {
		scarceStatus = 3
	}
	g.mu.Unlock()

	respondBatidos(w, http.StatusOK, 200, "操作成功", map[string]any{
		"id":              deviceID,
		"deviceNo":        deviceID,
		"name":            deviceID,
		"deviceTypeId":    batidosDeviceTypeID,
		"status":          status,
		"deviceRunStatus": runStatus,
		"scarceStatus":    scarceStatus,
	})
}

func (g *Batidos) deviceGoodsDetail(w http.ResponseWriter, r *http.Request) {
	deviceID := strings.TrimPrefix(r.URL.Path, batidosDeviceGoodsPath)
	if deviceID == "" {
		respondBatidos(w, http.StatusBadRequest, 400, "falta el deviceId en la ruta", nil)
		return
	}

	g.mu.Lock()
	catalogue := g.goods[deviceID]
	payload := make([]map[string]any, 0, len(catalogue))
	for _, item := range catalogue {
		payload = append(payload, map[string]any{
			"id":      item.ID,
			"no":      item.No,
			"name":    item.Name,
			"price":   item.Price,
			"soldOut": item.SoldOut,
			"listing": item.Listing,
		})
	}
	g.mu.Unlock()

	respondBatidos(w, http.StatusOK, 200, "操作成功", payload)
}

func respondBatidos(w http.ResponseWriter, httpStatus, code int, msg string, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code":    code,
		"success": code == 200,
		"msg":     msg,
		"data":    data,
	})
}
