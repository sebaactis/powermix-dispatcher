package fakeapi

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"compras-remotas/internal/payments/mercadopago"
)

const (
	mpOrdersPath       = "/v1/orders"
	mpDefaultExpiry    = 15 * time.Minute
	mpMaxBodyBytes     = 64 << 10
	mpWebhookTimeout   = 5 * time.Second
	mpStatusCreated    = "created"
	mpStatusProcessed  = "processed"
	mpStatusExpired    = "expired"
	mpStatusCanceled   = "canceled"
	mpDetailAccredited = "accredited"
)

var errMPNotFound = errors.New("la orden no existe en el Mercado Pago falso")

var errMPNotPayable = errors.New("la orden no está en created")

type mpOrder struct {
	id             string
	externalRef    string
	posID          string
	totalCents     int64
	paidCents      int64
	expirationTime string
	status         string
	statusDetail   string
	createdAt      time.Time
	expiresAt      time.Time
}

type MercadoPago struct {
	accessToken   string
	webhookSecret string
	webhookURL    string
	httpClient    *http.Client
	now           func() time.Time

	mu         sync.Mutex
	next       int
	orders     map[string]*mpOrder
	byIdemKey  map[string]string
	byExternal map[string]string
}

func NewMercadoPago(accessToken, webhookSecret, webhookURL string) *MercadoPago {
	return &MercadoPago{
		accessToken:   accessToken,
		webhookSecret: webhookSecret,
		webhookURL:    webhookURL,
		httpClient:    &http.Client{Timeout: mpWebhookTimeout},
		now:           time.Now,
		orders:        make(map[string]*mpOrder),
		byIdemKey:     make(map[string]string),
		byExternal:    make(map[string]string),
	}
}

func (m *MercadoPago) SetClock(now func() time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if now != nil {
		m.now = now
	}
}

func (m *MercadoPago) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+m.accessToken {
		respondMPError(w, http.StatusUnauthorized, "unauthorized", "invalid access token")
		return
	}

	rest, ok := strings.CutPrefix(r.URL.Path, mpOrdersPath)
	switch {
	case ok && rest == "" && r.Method == http.MethodPost:
		m.createOrder(w, r)
	case ok && strings.HasPrefix(rest, "/") && strings.HasSuffix(rest, "/cancel") && r.Method == http.MethodPost:
		m.cancelOrder(w, r, strings.TrimSuffix(strings.TrimPrefix(rest, "/"), "/cancel"))
	case ok && strings.HasPrefix(rest, "/") && !strings.Contains(rest[1:], "/") && r.Method == http.MethodGet:
		m.getOrder(w, strings.TrimPrefix(rest, "/"))
	default:
		respondMPError(w, http.StatusNotFound, "not_found", "unknown route")
	}
}

type mpCreateBody struct {
	Type              string `json:"type"`
	TotalAmount       string `json:"total_amount"`
	ExternalReference string `json:"external_reference"`
	ExpirationTime    string `json:"expiration_time"`
	Config            struct {
		QR struct {
			ExternalPosID string `json:"external_pos_id"`
			Mode          string `json:"mode"`
		} `json:"qr"`
	} `json:"config"`
	Transactions struct {
		Payments []struct {
			Amount string `json:"amount"`
		} `json:"payments"`
	} `json:"transactions"`
	Items json.RawMessage `json:"items"`
}

