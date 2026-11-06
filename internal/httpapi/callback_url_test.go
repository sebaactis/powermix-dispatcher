package httpapi

import (
	"bytes"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"compras-remotas/internal/order"
)

func TestBatidosCallbackURL(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		secret  string
		orderID string
		want    string
	}{
		{
			name:    "base y secreto y orden",
			baseURL: "https://kiosco.example.test",
			secret:  "ruta-secreta",
			orderID: "orden-1",
			want:    "https://kiosco.example.test/callbacks/batidos/ruta-secreta/orden-1",
		},
		{
			name:    "base con barra final",
			baseURL: "https://kiosco.example.test/",
			secret:  "ruta-secreta",
			orderID: "orden-1",
			want:    "https://kiosco.example.test/callbacks/batidos/ruta-secreta/orden-1",
		},
		{
			name:    "segmentos con caracteres que hay que escapar",
			baseURL: "https://kiosco.example.test",
			secret:  "secreto/con espacio",
			orderID: "orden 1/2",
			want:    "https://kiosco.example.test/callbacks/batidos/secreto%2Fcon%20espacio/orden%201%2F2",
		},
		{
			name:    "sin base",
			secret:  "ruta-secreta",
			orderID: "orden-1",
			want:    "",
		},
		{
			name:    "sin secreto",
			baseURL: "https://kiosco.example.test",
			orderID: "orden-1",
			want:    "",
		},
		{
			name:    "sin orden",
			baseURL: "https://kiosco.example.test",
			secret:  "ruta-secreta",
			want:    "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := BatidosCallbackURL(tc.baseURL, tc.secret, tc.orderID); got != tc.want {
				t.Fatalf("BatidosCallbackURL = %q, se esperaba %q", got, tc.want)
			}
		})
	}
}

func TestRedactCallbackPath(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string
	}{
		{
			name: "callback de batidos",
			path: "/callbacks/batidos/ruta-secreta/orden-1",
			want: "/callbacks/batidos/***",
		},
		{
			name: "callback de snacks",
			path: "/callbacks/snacks/ruta-secreta",
			want: "/callbacks/snacks/***",
		},
		{
			name: "sin nada después del origen",
			path: "/callbacks/batidos/",
			want: "/callbacks/batidos/***",
		},
		{
			name: "ruta normal",
			path: "/orders/orden-1",
			want: "/orders/orden-1",
		},
		{
			name: "callbacks sin origen",
			path: "/callbacks/",
			want: "/callbacks/",
		},
		{
			name: "raiz",
			path: "/",
			want: "/",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := redactCallbackPath(tc.path); got != tc.want {
				t.Fatalf("redactCallbackPath(%q) = %q, se esperaba %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestCallbackLogsNeverCarryTheSecretSegment(t *testing.T) {
	const secret = "ruta-secreta"

	cases := []struct {
		name       string
		confirmErr error
	}{
		{
			name:       "callback sin línea conocida",
			confirmErr: order.ErrCallbackUnknownLine,
		},
		{
			name:       "falla de verdad",
			confirmErr: errors.New("el proveedor no respondió"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logged bytes.Buffer

			mux, receiver := newLoggingCallbackFixture(t, secret, &logged)
			receiver.confirmErr = tc.confirmErr

			body := `{"code":"canje-1","orderNo":"A1","status":2}`
			call(t, mux, http.MethodPost, "/callbacks/batidos/"+secret+"/orden-1", body)

			if logged.Len() == 0 {
				t.Fatalf("se esperaba al menos una línea de log")
			}

			if strings.Contains(logged.String(), secret) {
				t.Fatalf("el secreto del callback no puede quedar en el log: %s", logged.String())
			}

			if !strings.Contains(logged.String(), "/callbacks/batidos/***") {
				t.Fatalf("el log debe conservar la forma redactada de la ruta: %s", logged.String())
			}
		})
	}
}

func TestBatidosCallbackURLMatchesTheRegisteredRoute(t *testing.T) {
	const secret = "ruta-secreta"

	mux, receiver := newCallbackFixture(t, CallbackSecrets{Batidos: secret})

	notifyURL := BatidosCallbackURL("https://kiosco.example.test", secret, "orden-1")

	parsed, err := url.Parse(notifyURL)
	if err != nil {
		t.Fatalf("la URL que arma el constructor no se puede interpretar: %v", err)
	}

	body := `{"code":"canje-1","deviceId":"dev-1","goodsId":"sku-1","orderNo":"A202603241535065465","status":2}`
	recorder := call(t, mux, http.MethodPost, parsed.RequestURI(), body)

	if recorder.Code != http.StatusOK {
		t.Fatalf("la URL del constructor tiene que entrar por la ruta de callbacks: se esperaba 200 y llegó %d: %s", recorder.Code, recorder.Body.String())
	}

	ack := decode(t, recorder)
	if ack["code"] != float64(200) || ack["success"] != true || ack["data"] != true || ack["msg"] != "操作成功" {
		t.Fatalf("la respuesta debe ser la que documenta el proveedor: %v", ack)
	}

	if len(receiver.confirmed) != 1 {
		t.Fatalf("el callback de la URL del constructor debía confirmarse una vez y se confirmó %d veces", len(receiver.confirmed))
	}

	if got := receiver.confirmed[0].OrderID; got != "orden-1" {
		t.Fatalf("orderId = %q, se esperaba el que llevaba la URL", got)
	}
}
