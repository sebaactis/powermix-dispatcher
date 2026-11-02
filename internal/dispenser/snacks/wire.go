package snacks

import "encoding/json"

type shipmentBody struct {
	AccessKey      string                `json:"AccessKey"`
	PaymentOrderID string                `json:"PaymentOrderId"`
	MID            string                `json:"Mid"`
	Products       []shipmentProductBody `json:"DeviceRemoteShipmentProducts"`
}

type shipmentProductBody struct {
	OrderProductID string `json:"OrderProductId"`
	ProductID      string `json:"ProductId"`
	ItemPricing    struct {
		RetailPrice float64 `json:"retailPrice"`
	} `json:"ItemPricing"`
}

type envelope struct {
	Success bool            `json:"success"`
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Result  json.RawMessage `json:"result"`
}
