package mercadopago

import (
	"testing"
	"time"
)

func TestFormatAmount(t *testing.T) {
	cases := map[int64]string{1: "0.01", 99: "0.99", 100: "1.00", 150050: "1500.50"}
	for cents, want := range cases {
		if got := formatAmount(cents); got != want {
			t.Errorf("formatAmount(%d) = %q, se esperaba %q", cents, got, want)
		}
	}
}

func TestParseAmount(t *testing.T) {
	ok := map[string]int64{"0": 0, "15": 1500, "15.5": 1550, "15.05": 1505, "1500.00": 150000, "0.01": 1}
	for raw, want := range ok {
		got, err := parseAmount(raw)
		if err != nil || got != want {
			t.Errorf("parseAmount(%q) = %d, %v; se esperaba %d", raw, got, err, want)
		}
	}
	for _, raw := range []string{"", ".", "15.", ".5", "15.123", "-1.00", "+1", "1,00", "1e3", " 1", "99999999999999999999"} {
		if _, err := parseAmount(raw); err == nil {
			t.Errorf("parseAmount(%q) aceptó un monto inválido", raw)
		}
	}
}

func TestFormatAmountRoundTrips(t *testing.T) {
	for _, cents := range []int64{1, 10, 101, 123456789} {
		got, err := parseAmount(formatAmount(cents))
		if err != nil || got != cents {
			t.Errorf("ida y vuelta de %d = %d, %v", cents, got, err)
		}
	}
}

func TestFormatExpiration(t *testing.T) {
	cases := map[time.Duration]string{180 * time.Second: "PT3M", 90 * time.Second: "PT90S", time.Minute: "PT1M"}
	for d, want := range cases {
		if got := formatExpiration(d); got != want {
			t.Errorf("formatExpiration(%s) = %q, se esperaba %q", d, got, want)
		}
	}
}
