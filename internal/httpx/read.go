package httpx

import (
	"errors"
	"fmt"
	"io"
)

var ErrTooLarge = errors.New("la respuesta supera el límite")

func ReadLimited(r io.Reader, limit int) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if err != nil {
		return raw, err
	}

	if len(raw) > limit {
		return raw[:limit], fmt.Errorf("%w de %d bytes", ErrTooLarge, limit)
	}

	return raw, nil
}
