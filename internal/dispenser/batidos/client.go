package batidos

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"compras-remotas/internal/dispenser"
)

const (
	campaignPath    = "/coffee/newThird/active/v2/submit"
	producePath     = "/coffee/newThird/order/produceCoffee"
	orderInfoPath   = "/coffee/newThird/order/getOrderInfo"
	devicePath      = "/coffee/newThird/device/detail/"
	deviceGoodsPath = "/coffee/newThird/goods/deviceGoodsDetail/"
)

const (
	successCode = 200

	defaultTimeout = 10 * time.Second

	maxResponseBytes = 1 << 20
)

type Option func(*Client)

func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.http = hc
		}
	}
}

func WithClock(now func() time.Time) Option {
	return func(c *Client) {
		if now != nil {
			c.now = now
		}
	}
}

type Client struct {
	baseURL string
	key     string
	secret  string
	http    *http.Client
	now     func() time.Time
}

func NewClient(baseURL, key, secret string, opts ...Option) *Client {
	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		key:     key,
		secret:  secret,
		http:    &http.Client{Timeout: defaultTimeout},
		now:     time.Now,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

type CampaignRequest struct {
	ActiveName string

	Start        time.Time
	End          time.Time
	DeviceTypeID string
	DeviceIDs    []string

	ExchangeType int
	GoodsIDs     []string
	NotifyURL    string
}

type Campaign struct {
	ActiveNo      string
	ActiveName    string
	StartTime     string
	EndTime       string
	ExchangeType  int
	ExchangeCodes []string
}

type ProduceRequest struct {
	Code     string
	DeviceID string
	GoodsID  string
	DeviceNo string
}

type OrderStatus int

const (
	OrderStatusFailed     OrderStatus = 0
	OrderStatusInProgress OrderStatus = 1
	OrderStatusCompleted  OrderStatus = 2
)

type OrderInfo struct {
	OrderNo   string
	DeviceNo  string
	GoodsName string
	Status    OrderStatus

	OutStockStatus int
}

type DeviceStatus int

const (
	DeviceStatusOnline  DeviceStatus = 1
	DeviceStatusOffline DeviceStatus = 2
)

type DeviceRunStatus int

const (
	DeviceRunStatusNormal   DeviceRunStatus = 1
	DeviceRunStatusAbnormal DeviceRunStatus = 2
)

type ScarceStatus int

const (
	ScarceStatusNormal     ScarceStatus = 1
	ScarceStatusOutOfStock ScarceStatus = 3
)

type Device struct {
	ID           string
	DeviceNo     string
	Name         string
	DeviceTypeID string

	Status          DeviceStatus
	DeviceRunStatus DeviceRunStatus
	ScarceStatus    ScarceStatus
}

type DeviceGoods struct {
	ID   string
	No   string
	Name string

	Price   float64
	SoldOut int
	Listing bool
}

type APIError struct {
	HTTPStatus int
	Code       int
	Message    string
}

func (e *APIError) Error() string {
	message := e.Message
	if message == "" {
		message = "sin mensaje del proveedor"
	}
	return fmt.Sprintf("batidos: la API respondió code=%d (http %d): %s", e.Code, e.HTTPStatus, message)
}

func (c *Client) CreateCampaign(ctx context.Context, req CampaignRequest) (Campaign, error) {
	body := campaignWireRequest{
		ActiveName: req.ActiveName,

		StartTime:     CampaignTime(req.Start),
		EndTime:       CampaignTime(req.End),
		ExchangeType:  req.ExchangeType,
		DeviceTypeID:  req.DeviceTypeID,
		DeviceIDs:     req.DeviceIDs,
		NotifyURL:     req.NotifyURL,
		GoodsItemDtos: make([]goodsItemWire, 0, len(req.GoodsIDs)),
	}
	for _, goodsID := range req.GoodsIDs {
		body.GoodsItemDtos = append(body.GoodsItemDtos, goodsItemWire{GoodsID: goodsID, Qty: 1})
	}

	var data campaignWireResponse
	if err := c.do(ctx, campaignPath, body, &data); err != nil {
		return Campaign{}, err
	}
	if len(data.ExchangeCodes) == 0 {

		return Campaign{}, fmt.Errorf("la campaña se creó sin código de canje")
	}

	return Campaign{
		ActiveNo:      data.ActiveInfo.ActiveNo,
		ActiveName:    data.ActiveInfo.ActiveName,
		StartTime:     data.ActiveInfo.StartTime,
		EndTime:       data.ActiveInfo.EndTime,
		ExchangeType:  data.ActiveInfo.ExchangeType,
		ExchangeCodes: data.ExchangeCodes,
	}, nil
}

func (c *Client) ProduceCoffee(ctx context.Context, req ProduceRequest) (dispenser.Attempt, error) {
	body := produceWireRequest{
		Code:     req.Code,
		DeviceID: req.DeviceID,
		GoodsID:  req.GoodsID,
		DeviceNo: req.DeviceNo,
	}

	var accepted bool
	if err := c.do(ctx, producePath, body, &accepted); err != nil {
		return dispenser.Attempt{}, err
	}
	if !accepted {
		return dispenser.Attempt{Accepted: false, Detail: "el proveedor no tomó el pedido"},
			fmt.Errorf("el proveedor no tomó el pedido (data=false)")
	}

	return dispenser.Attempt{Accepted: true, Detail: "pedido tomado por la máquina"}, nil
}

func (c *Client) OrderInfo(ctx context.Context, exchangeCode, deviceID string) (OrderInfo, error) {
	body := orderInfoWireRequest{Code: exchangeCode, DeviceID: deviceID}

	var data orderInfoWireResponse
	if err := c.do(ctx, orderInfoPath, body, &data); err != nil {
		return OrderInfo{}, err
	}

	return OrderInfo{
		OrderNo:        data.OrderNo,
		DeviceNo:       data.DeviceNo,
		GoodsName:      data.GoodsName,
		Status:         OrderStatus(data.Status),
		OutStockStatus: data.OutStockStatus,
	}, nil
}

func (c *Client) Device(ctx context.Context, deviceID string) (Device, error) {
	var data deviceWireResponse
	if err := c.get(ctx, devicePath+url.PathEscape(deviceID), &data); err != nil {
		return Device{}, err
	}

	return Device{
		ID:              data.ID,
		DeviceNo:        data.DeviceNo,
		Name:            data.Name,
		DeviceTypeID:    data.DeviceTypeID,
		Status:          DeviceStatus(data.Status),
		DeviceRunStatus: DeviceRunStatus(data.DeviceRunStatus),
		ScarceStatus:    ScarceStatus(data.ScarceStatus),
	}, nil
}

func (c *Client) DeviceGoods(ctx context.Context, deviceID string) ([]DeviceGoods, error) {
	var data []deviceGoodsWireResponse
	if err := c.get(ctx, deviceGoodsPath+url.PathEscape(deviceID), &data); err != nil {
		return nil, err
	}

	goods := make([]DeviceGoods, 0, len(data))
	for _, item := range data {
		goods = append(goods, DeviceGoods{
			ID:      item.ID,
			No:      item.No,
			Name:    item.Name,
			Price:   item.Price,
			SoldOut: item.SoldOut,
			Listing: item.Listing,
		})
	}
	return goods, nil
}

func (c *Client) do(ctx context.Context, path string, body, out any) error {
	return c.send(ctx, http.MethodPost, path, body, out)
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	return c.send(ctx, http.MethodGet, path, nil, out)
}

func (c *Client) send(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("no se pudo serializar el request de %s: %w", path, err)
		}
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("no se pudo armar el request de %s: %w", path, err)
	}

	for name, value := range SignatureHeaders(c.key, c.secret, c.now()) {
		req.Header.Set(name, value)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {

		return fmt.Errorf("falló la llamada a %s: %w", path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("no se pudo leer la respuesta de %s: %w", path, err)
	}

	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return &APIError{
			HTTPStatus: resp.StatusCode,
			Message:    "la respuesta no es un sobre JSON válido: " + err.Error(),
		}
	}
	if env.Code == nil {
		return &APIError{HTTPStatus: resp.StatusCode, Message: "la respuesta no trae el campo code"}
	}
	if *env.Code != successCode || resp.StatusCode != http.StatusOK {
		return &APIError{HTTPStatus: resp.StatusCode, Code: *env.Code, Message: env.Msg}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("no se pudo leer data de %s: %w", path, err)
	}
	return nil
}
