// Package snacks implementa la integración con Snacks Powermix (API abierta de
// Snacks).
//
// Este archivo cubre solo el contrato de red que se puede verificar sin
// credenciales: la firma HMAC, el nonce y la ventana de tiempo.
package snacks

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var (
	ErrMissingHeader   = errors.New("snacks: falta un header obligatorio")
	ErrSkewedTimestamp = errors.New("snacks: el timestamp está fuera de la ventana de ±5 minutos")
	ErrBadSignature    = errors.New("snacks: la firma no coincide")
)

// maxSkew es la tolerancia del proveedor. Convierte la sincronización del reloj
// en un requisito funcional, no en una prolijidad operativa.
const maxSkew = 5 * time.Minute

// Canonical es la cadena exacta que se firma:
//
//	MÉTODO \n RUTA \n timestamp \n nonce
//
// Los parámetros de query y el cuerpo no forman parte de ella.
func Canonical(method, path, timestamp, nonce string) string {
	return strings.ToUpper(method) + "\n" + path + "\n" + timestamp + "\n" + nonce
}

// Sign devuelve Base64(HMAC-SHA256(secret, cadena canónica)).
func Sign(secret, method, path, timestamp, nonce string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(Canonical(method, path, timestamp, nonce)))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// TimestampMillis es el header timestamp: epoch en milisegundos, 13 dígitos.
func TimestampMillis(t time.Time) string {
	return strconv.FormatInt(t.UnixMilli(), 10)
}

// NewNonce devuelve un nonce nuevo de 32 caracteres hex en minúscula. Cada
// llamada necesita uno nuevo: repetirlo se rechaza como replay durante diez
// minutos.
func NewNonce() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("snacks: no se pudo generar el nonce: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

// SignatureHeaders arma el juego completo de headers para un request. Un
// reintento debe volver a llamar a esto: necesita un nonce y un timestamp
// nuevos.
func SignatureHeaders(accessKey, secret, method, path string, now time.Time) (map[string]string, error) {
	nonce, err := NewNonce()
	if err != nil {
		return nil, err
	}
	timestamp := TimestampMillis(now)

	return map[string]string{
		"accessKey":    accessKey,
		"timestamp":    timestamp,
		"nonce":        nonce,
		"sign":         Sign(secret, method, path, timestamp, nonce),
		"Content-Type": "application/json",
	}, nil
}

// VerifySignature revisa presencia de headers, ventana de reloj y firma. Lo usa
// el servidor falso local y también nuestros handlers de callback cuando el
// proveedor nos manda algo.
func VerifySignature(secret, method, path string, h http.Header, now time.Time) error {
	timestamp := h.Get("timestamp")
	nonce := h.Get("nonce")
	sign := h.Get("sign")

	if h.Get("accessKey") == "" || timestamp == "" || nonce == "" || sign == "" {
		return ErrMissingHeader
	}

	millis, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: el timestamp %q no es un epoch en milisegundos", ErrMissingHeader, timestamp)
	}

	skew := now.Sub(time.UnixMilli(millis))
	if skew < 0 {
		skew = -skew
	}
	if skew > maxSkew {
		return ErrSkewedTimestamp
	}

	expected := Sign(secret, method, path, timestamp, nonce)
	if !hmac.Equal([]byte(expected), []byte(sign)) {
		return ErrBadSignature
	}
	return nil
}
