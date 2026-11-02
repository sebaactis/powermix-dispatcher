package snacks

import (
	"encoding/json"
	"strings"
)

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

type devicesResultBody struct {
	Page        int              `json:"page"`
	PageSize    int              `json:"pageSize"`
	Total       int              `json:"total"`
	TotalPages  int              `json:"totalPages"`
	Items       []deviceItemBody `json:"items"`
	HasPrevPage bool             `json:"hasPrevPage"`
	HasNextPage bool             `json:"hasNextPage"`
}

type deviceItemBody struct {
	DeviceCode          string `json:"deviceCode"`
	DeviceName          string `json:"deviceName"`
	DeviceTypeName      string `json:"deviceTypeName"`
	OnlineStatus        int    `json:"onlineStatus"`
	SellStatus          int    `json:"sellStatus"`
	FaultState          int    `json:"faultState"`
	LockType            int    `json:"lockType"`
	StockoutState       int    `json:"stockoutState"`
	ShortageAlarmNumber int    `json:"shortageAlarmNumber"`
}

type ordersResultBody struct {
	Page        int             `json:"page"`
	PageSize    int             `json:"pageSize"`
	Total       int             `json:"total"`
	TotalPages  int             `json:"totalPages"`
	HasPrevPage bool            `json:"hasPrevPage"`
	HasNextPage bool            `json:"hasNextPage"`
	Items       []orderItemBody `json:"items"`
}

type orderItemBody struct {
	OrderNo            string  `json:"orderNo"`
	TransactionOrderNo string  `json:"transactionOrderNo"`
	DeviceCode         string  `json:"deviceCode"`
	OrderStatus        int     `json:"orderStatus"`
	OrderStatusName    string  `json:"orderStatusName"`
	OrderAmount        float64 `json:"orderAmount"`
	PayAmount          float64 `json:"payAmount"`
	PayTime            string  `json:"payTime"`
	OrderCreatedTime   string  `json:"orderCreatedTime"`
}

type slotItemBody struct {
	SlotID      scalarText `json:"slotId"`
	SlotNo      scalarText `json:"slotNo"`
	SlotStatus  int        `json:"slotStatus"`
	Stock       int        `json:"stock"`
	Capacity    int        `json:"capacity"`
	ProductID   scalarText `json:"productId"`
	ProductName string     `json:"productName"`
	Price       float64    `json:"price"`
}

type scalarText string

func (s *scalarText) UnmarshalJSON(raw []byte) error {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		*s = ""
		return nil
	}
	if strings.HasPrefix(text, `"`) {
		var quoted string
		if err := json.Unmarshal(raw, &quoted); err != nil {
			return err
		}
		*s = scalarText(quoted)
		return nil
	}
	*s = scalarText(text)
	return nil
}
