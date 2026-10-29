// Package fakeapi provee sustitutos locales de los dos backends de máquinas.
//
// Existen para poder ejercitar el flujo completo sin credenciales ni máquinas
// reales, y para que el contrato de red (firmas, reloj, códigos de un solo uso,
// replay de nonce) falle en un test y no delante de un cliente.
//
// Reflejan lo que dice la documentación. Donde la documentación calla, el falso
// no inventa comportamiento: queda anotado en el código.
package fakeapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"compras-remotas/internal/dispenser/batidos"
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

// Batidos es el sustituto local de la API de comercio de Batidos.
type Batidos struct {
	key    string
	secret string

	mu     sync.Mutex
	next   int
	orders int
	codes  map[string]*batidosCode
}

func NewBatidos(key, secret string) *Batidos {
	return &Batidos{key: key, secret: secret, codes: make(map[string]*batidosCode)}
}

// Advance simula el callback del proveedor moviendo una orden a un estado nuevo
// (1 preparando, 2 completado, 0 falló).
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

	switch r.URL.Path {
	case "/coffee/newThird/active/v2/submit":
		g.submitCampaign(w, r)
	case "/coffee/newThird/order/produceCoffee":
		g.produceCoffee(w, r)
	case "/coffee/newThird/order/getOrderInfo":
		g.getOrderInfo(w, r)
	default:
		respondBatidos(w, http.StatusNotFound, 400, "ruta desconocida", nil)
	}
}

// checkSignature replica la autenticación documentada: key, timestamp y
// MD5(key+secret+timestamp). La documentación no describe una ventana de tiempo
// para esta API, así que acá no se exige ninguna.
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

	// El proveedor lee estas dos fechas como UTC+8. Una ventana construida con
	// hora local es una ventana en el futuro, así que se valida a propósito.
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

	// "操作成功" es literalmente el mensaje que devuelve el proveedor: se
	// conserva para que el falso se parezca al original.
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
	c.status = 1 // preparando: aceptado no es entregado
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
