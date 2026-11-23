package mercadopago

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"compras-remotas/internal/httpx"
	"compras-remotas/internal/payments"
)

const DefaultBaseURL = "https://api.mercadopago.com"

const ordersPath = "/v1/orders"

const defaultTimeout = 10 * time.Second

const maxResponseBytes = 1 << 20

const maxErrorMessage = 200

var _ payments.Gateway = (*Client)(nil)

type APIError struct {
	HTTPStatus int
	Code       string
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("mercadopago: la API rechazó la llamada (http=%d, code=%s): %s", e.HTTPStatus, e.Code, e.Message)
}

type Option func(*Client)

func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) {
		if httpClient != nil {
			c.httpClient = httpClient
		}
	}
}

type Client struct {
	baseURL     string
	accessToken string
	httpClient  *http.Client
}

func NewClient(baseURL, accessToken string, opts ...Option) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	c := &Client{
		baseURL:     strings.TrimRight(baseURL, "/"),
		accessToken: accessToken,
		httpClient:  &http.Client{Timeout: defaultTimeout},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	return c
}

func (c *Client) CreateQR(ctx context.Context, req payments.QRRequest) (payments.Charge, error) {
	if err := validateQRRequest(req); err != nil {
		return payments.Charge{}, err
	}

	amount := formatAmount(req.AmountCents)
	body := createOrderBody{
		Type:              "qr",
		TotalAmount:       amount,
		ExternalReference: req.OrderID,
		ExpirationTime:    formatExpiration(req.ExpiresIn),
		Config:            orderConfigBody{QR: qrConfigBody{ExternalPosID: req.PosID, Mode: "dynamic"}},
		Transactions:      transactionsBody{Payments: []paymentAmountBody{{Amount: amount}}},
		Items:             []orderItemBody{{Title: req.Title, UnitPrice: amount, Quantity: 1, UnitMeasure: "unit"}},
	}

	charge, err := c.call(ctx, http.MethodPost, ordersPath, req.OrderID, body)
	if err != nil {
		return payments.Charge{}, err
	}
	if charge.QRData == "" {
		return payments.Charge{}, fmt.Errorf("mercadopago: la orden %s volvió sin qr_data", charge.ProviderID)
	}
	if charge.ExternalRef != req.OrderID {
		return payments.Charge{}, fmt.Errorf("mercadopago: la orden %s volvió con external_reference %q y se pidió %q", charge.ProviderID, charge.ExternalRef, req.OrderID)
	}
	return charge, nil
}

func (c *Client) Lookup(ctx context.Context, providerID string) (payments.Charge, error) {
	if providerID == "" {
		return payments.Charge{}, errors.New("mercadopago: falta el id de la orden")
	}
	return c.call(ctx, http.MethodGet, ordersPath+"/"+url.PathEscape(providerID), "", nil)
}

func (c *Client) Cancel(ctx context.Context, providerID string) (payments.Charge, error) {
	if providerID == "" {
		return payments.Charge{}, errors.New("mercadopago: falta el id de la orden")
	}
	charge, err := c.call(ctx, http.MethodPost, ordersPath+"/"+url.PathEscape(providerID)+"/cancel", "cancel-"+providerID, nil)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.HTTPStatus == http.StatusConflict {
		return payments.Charge{}, fmt.Errorf("%w: %w", payments.ErrNotCancelable, err)
	}
	return charge, err
}

func validateQRRequest(req payments.QRRequest) error {
	switch {
	case req.OrderID == "" || len(req.OrderID) > 64 || !validExternalRef(req.OrderID):
		return fmt.Errorf("mercadopago: id de orden inválido %q (hasta 64 caracteres [A-Za-z0-9_-])", req.OrderID)
	case req.PosID == "":
		return errors.New("mercadopago: falta el external_pos_id de la caja")
	case req.AmountCents <= 0:
		return fmt.Errorf("mercadopago: monto inválido %d", req.AmountCents)
	case req.Title == "":
		return errors.New("mercadopago: falta el título del ítem")
	case req.ExpiresIn < time.Second:
		return fmt.Errorf("mercadopago: vencimiento inválido %s", req.ExpiresIn)
	}
	return nil
}

func validExternalRef(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func (c *Client) call(ctx context.Context, method, path, idempotencyKey string, body any) (payments.Charge, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return payments.Charge{}, fmt.Errorf("mercadopago: no se pudo serializar el pedido: %w", err)
		}
		reader = bytes.NewReader(raw)
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return payments.Charge{}, fmt.Errorf("mercadopago: no se pudo armar el request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.accessToken)
	httpReq.Header.Set("Accept", "application/json")
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		httpReq.Header.Set("X-Idempotency-Key", idempotencyKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return payments.Charge{}, fmt.Errorf("mercadopago: %s %s falló: %w", method, path, err)
	}
	defer resp.Body.Close()

	raw, err := httpx.ReadLimited(resp.Body, maxResponseBytes)
	if err != nil {
		return payments.Charge{}, fmt.Errorf("mercadopago: no se pudo leer la respuesta: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return payments.Charge{}, fmt.Errorf("%w: %w", payments.ErrNotFound, apiError(resp.StatusCode, raw))
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return payments.Charge{}, apiError(resp.StatusCode, raw)
	}

	var order orderBody
	if err := json.Unmarshal(raw, &order); err != nil {
		return payments.Charge{}, fmt.Errorf("mercadopago: respuesta ilegible: %w", err)
	}
	return toCharge(order)
}

func apiError(status int, raw []byte) *APIError {
	e := &APIError{HTTPStatus: status}
	var parsed errorBody
	if json.Unmarshal(raw, &parsed) == nil && len(parsed.Errors) > 0 {
		e.Code = parsed.Errors[0].Code
		e.Message = parsed.Errors[0].Message
	} else {
		e.Message = string(raw)
	}
	if len(e.Message) > maxErrorMessage {
		e.Message = e.Message[:maxErrorMessage] + "…"
	}
	return e
}

func toCharge(order orderBody) (payments.Charge, error) {
	if order.ID == "" {
		return payments.Charge{}, errors.New("mercadopago: la respuesta no trae el id de la orden")
	}

	amount, err := parseAmount(order.TotalAmount)
	if err != nil {
		return payments.Charge{}, fmt.Errorf("mercadopago: total_amount de %s: %w", order.ID, err)
	}
	paid, err := paidAmount(order)
	if err != nil {
		return payments.Charge{}, fmt.Errorf("mercadopago: monto pagado de %s: %w", order.ID, err)
	}

	charge := payments.Charge{
		ProviderID:           order.ID,
		ExternalRef:          order.ExternalReference,
		Status:               mapStatus(order.Status),
		ProviderStatus:       order.Status,
		ProviderStatusDetail: order.StatusDetail,
		AmountCents:          amount,
		PaidAmountCents:      paid,
	}
	if order.TypeResponse != nil {
		charge.QRData = order.TypeResponse.QRData
	}
	return charge, nil
}

func paidAmount(order orderBody) (int64, error) {
	if order.TotalPaidAmount != "" {
		return parseAmount(order.TotalPaidAmount)
	}
	var total int64
	for _, p := range order.Transactions.Payments {
		if p.Status != "processed" {
			continue
		}
		raw := p.PaidAmount
		if raw == "" {
			raw = p.Amount
		}
		cents, err := parseAmount(raw)
		if err != nil {
			return 0, err
		}
		total += cents
	}
	return total, nil
}
