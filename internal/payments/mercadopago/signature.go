package mercadopago

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const SignatureTolerance = 5 * time.Minute

const secondsCutoff = 1_000_000_000_000

var ErrInvalidSignature = errors.New("mercadopago: firma del webhook inválida")

func VerifySignature(secret, xSignature, xRequestID, dataID string, now time.Time) error {
	if secret == "" {
		return fmt.Errorf("%w: falta el secreto del webhook", ErrInvalidSignature)
	}
	ts, v1 := parseSignatureHeader(xSignature)
	if ts == "" || v1 == "" {
		return fmt.Errorf("%w: encabezado x-signature incompleto", ErrInvalidSignature)
	}
	tsValue, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: ts no numérico", ErrInvalidSignature)
	}

	received, err := hex.DecodeString(v1)
	if err != nil {
		return fmt.Errorf("%w: v1 no es hexadecimal", ErrInvalidSignature)
	}
	if !hmac.Equal(received, signManifest(secret, manifest(dataID, xRequestID, ts))) {
		return fmt.Errorf("%w: la firma no coincide", ErrInvalidSignature)
	}

	drift := now.Sub(signatureTime(tsValue))
	if drift < 0 {
		drift = -drift
	}
	if drift > SignatureTolerance {
		return fmt.Errorf("%w: ts fuera de la ventana de %s", ErrInvalidSignature, SignatureTolerance)
	}
	return nil
}

func signatureTime(ts int64) time.Time {
	if ts < secondsCutoff {
		return time.Unix(ts, 0)
	}
	return time.UnixMilli(ts)
}

func SignatureHeader(secret, xRequestID, dataID string, at time.Time) string {
	ts := strconv.FormatInt(at.UnixMilli(), 10)
	return "ts=" + ts + ",v1=" + hex.EncodeToString(signManifest(secret, manifest(dataID, xRequestID, ts)))
}

func parseSignatureHeader(header string) (ts, v1 string) {
	for _, part := range strings.Split(header, ",") {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch {
		case key == "ts" && ts == "":
			ts = value
		case key == "v1" && v1 == "":
			v1 = value
		}
	}
	return ts, v1
}

func manifest(dataID, xRequestID, ts string) string {
	var b strings.Builder
	if dataID != "" {
		b.WriteString("id:" + strings.ToLower(dataID) + ";")
	}
	if xRequestID != "" {
		b.WriteString("request-id:" + xRequestID + ";")
	}
	if ts != "" {
		b.WriteString("ts:" + ts + ";")
	}
	return b.String()
}

func signManifest(secret, message string) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(message))
	return mac.Sum(nil)
}
