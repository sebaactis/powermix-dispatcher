package snacks_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"compras-remotas/internal/dispenser/snacks"
)

const responseLimit = 1 << 20

func paddedServer(t *testing.T, body string, size int) string {
	t.Helper()

	if len(body) > size {
		t.Fatalf("el cuerpo base ocupa %d bytes, más que %d", len(body), size)
	}
	padded := body + strings.Repeat(" ", size-len(body))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, padded)
	}))
	t.Cleanup(srv.Close)

	return srv.URL
}

func TestClientResponseSizeLimit(t *testing.T) {
	calls := []struct {
		name string
		body string
		call func(*snacks.Client) error
	}{
		{
			name: "despacho",
			body: `{"code":0,"message":"success","result":{}}`,
			call: func(c *snacks.Client) error {
				_, err := c.Ship(context.Background(), shipmentRequest("po_1"))
				return err
			},
		},
		{
			name: "consulta",
			body: `{"success":true,"code":200,"message":"ok","result":[]}`,
			call: func(c *snacks.Client) error {
				_, err := c.Slots(context.Background(), "")
				return err
			},
		},
	}

	for _, call := range calls {
		t.Run(call.name+": exactamente 1 MB se acepta", func(t *testing.T) {
			client := snacks.NewClient(paddedServer(t, call.body, responseLimit), testAccessKey, testAppSecret)

			if err := call.call(client); err != nil {
				t.Fatalf("una respuesta de exactamente 1 MB se rechazó: %v", err)
			}
		})

		t.Run(call.name+": 1 MB más un byte se rechaza con un error claro", func(t *testing.T) {
			client := snacks.NewClient(paddedServer(t, call.body, responseLimit+1), testAccessKey, testAppSecret)

			err := call.call(client)
			if err == nil || !strings.Contains(err.Error(), "supera") {
				t.Fatalf("err = %v, se esperaba un error que diga que la respuesta supera el límite", err)
			}

			var apiErr *snacks.APIError
			if errors.As(err, &apiErr) {
				t.Fatalf("la respuesta demasiado grande se informó como %T: el despacho la tomaría como rechazo del proveedor y no como resultado desconocido", apiErr)
			}
		})
	}
}
