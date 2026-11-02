package batidos

import "encoding/json"

type goodsItemWire struct {
	GoodsID string `json:"goodsId"`
	Qty     int    `json:"qty"`
}

type campaignWireRequest struct {
	ActiveName    string          `json:"activeName"`
	StartTime     string          `json:"startTime"`
	EndTime       string          `json:"endTime"`
	ExchangeType  int             `json:"exchangeType"`
	DeviceTypeID  string          `json:"deviceTypeId"`
	DeviceIDs     []string        `json:"deviceIds"`
	NotifyURL     string          `json:"notifyUrl"`
	GoodsItemDtos []goodsItemWire `json:"goodsItemDtos"`
}

type campaignWireResponse struct {
	ExchangeCodes []string `json:"exchangeCodes"`
	ActiveInfo    struct {
		ActiveNo     string `json:"activeNo"`
		ActiveName   string `json:"activeName"`
		StartTime    string `json:"startTime"`
		EndTime      string `json:"endTime"`
		ExchangeType int    `json:"exchangeType"`
	} `json:"activeInfo"`
}

type produceWireRequest struct {
	Code     string `json:"code"`
	DeviceID string `json:"deviceId"`
	GoodsID  string `json:"goodsId"`
	DeviceNo string `json:"deviceNo"`
}

type orderInfoWireRequest struct {
	Code     string `json:"code"`
	DeviceID string `json:"deviceId"`
}

type orderInfoWireResponse struct {
	OrderNo        string `json:"orderNo"`
	DeviceNo       string `json:"deviceNo"`
	GoodsName      string `json:"goodsName"`
	Status         int    `json:"status"`
	OutStockStatus int    `json:"outStockStatus"`
}

type envelope struct {
	Code *int            `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}
