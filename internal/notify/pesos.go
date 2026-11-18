package notify

import (
	"fmt"
	"strconv"
)

func Pesos(cents int64) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}

	whole := strconv.FormatInt(cents/100, 10)
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + "." + whole[i:]
	}

	return fmt.Sprintf("%s$%s,%02d", sign, whole, cents%100)
}
