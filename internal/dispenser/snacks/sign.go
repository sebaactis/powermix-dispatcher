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

const maxSkew = 5 * time.Minute

func Canonical(method, path, timestamp, nonce string) string {
	return strings.ToUpper(method) + "\n" + path + "\n" + timestamp + "\n" + nonce
}

func Sign(secret, method, path, timestamp, nonce string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(Canonical(method, path, timestamp, nonce)))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func TimestampMillis(t time.Time) string {
	return strconv.FormatInt(t.UnixMilli(), 10)
}

func NewNonce() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("snacks: no se pudo generar el nonce: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

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
