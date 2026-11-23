package mercadopago

import (
	"errors"
	"testing"
	"time"
)

const (
	vectorSecret    = "secreto-webhook"
	vectorDataID    = "ORD01JQ4S4KY8HWQ6NA5PXB65B3D3"
	vectorRequestID = "2066ca19-c6f1-498a-be75-1923005edd06"
	vectorTS        = int64(1742505638683)
	vectorV1        = "b5866ec1ea14a5d5d605ed5ecd2254398348a6827435eda849add331129c8b55"
)

func vectorNow() time.Time { return time.UnixMilli(vectorTS) }

func vectorHeader() string {
	return "ts=1742505638683,v1=" + vectorV1
}

func TestVerifySignatureAcceptsIndependentVector(t *testing.T) {
	if err := VerifySignature(vectorSecret, vectorHeader(), vectorRequestID, vectorDataID, vectorNow()); err != nil {
		t.Fatalf("firma válida rechazada: %v", err)
	}
}

func TestVerifySignatureToleratesSpacesAndOrder(t *testing.T) {
	header := " v1=" + vectorV1 + " , ts=1742505638683 "
	if err := VerifySignature(vectorSecret, header, vectorRequestID, vectorDataID, vectorNow()); err != nil {
		t.Fatalf("firma válida rechazada: %v", err)
	}
}

func TestSignatureHeaderMatchesIndependentVector(t *testing.T) {
	if got := SignatureHeader(vectorSecret, vectorRequestID, vectorDataID, vectorNow()); got != vectorHeader() {
		t.Fatalf("encabezado = %q, se esperaba %q", got, vectorHeader())
	}
}

func TestVerifySignatureWindow(t *testing.T) {
	cases := []struct {
		name  string
		shift time.Duration
		ok    bool
	}{
		{"justo en el borde por delante", SignatureTolerance, true},
		{"justo en el borde por detrás", -SignatureTolerance, true},
		{"vencida", SignatureTolerance + time.Millisecond, false},
		{"del futuro", -SignatureTolerance - time.Millisecond, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := VerifySignature(vectorSecret, vectorHeader(), vectorRequestID, vectorDataID, vectorNow().Add(tc.shift))
			if tc.ok && err != nil {
				t.Fatalf("rechazada dentro de la ventana: %v", err)
			}
			if !tc.ok && !errors.Is(err, ErrInvalidSignature) {
				t.Fatalf("err = %v, se esperaba ErrInvalidSignature", err)
			}
		})
	}
}

func TestVerifySignatureRejects(t *testing.T) {
	cases := []struct {
		name      string
		secret    string
		header    string
		requestID string
		dataID    string
	}{
		{"otro secreto", "otro", vectorHeader(), vectorRequestID, vectorDataID},
		{"sin secreto", "", vectorHeader(), vectorRequestID, vectorDataID},
		{"otro data.id", vectorSecret, vectorHeader(), vectorRequestID, "ORD00000000000000000000000000"},
		{"sin data.id", vectorSecret, vectorHeader(), vectorRequestID, ""},
		{"otro request-id", vectorSecret, vectorHeader(), "otro", vectorDataID},
		{"sin request-id", vectorSecret, vectorHeader(), "", vectorDataID},
		{"encabezado vacío", vectorSecret, "", vectorRequestID, vectorDataID},
		{"sin ts", vectorSecret, "v1=" + vectorV1, vectorRequestID, vectorDataID},
		{"sin v1", vectorSecret, "ts=1742505638683", vectorRequestID, vectorDataID},
		{"ts no numérico", vectorSecret, "ts=abc,v1=" + vectorV1, vectorRequestID, vectorDataID},
		{"v1 no hexadecimal", vectorSecret, "ts=1742505638683,v1=zz", vectorRequestID, vectorDataID},
		{"v1 truncado", vectorSecret, "ts=1742505638683,v1=" + vectorV1[:62], vectorRequestID, vectorDataID},
		{"ts alterado", vectorSecret, "ts=1742505638684,v1=" + vectorV1, vectorRequestID, vectorDataID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := VerifySignature(tc.secret, tc.header, tc.requestID, tc.dataID, vectorNow())
			if !errors.Is(err, ErrInvalidSignature) {
				t.Fatalf("err = %v, se esperaba ErrInvalidSignature", err)
			}
		})
	}
}

func TestManifestOmitsEmptyPairsAndLowercasesID(t *testing.T) {
	if got, want := manifest("ORD1A", "", "10"), "id:ord1a;ts:10;"; got != want {
		t.Fatalf("manifest = %q, se esperaba %q", got, want)
	}
}

const (
	vectorTSSeconds = int64(1742505638)
	vectorV1Seconds = "11ca658e265c2e0450b854c45cbdf2a0ce517a6db53fa68cb2b148b2135f7d09"
)

func vectorHeaderSeconds() string {
	return "ts=1742505638,v1=" + vectorV1Seconds
}

func TestVerifySignatureAcceptsTSInSeconds(t *testing.T) {
	now := time.Unix(vectorTSSeconds, 0)
	if err := VerifySignature(vectorSecret, vectorHeaderSeconds(), vectorRequestID, vectorDataID, now); err != nil {
		t.Fatalf("firma con ts en segundos rechazada: %v", err)
	}
}

func TestVerifySignatureWindowWithTSInSeconds(t *testing.T) {
	cases := []struct {
		name  string
		shift time.Duration
		ok    bool
	}{
		{"justo en el borde por delante", SignatureTolerance, true},
		{"justo en el borde por detrás", -SignatureTolerance, true},
		{"vencida", SignatureTolerance + time.Second, false},
		{"del futuro", -SignatureTolerance - time.Second, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Unix(vectorTSSeconds, 0).Add(tc.shift)
			err := VerifySignature(vectorSecret, vectorHeaderSeconds(), vectorRequestID, vectorDataID, now)
			if tc.ok && err != nil {
				t.Fatalf("rechazada dentro de la ventana: %v", err)
			}
			if !tc.ok && !errors.Is(err, ErrInvalidSignature) {
				t.Fatalf("err = %v, se esperaba ErrInvalidSignature", err)
			}
		})
	}
}

func TestVerifySignatureWithoutDataIDOmitsPair(t *testing.T) {
	header := SignatureHeader(vectorSecret, vectorRequestID, "", vectorNow())
	if err := VerifySignature(vectorSecret, header, vectorRequestID, "", vectorNow()); err != nil {
		t.Fatalf("firma sin data.id rechazada: %v", err)
	}
	if err := VerifySignature(vectorSecret, header, vectorRequestID, vectorDataID, vectorNow()); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("err = %v; una firma sin id no debe valer para un data.id", err)
	}
}
