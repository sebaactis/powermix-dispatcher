package batidos

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

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

type deviceWireResponse struct {
	ID              string      `json:"id"`
	DeviceNo        string      `json:"deviceNo"`
	Name            string      `json:"name"`
	DeviceTypeID    string      `json:"deviceTypeId"`
	Status          stateNumber `json:"status"`
	DeviceRunStatus stateNumber `json:"deviceRunStatus"`
	ScarceStatus    stateNumber `json:"scarceStatus"`
}

type deviceGoodsWireResponse struct {
	ID      string  `json:"id"`
	No      string  `json:"no"`
	Name    string  `json:"name"`
	Price   float64 `json:"price"`
	SoldOut int     `json:"soldOut"`
	Listing bool    `json:"listing"`
}

type stateNumber int

func (n *stateNumber) UnmarshalJSON(raw []byte) error {
	text := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if text == "" || text == "null" {
		*n = 0
		return nil
	}

	value, err := strconv.Atoi(text)
	if err != nil {
		return fmt.Errorf("el proveedor informó un estado no numérico: %s", raw)
	}
	*n = stateNumber(value)
	return nil
}

type envelope struct {
	Code *int            `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}