func (m *MercadoPago) createOrder(w http.ResponseWriter, r *http.Request) {
	idemKey := r.Header.Get("X-Idempotency-Key")
	if idemKey == "" {
		respondMPError(w, http.StatusBadRequest, "idempotency_key_required", "X-Idempotency-Key is required")
		return
	}

	var body mpCreateBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, mpMaxBodyBytes)).Decode(&body); err != nil {
		respondMPError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}

	total, err := mpParseAmount(body.TotalAmount)
	switch {
	case body.Type != "qr":
		respondMPError(w, http.StatusBadRequest, "invalid_type", "type must be qr")
		return
	case body.Config.QR.Mode != "dynamic":
		respondMPError(w, http.StatusBadRequest, "invalid_mode", "the fake only supports dynamic QR")
		return
	case body.Config.QR.ExternalPosID == "":
		respondMPError(w, http.StatusBadRequest, "pos_required", "config.qr.external_pos_id is required")
		return
	case body.ExternalReference == "":
		respondMPError(w, http.StatusBadRequest, "external_reference_required", "external_reference is required")
		return
	case err != nil || total <= 0:
		respondMPError(w, http.StatusBadRequest, "invalid_total_amount", "total_amount must be a positive decimal")
		return
	case len(body.Transactions.Payments) != 1 || body.Transactions.Payments[0].Amount != body.TotalAmount:
		respondMPError(w, http.StatusBadRequest, "invalid_payments", "a single payment for total_amount is required")
		return
	}

	expiry := mpDefaultExpiry
	if body.ExpirationTime != "" {
		if expiry, err = mpParseISODuration(body.ExpirationTime); err != nil {
			respondMPError(w, http.StatusBadRequest, "invalid_expiration_time", err.Error())
			return
		}
	}

	m.mu.Lock()
	if id, exists := m.byIdemKey[idemKey]; exists {
		view := m.viewLocked(m.orders[id])
		m.mu.Unlock()
		respondMP(w, http.StatusCreated, view)
		return
	}
	m.next++
	now := m.now()
	order := &mpOrder{
		id:             fmt.Sprintf("ORDFAKE%022d", m.next),
		externalRef:    body.ExternalReference,
		posID:          body.Config.QR.ExternalPosID,
		totalCents:     total,
		expirationTime: body.ExpirationTime,
		status:         mpStatusCreated,
		statusDetail:   mpStatusCreated,
		createdAt:      now,
		expiresAt:      now.Add(expiry),
	}
	m.orders[order.id] = order
	m.byIdemKey[idemKey] = order.id
	m.byExternal[order.externalRef] = order.id
	view := m.viewLocked(order)
	m.mu.Unlock()

	respondMP(w, http.StatusCreated, view)
}

func (m *MercadoPago) getOrder(w http.ResponseWriter, id string) {
	m.mu.Lock()
	order, exists := m.orders[id]
	var view mpOrderView
	if exists {
		view = m.viewLocked(order)
	}
	m.mu.Unlock()

	if !exists {
		respondMPError(w, http.StatusNotFound, "order_not_found", "order not found")
		return
	}
	respondMP(w, http.StatusOK, view)
}

func (m *MercadoPago) cancelOrder(w http.ResponseWriter, r *http.Request, id string) {
	if r.Header.Get("X-Idempotency-Key") == "" {
		respondMPError(w, http.StatusBadRequest, "idempotency_key_required", "X-Idempotency-Key is required")
		return
	}

	m.mu.Lock()
	order, exists := m.orders[id]
	if !exists {
		m.mu.Unlock()
		respondMPError(w, http.StatusNotFound, "order_not_found", "order not found")
		return
	}
	m.expireLocked(order)
	switch order.status {
	case mpStatusCreated:
		order.status, order.statusDetail = mpStatusCanceled, mpStatusCanceled
	case mpStatusCanceled:
	default:
		status := order.status
		m.mu.Unlock()
		respondMPError(w, http.StatusConflict, "order_not_cancelable", "order is "+status)
		return
	}
	view := m.viewLocked(order)
	m.mu.Unlock()

	respondMP(w, http.StatusOK, view)
}

func (m *MercadoPago) expireLocked(order *mpOrder) {
	if order.status == mpStatusCreated && m.now().After(order.expiresAt) {
		order.status, order.statusDetail = mpStatusExpired, mpStatusExpired
	}
}

