package snacks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"compras-remotas/internal/dispenser"
)

const shipmentPath = "/open/v7/device/remote/shipment"

const devicesPath = "/open/v7/device/page"

const slotsPath = "/open/v7/device/slotlist"

const defaultTimeout = 10 * time.Second

const maxResponseBytes = 1 << 20

type ShipmentRequest struct {
	PaymentOrderID string

	MID string

	Products []ShipmentProduct
}

type ShipmentProduct struct {
	OrderProductID string

	ProductID string

	RetailPrice float64
}

type OnlineStatus int

const (
	OnlineStatusOffline OnlineStatus = 0
	OnlineStatusOnline  OnlineStatus = 1
)

type SellStatus int

const (
	SellStatusNormal     SellStatus = 1
	SellStatusSuspended  SellStatus = 2
	SellStatusProhibited SellStatus = 3
)

type FaultState int

const (
	FaultStateNormal          FaultState = 0
	FaultStateFault           FaultState = 1
	FaultStateOverTemperature FaultState = 2
)

type LockType int

const (
	LockTypeNormal LockType = 0
)

type StockoutState int

const (
	StockoutStateNormal     StockoutState = 0
	StockoutStateOutOfStock StockoutState = 1
)

type Device struct {
	DeviceCode string

	DeviceName string

	DeviceTypeName string

	OnlineStatus OnlineStatus

	SellStatus SellStatus

	FaultState FaultState

	LockType LockType

	StockoutState StockoutState

	ShortageAlarmNumber int
}

type DevicesPage struct {
	Page int

	PageSize int

	Total int

	TotalPages int

	HasPrevPage bool

	HasNextPage bool

	Items []Device
}

type SlotStatus int

const (
	SlotStatusNormal      SlotStatus = 1
	SlotStatusMalfunction SlotStatus = 2
	SlotStatusDisabled    SlotStatus = 3
	SlotStatusNotFound    SlotStatus = 255
)

type Slot struct {
	SlotID string

	SlotNo string

	SlotStatus SlotStatus

	Stock int

	Capacity int

	ProductID string

	ProductName string

	Price float64
}

type APIError struct {
	HTTPStatus int
	Code       int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("snacks: la API rechazó la llamada (http=%d, code=%d): %s", e.HTTPStatus, e.Code, e.Message)
}

type Option func(*Client)

