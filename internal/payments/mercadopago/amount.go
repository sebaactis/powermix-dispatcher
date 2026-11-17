package mercadopago

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func formatAmount(cents int64) string {
	return fmt.Sprintf("%d.%02d", cents/100, cents%100)
}

func parseAmount(raw string) (int64, error) {
	whole, frac, hasFrac := strings.Cut(raw, ".")
	if whole == "" || !onlyDigits(whole) || (hasFrac && (frac == "" || len(frac) > 2 || !onlyDigits(frac))) {
		return 0, fmt.Errorf("mercadopago: monto inválido %q", raw)
	}
	units, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || units > (1<<62)/100 {
		return 0, fmt.Errorf("mercadopago: monto fuera de rango %q", raw)
	}
	for len(frac) < 2 {
		frac += "0"
	}
	cents, _ := strconv.ParseInt(frac, 10, 64)
	return units*100 + cents, nil
}

func onlyDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func formatExpiration(d time.Duration) string {
	seconds := int64(d / time.Second)
	if seconds%60 == 0 {
		return fmt.Sprintf("PT%dM", seconds/60)
	}
	return fmt.Sprintf("PT%dS", seconds)
}