func (m *MercadoPago) resolveLocked(ref string) (*mpOrder, error) {
	if order, ok := m.orders[ref]; ok {
		return order, nil
	}
	if id, ok := m.byExternal[ref]; ok {
		return m.orders[id], nil
	}
	return nil, fmt.Errorf("%w: %q", errMPNotFound, ref)
}

func (m *MercadoPago) Pay(ref string, paidCents int64) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	order, err := m.resolveLocked(ref)
	if err != nil {
		return "", err
	}
	m.expireLocked(order)
	if order.status != mpStatusCreated {
		return "", fmt.Errorf("%w: está en %s", errMPNotPayable, order.status)
	}
	if paidCents <= 0 {
		paidCents = order.totalCents
	}
	order.status, order.statusDetail = mpStatusProcessed, mpDetailAccredited
	order.paidCents = paidCents
	return order.id, nil
}

func (m *MercadoPago) Expire(ref string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	order, err := m.resolveLocked(ref)
	if err != nil {
		return "", err
	}
	if order.status != mpStatusCreated {
		return "", fmt.Errorf("%w: está en %s", errMPNotPayable, order.status)
	}
	order.status, order.statusDetail = mpStatusExpired, mpStatusExpired
	return order.id, nil
}

type WebhookDelivery struct {
	Sent       bool   `json:"sent"`
	URL        string `json:"url,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty"`
	Error      string `json:"error,omitempty"`
}

func (m *MercadoPago) Notify(ref string) (WebhookDelivery, error) {
	m.mu.Lock()
	order, err := m.resolveLocked(ref)
	if err != nil {
		m.mu.Unlock()
		return WebhookDelivery{}, err
	}
	m.expireLocked(order)
	id, status, detail := order.id, order.status, order.statusDetail
	at := m.now()
	m.mu.Unlock()

	if m.webhookURL == "" {
		return WebhookDelivery{Error: "sin -mp-webhook-url: no se envió el aviso"}, nil
	}

	target, err := url.Parse(m.webhookURL)
	if err != nil {
		return WebhookDelivery{}, fmt.Errorf("la URL del webhook es inválida: %w", err)
	}
	query := target.Query()
	query.Set("data.id", id)
	query.Set("type", "order")
	target.RawQuery = query.Encode()

	requestID := mpRequestID()
	payload, _ := json.Marshal(map[string]any{
		"action":      "order." + status,
		"api_version": "v1",
		"type":        "order",
		"live_mode":   false,
		"data":        map[string]any{"id": id, "status": status, "status_detail": detail},
	})

	req, err := http.NewRequest(http.MethodPost, target.String(), bytes.NewReader(payload))
	if err != nil {
		return WebhookDelivery{}, fmt.Errorf("no se pudo armar el aviso: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Id", requestID)
	req.Header.Set("X-Signature", mercadopago.SignatureHeader(m.webhookSecret, requestID, id, at))

	delivery := WebhookDelivery{URL: mpRedactURL(target)}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		delivery.Error = err.Error()
		return delivery, nil
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, mpMaxBodyBytes))

	delivery.Sent = true
	delivery.HTTPStatus = resp.StatusCode
	return delivery, nil
}

func mpRedactURL(u *url.URL) string {
	redacted := *u
	segments := strings.Split(redacted.Path, "/")
	if len(segments) > 0 && segments[len(segments)-1] != "" {
		segments[len(segments)-1] = "{secret}"
	}
	redacted.Path = strings.Join(segments, "/")
	redacted.RawPath = ""
	return strings.Replace(redacted.String(), "%7Bsecret%7D", "{secret}", 1)
}

func mpRequestID() string {
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}

