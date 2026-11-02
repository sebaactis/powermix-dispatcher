package snacks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"compras-remotas/internal/dispenser"
)

const shipmentPath = "/open/v7/device/remote/shipment"

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

type APIError struct {
	HTTPStatus int
	Code       int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("snacks: la API rechazó el despacho (http=%d, code=%d): %s", e.HTTPStatus, e.Code, e.Message)
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
