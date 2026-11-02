package batidos

import (
	"crypto/md5"
	"encoding/hex"
	"strconv"
	"time"
)

var chinaTime = time.FixedZone("UTC+8", 8*60*60)

const campaignLayout = "2006-01-02 15:04:05"

func KeyMD5(key, secret, timestampMillis string) string {
	sum := md5.Sum([]byte(key + secret + timestampMillis))
	return hex.EncodeToString(sum[:])
}

func TimestampMillis(t time.Time) string {
	return strconv.FormatInt(t.UnixMilli(), 10)
}

func CampaignTime(t time.Time) string {
	return t.In(chinaTime).Format(campaignLayout)
}

func ParseCampaignTime(s string) (time.Time, error) {
	return time.ParseInLocation(campaignLayout, s, chinaTime)
}

func SignatureHeaders(key, secret string, now time.Time) map[string]string {
	timestamp := TimestampMillis(now)
	return map[string]string{
		"key":       key,
		"timestamp": timestamp,
		"key-md5":   KeyMD5(key, secret, timestamp),
	}
}
