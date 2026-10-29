// Package batidos implementa la integración con Batidos Powermix (API abierta de
// comercio de Batidos).
//
// Este archivo cubre solo el contrato de red que se puede verificar sin
// credenciales: la firma del request y el manejo de fechas. El cliente que llama
// a los endpoints se construye encima de esto.
package batidos

import (
	"crypto/md5"
	"encoding/hex"
	"strconv"
	"time"
)

// chinaTime es UTC+8. China no tiene horario de verano, así que una zona fija es
// exacta y evita depender de que el sistema tenga tzdata instalado.
var chinaTime = time.FixedZone("UTC+8", 8*60*60)

// campaignLayout es el único formato de fecha que acepta el endpoint de campaña.
const campaignLayout = "2006-01-02 15:04:05"

// KeyMD5 devuelve MD5(key + secret + timestamp) en hex minúscula, como exige el
// header key-md5. La concatenación no lleva separadores.
func KeyMD5(key, secret, timestampMillis string) string {
	sum := md5.Sum([]byte(key + secret + timestampMillis))
	return hex.EncodeToString(sum[:])
}

// TimestampMillis es el header timestamp: epoch en milisegundos. Es un instante
// absoluto, así que la nota del proveedor sobre "huso horario del este (UTC+8)"
// no lo modifica.
func TimestampMillis(t time.Time) string {
	return strconv.FormatInt(t.UnixMilli(), 10)
}

// CampaignTime formatea un instante para startTime/endTime, que el proveedor
// interpreta como UTC+8. Mandar hora local desde Argentina adelantaría la
// campaña once horas y el canje sería rechazado.
func CampaignTime(t time.Time) string {
	return t.In(chinaTime).Format(campaignLayout)
}

// ParseCampaignTime lee una fecha de campaña interpretándola como UTC+8.
func ParseCampaignTime(s string) (time.Time, error) {
	return time.ParseInLocation(campaignLayout, s, chinaTime)
}

// SignatureHeaders arma los tres headers que lleva todo request a Batidos.
func SignatureHeaders(key, secret string, now time.Time) map[string]string {
	timestamp := TimestampMillis(now)
	return map[string]string{
		"key":       key,
		"timestamp": timestamp,
		"key-md5":   KeyMD5(key, secret, timestamp),
	}
}