type mpOrderView struct {
	ID                string `json:"id"`
	Type              string `json:"type"`
	ExternalReference string `json:"external_reference"`
	TotalAmount       string `json:"total_amount"`
	TotalPaidAmount   string `json:"total_paid_amount,omitempty"`
	ExpirationTime    string `json:"expiration_time,omitempty"`
	Status            string `json:"status"`
	StatusDetail      string `json:"status_detail"`
	CreatedDate       string `json:"created_date"`
	Config            struct {
		QR struct {
			ExternalPosID string `json:"external_pos_id"`
			Mode          string `json:"mode"`
		} `json:"qr"`
	} `json:"config"`
	TypeResponse struct {
		QRData string `json:"qr_data"`
	} `json:"type_response"`
	Transactions struct {
		Payments []mpPaymentView `json:"payments"`
	} `json:"transactions"`
}

type mpPaymentView struct {
	ID           string `json:"id"`
	Amount       string `json:"amount"`
	PaidAmount   string `json:"paid_amount,omitempty"`
	Status       string `json:"status"`
	StatusDetail string `json:"status_detail"`
}

func (m *MercadoPago) viewLocked(order *mpOrder) mpOrderView {
	m.expireLocked(order)

	var v mpOrderView
	v.ID = order.id
	v.Type = "qr"
	v.ExternalReference = order.externalRef
	v.TotalAmount = mpFormatAmount(order.totalCents)
	v.ExpirationTime = order.expirationTime
	v.Status = order.status
	v.StatusDetail = order.statusDetail
	v.CreatedDate = order.createdAt.UTC().Format(time.RFC3339Nano)
	v.Config.QR.ExternalPosID = order.posID
	v.Config.QR.Mode = "dynamic"
	v.TypeResponse.QRData = "00020101021243650016COM.MERCADOLIBRE0201306" + order.id

	payment := mpPaymentView{
		ID:           "PAY" + strings.TrimPrefix(order.id, "ORD"),
		Amount:       v.TotalAmount,
		Status:       order.status,
		StatusDetail: order.statusDetail,
	}
	if order.status == mpStatusProcessed {
		v.TotalPaidAmount = mpFormatAmount(order.paidCents)
		payment.PaidAmount = v.TotalPaidAmount
	}
	v.Transactions.Payments = []mpPaymentView{payment}
	return v
}

func mpFormatAmount(cents int64) string {
	return fmt.Sprintf("%d.%02d", cents/100, cents%100)
}

func mpParseAmount(raw string) (int64, error) {
	whole, frac, hasFrac := strings.Cut(raw, ".")
	units, err := strconv.ParseUint(whole, 10, 62)
	if err != nil || (hasFrac && (len(frac) == 0 || len(frac) > 2)) {
		return 0, fmt.Errorf("monto inválido %q", raw)
	}
	var cents uint64
	if hasFrac {
		if cents, err = strconv.ParseUint(frac+strings.Repeat("0", 2-len(frac)), 10, 8); err != nil {
			return 0, fmt.Errorf("monto inválido %q", raw)
		}
	}
	return int64(units*100 + cents), nil
}

func mpParseISODuration(raw string) (time.Duration, error) {
	rest, ok := strings.CutPrefix(raw, "PT")
	if !ok || rest == "" {
		return 0, fmt.Errorf("expiration_time inválido %q", raw)
	}
	var total time.Duration
	for rest != "" {
		i := strings.IndexAny(rest, "HMS")
		if i <= 0 {
			return 0, fmt.Errorf("expiration_time inválido %q", raw)
		}
		n, err := strconv.Atoi(rest[:i])
		if err != nil || n < 0 {
			return 0, fmt.Errorf("expiration_time inválido %q", raw)
		}
		unit := map[byte]time.Duration{'H': time.Hour, 'M': time.Minute, 'S': time.Second}[rest[i]]
		total += time.Duration(n) * unit
		rest = rest[i+1:]
	}
	return total, nil
}

func respondMP(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func respondMPError(w http.ResponseWriter, status int, code, message string) {
	respondMP(w, status, map[string]any{
		"errors": []map[string]any{{"code": code, "message": message, "details": []string{}}},
	})
}
