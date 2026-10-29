package snacks

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

// Vectores calculados con `openssl dgst -sha256 -hmac` + base64, independientes
// de esta implementación.
func TestSignMatchesReference(t *testing.T) {
	cases := []struct {
		name                    string
		method, path, ts, nonce string
		want                    string
	}{
		{
			name:   "ejemplo de la documentación",
			method: "GET", path: "/api/v1/users",
			ts:    "1719999999999",
			nonce: "550e8400e29b41d4a716446655440000",
			want:  "9m0fmovcYmKYpMQ/c+4SNmyhiTc7pR3Njn0/fLEWZqk=",
		},
		{
			name:   "despacho remoto",
			method: "POST", path: "/open/v7/device/remote/shipment",
			ts:    "1751234567890",
			nonce: "7f3a9c2e5b1d4f8a9c6b3e2d1f0a9b8c",
			want:  "ZLCYwew0lCFOVR2npQbIFuZZ/USvESlBvftaoQSzGbg=",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Sign("testsecret", tc.method, tc.path, tc.ts, tc.nonce)
			if got != tc.want {
				t.Fatalf("Sign = %q, se esperaba %q", got, tc.want)
			}
		})
	}
}

// La cadena canónica tiene un orden y separadores exactos: cambiar el orden
// cambia la firma.
func TestCanonicalStringOrder(t *testing.T) {
	got := Canonical("POST", "/open/v7/device/remote/shipment", "1751234567890", "abc123")
	want := "POST\n/open/v7/device/remote/shipment\n1751234567890\nabc123"
	if got != want {
		t.Fatalf("Canonical = %q, se esperaba %q", got, want)
	}
}

// El nonce debe ser hex de 32 caracteres sin guiones y no repetirse.
func TestNewNonceIsUniqueHexWithoutHyphens(t *testing.T) {
	seen := make(map[string]struct{}, 2000)

	for range 2000 {
		nonce, err := NewNonce()
		if err != nil {
			t.Fatalf("NewNonce: %v", err)
		}
		if len(nonce) != 32 {
			t.Fatalf("el nonce %q tiene longitud %d, se esperaba 32", nonce, len(nonce))
		}
		for _, r := range nonce {
			if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
				t.Fatalf("el nonce %q no es hex en minúscula", nonce)
			}
		}
		if _, dup := seen[nonce]; dup {
			t.Fatalf("el nonce %q se repitió", nonce)
		}
		seen[nonce] = struct{}{}
	}
}

func TestVerifySignatureAcceptsWhatSignatureHeadersProduced(t *testing.T) {
	now := time.Date(2026, 3, 25, 8, 30, 42, 0, time.UTC)
	const method, path = "POST", "/open/v7/device/remote/shipment"

	headers, err := SignatureHeaders("ak_test", "testsecret", method, path, now)
	if err != nil {
		t.Fatalf("SignatureHeaders: %v", err)
	}

	h := http.Header{}
	for k, v := range headers {
		h.Set(k, v)
	}

	if err := VerifySignature("testsecret", method, path, h, now); err != nil {
		t.Fatalf("VerifySignature rechazó una firma producida por ella misma: %v", err)
	}
}

func TestVerifySignatureRejectsClockSkewBeyondFiveMinutes(t *testing.T) {
	signed := time.Date(2026, 3, 25, 8, 30, 42, 0, time.UTC)
	const method, path = "POST", "/open/v7/device/remote/shipment"

	headers, err := SignatureHeaders("ak_test", "testsecret", method, path, signed)
	if err != nil {
		t.Fatalf("SignatureHeaders: %v", err)
	}

	h := http.Header{}
	for k, v := range headers {
		h.Set(k, v)
	}

	tooLate := signed.Add(6 * time.Minute)
	err = VerifySignature("testsecret", method, path, h, tooLate)
	if !errors.Is(err, ErrSkewedTimestamp) {
		t.Fatalf("VerifySignature = %v, se esperaba ErrSkewedTimestamp", err)
	}
}

func TestVerifySignatureRejectsTampering(t *testing.T) {
	now := time.Date(2026, 3, 25, 8, 30, 42, 0, time.UTC)
	const method, path = "POST", "/open/v7/device/remote/shipment"

	headers, err := SignatureHeaders("ak_test", "testsecret", method, path, now)
	if err != nil {
		t.Fatalf("SignatureHeaders: %v", err)
	}

	h := http.Header{}
	for k, v := range headers {
		h.Set(k, v)
	}

	if err := VerifySignature("othersecret", method, path, h, now); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("VerifySignature con otro secret = %v, se esperaba ErrBadSignature", err)
	}
	if err := VerifySignature("testsecret", method, "/open/v7/device/page", h, now); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("VerifySignature con otra ruta = %v, se esperaba ErrBadSignature", err)
	}
}
