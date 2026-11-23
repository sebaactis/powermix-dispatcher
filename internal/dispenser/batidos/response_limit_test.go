package batidos_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"compras-remotas/internal/dispenser/batidos"
)

const responseLimit = 1 << 20

func paddedBody(t *testing.T, body string, size int) string {
	t.Helper()

	if len(body) > size {
		t.Fatalf("el cuerpo base ocupa %d bytes, más que %d", len(body), size)
	}

	return body + strings.Repeat(" ", size-len(body))
}

func TestClientResponseSizeLimit(t *testing.T) {
	const device = `{"code":200,"success":true,"msg":"ok","data":{"id":"dev-1","deviceNo":"adbafc62","status":1,"deviceRunStatus":1,"scarceStatus":1}}`

	cases := []struct {
		name    string
		size    int
		wantErr bool
	}{
		{"exactamente 1 MB se acepta", responseLimit, false},
		{"1 MB más un byte se rechaza con un error claro", responseLimit + 1, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := paddedBody(t, device, tc.size)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, body)
			}))
			defer srv.Close()

			_, err := newTestClient(t, srv.URL).Device(context.Background(), "dev-1")

			if !tc.wantErr {
				if err != nil {
					t.Fatalf("una respuesta de exactamente 1 MB se rechazó: %v", err)
				}
				return
			}

			if err == nil || !strings.Contains(err.Error(), "supera") {
				t.Fatalf("err = %v, se esperaba un error que diga que la respuesta supera el límite", err)
			}

			var apiErr *batidos.APIError
			if errors.As(err, &apiErr) {
				t.Fatalf("la respuesta demasiado grande se informó como %T: el despacho la tomaría como rechazo del proveedor y no como resultado desconocido", apiErr)
			}
		})
	}
}