func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) {
		if httpClient != nil {
			c.httpClient = httpClient
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
	baseURL    string
	accessKey  string
	appSecret  string
	httpClient *http.Client
	now        func() time.Time
}

func NewClient(baseURL, accessKey, appSecret string, opts ...Option) *Client {
	c := &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		accessKey:  accessKey,
		appSecret:  appSecret,
		httpClient: &http.Client{Timeout: defaultTimeout},
		now:        time.Now,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	return c
}

func (c *Client) Ship(ctx context.Context, req ShipmentRequest) (dispenser.Attempt, error) {
	raw, err := json.Marshal(c.body(req))
	if err != nil {
		return dispenser.Attempt{}, fmt.Errorf("snacks: no se pudo serializar el pedido de despacho: %w", err)
	}

	headers, err := SignatureHeaders(c.accessKey, c.appSecret, http.MethodPost, shipmentPath, c.now())
	if err != nil {
		return dispenser.Attempt{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+shipmentPath, bytes.NewReader(raw))
	if err != nil {
		return dispenser.Attempt{}, fmt.Errorf("snacks: no se pudo armar el request de despacho: %w", err)
	}
	for name, value := range headers {
		httpReq.Header.Set(name, value)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return dispenser.Attempt{}, fmt.Errorf("snacks: no se pudo llamar al despacho remoto: %w", err)
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return dispenser.Attempt{}, fmt.Errorf("snacks: no se pudo leer la respuesta del despacho: %w", err)
	}

	var env envelope
	decodeErr := json.Unmarshal(payload, &env)

	rejected := resp.StatusCode < 200 || resp.StatusCode >= 300 || (env.Code != 0 && env.Code != 200)
	if rejected {
		message := env.Message
		if message == "" {
			message = strings.TrimSpace(string(payload))
		}
		return dispenser.Attempt{}, &APIError{
			HTTPStatus: resp.StatusCode,
			Code:       env.Code,
			Message:    message,
		}
	}
	if decodeErr != nil {
		return dispenser.Attempt{}, fmt.Errorf("snacks: la respuesta del despacho no es el JSON esperado: %w", decodeErr)
	}

	return dispenser.Attempt{
		Accepted: true,
		Ref:      subOrderID(env.Result),
		Detail:   "despacho encolado: el resultado real llega por callback",
	}, nil
}

func (c *Client) Devices(ctx context.Context, page, pageSize int, deviceCode string) (DevicesPage, error) {
	query := url.Values{}
	query.Set("Page", strconv.Itoa(page))
	query.Set("PageSize", strconv.Itoa(pageSize))
	if deviceCode != "" {
		query.Set("DeviceCode", deviceCode)
	}

	var result devicesResultBody
	if err := c.get(ctx, devicesPath, query, &result); err != nil {
		return DevicesPage{}, err
	}

	devices := make([]Device, 0, len(result.Items))
	for _, item := range result.Items {
		devices = append(devices, Device{
			DeviceCode:          item.DeviceCode,
			DeviceName:          item.DeviceName,
			DeviceTypeName:      item.DeviceTypeName,
			OnlineStatus:        OnlineStatus(item.OnlineStatus),
			SellStatus:          SellStatus(item.SellStatus),
			FaultState:          FaultState(item.FaultState),
			LockType:            LockType(item.LockType),
			StockoutState:       StockoutState(item.StockoutState),
			ShortageAlarmNumber: item.ShortageAlarmNumber,
		})
	}

	return DevicesPage{
		Page:        result.Page,
		PageSize:    result.PageSize,
		Total:       result.Total,
		TotalPages:  result.TotalPages,
		HasPrevPage: result.HasPrevPage,
		HasNextPage: result.HasNextPage,
		Items:       devices,
	}, nil
}

func (c *Client) Slots(ctx context.Context, deviceCode string) ([]Slot, error) {
	query := url.Values{}
	if deviceCode != "" {
		query.Set("DeviceCode", deviceCode)
	}

	var result []slotItemBody
	if err := c.get(ctx, slotsPath, query, &result); err != nil {
		return nil, err
	}

	slots := make([]Slot, 0, len(result))
	for _, item := range result {
		slots = append(slots, Slot{
			SlotID:      string(item.SlotID),
			SlotNo:      string(item.SlotNo),
			SlotStatus:  SlotStatus(item.SlotStatus),
			Stock:       item.Stock,
			Capacity:    item.Capacity,
			ProductID:   string(item.ProductID),
			ProductName: item.ProductName,
			Price:       item.Price,
		})
	}
	return slots, nil
}

func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	endpoint := c.baseURL + path
	if encoded := query.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}

	headers, err := SignatureHeaders(c.accessKey, c.appSecret, http.MethodGet, path, c.now())
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("snacks: no se pudo armar el request de %s: %w", path, err)
	}
	for name, value := range headers {
		httpReq.Header.Set(name, value)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("snacks: no se pudo consultar %s: %w", path, err)
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("snacks: no se pudo leer la respuesta de %s: %w", path, err)
	}

	var env envelope
	decodeErr := json.Unmarshal(payload, &env)

	rejected := resp.StatusCode < 200 || resp.StatusCode >= 300 || (env.Code != 0 && env.Code != 200)
	if rejected {
		message := env.Message
		if message == "" {
			message = strings.TrimSpace(string(payload))
		}
		return &APIError{
			HTTPStatus: resp.StatusCode,
			Code:       env.Code,
			Message:    message,
		}
	}
	if decodeErr != nil {
		return fmt.Errorf("snacks: la respuesta de %s no es el JSON esperado: %w", path, decodeErr)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(env.Result, out); err != nil {
		return fmt.Errorf("snacks: la respuesta de %s no se pudo interpretar: %w", path, err)
	}
	return nil
}

func (c *Client) body(req ShipmentRequest) shipmentBody {
	body := shipmentBody{
		AccessKey:      c.accessKey,
		PaymentOrderID: req.PaymentOrderID,
		MID:            req.MID,
		Products:       make([]shipmentProductBody, 0, len(req.Products)),
	}
	for _, product := range req.Products {
		item := shipmentProductBody{
			OrderProductID: product.OrderProductID,
			ProductID:      product.ProductID,
		}
		item.ItemPricing.RetailPrice = product.RetailPrice
		body.Products = append(body.Products, item)
	}
	return body
}

func subOrderID(result json.RawMessage) string {
	if len(result) == 0 {
		return ""
	}

	var data struct {
		SubOrderID string `json:"subOrderId"`
		SubOrderNo string `json:"subOrderNo"`
	}
	if err := json.Unmarshal(result, &data); err != nil {
		return ""
	}
	if data.SubOrderID != "" {
		return data.SubOrderID
	}
	return data.SubOrderNo
}
