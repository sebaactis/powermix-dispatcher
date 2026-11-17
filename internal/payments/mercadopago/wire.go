package mercadopago

type createOrderBody struct {
	Type              string           `json:"type"`
	TotalAmount       string           `json:"total_amount"`
	ExternalReference string           `json:"external_reference"`
	ExpirationTime    string           `json:"expiration_time"`
	Config            orderConfigBody  `json:"config"`
	Transactions      transactionsBody `json:"transactions"`
	Items             []orderItemBody  `json:"items"`
}

type orderConfigBody struct {
	QR qrConfigBody `json:"qr"`
}

type qrConfigBody struct {
	ExternalPosID string `json:"external_pos_id"`
	Mode          string `json:"mode"`
}

type transactionsBody struct {
	Payments []paymentAmountBody `json:"payments"`
}

type paymentAmountBody struct {
	Amount string `json:"amount"`
}

type orderItemBody struct {
	Title       string `json:"title"`
	UnitPrice   string `json:"unit_price"`
	Quantity    int    `json:"quantity"`
	UnitMeasure string `json:"unit_measure"`
}

type orderBody struct {
	ID                string                `json:"id"`
	ExternalReference string                `json:"external_reference"`
	Status            string                `json:"status"`
	StatusDetail      string                `json:"status_detail"`
	TotalAmount       string                `json:"total_amount"`
	TotalPaidAmount   string                `json:"total_paid_amount"`
	TypeResponse      *typeResponseBody     `json:"type_response"`
	Transactions      orderTransactionsBody `json:"transactions"`
}

type typeResponseBody struct {
	QRData string `json:"qr_data"`
}

type orderTransactionsBody struct {
	Payments []orderPaymentBody `json:"payments"`
}

type orderPaymentBody struct {
	ID           string `json:"id"`
	Amount       string `json:"amount"`
	PaidAmount   string `json:"paid_amount"`
	Status       string `json:"status"`
	StatusDetail string `json:"status_detail"`
}

type errorBody struct {
	Errors []errorItemBody `json:"errors"`
}

type errorItemBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
