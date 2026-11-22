package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"compras-remotas/internal/logging"
)

func parseJSONLogLines(t *testing.T, raw string) []map[string]any {
	t.Helper()

	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("la línea de log %q no es JSON válido: %v", line, err)
		}
		records = append(records, rec)
	}
	return records
}

func TestWrapGeneratesUniqueRequestIDAndIgnoresInboundHeader(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.New(&buf, slog.LevelDebug)

	var seenInboundHeader string
	var seenCtxID string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, r *http.Request) {
		seenInboundHeader = r.Header.Get("X-Request-Id")
		seenCtxID, _ = logging.RequestIDFromContext(r.Context())
		logger.InfoContext(r.Context(), "procesando ping")
		_, _ = w.Write([]byte("pong"))
	})

	wrapped := Wrap(mux, logger)

	req1 := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req1.Header.Set("X-Request-Id", "cliente-malicioso-1")
	rec1 := httptest.NewRecorder()
	wrapped.ServeHTTP(rec1, req1)

	id1 := rec1.Header().Get("X-Request-Id")
	if id1 == "" {
		t.Fatal("falta el header X-Request-Id en la respuesta")
	}
	if id1 == "cliente-malicioso-1" {
		t.Fatalf("el servidor usó el X-Request-Id del cliente (%q)", id1)
	}
	if seenInboundHeader != "cliente-malicioso-1" {
		t.Fatalf("no se debe borrar el X-Request-Id entrante para webhooks: llegó %q", seenInboundHeader)
	}
	if seenCtxID != id1 {
		t.Fatalf("request_id en contexto = %q, en header = %q", seenCtxID, id1)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/ping", nil)
	rec2 := httptest.NewRecorder()
	wrapped.ServeHTTP(rec2, req2)

	id2 := rec2.Header().Get("X-Request-Id")
	if id2 == "" || id2 == id1 {
		t.Fatalf("cada pedido debe recibir un X-Request-Id único: id1=%q id2=%q", id1, id2)
	}

	records := parseJSONLogLines(t, buf.String())
	if len(records) < 2 {
		t.Fatalf("se esperaban al menos 2 logs (handler + access log), llegaron %d", len(records))
	}

	if records[0]["request_id"] != id1 {
		t.Fatalf("el log del handler no tiene request_id=%q: %v", id1, records[0])
	}
	if records[1]["request_id"] != id1 {
		t.Fatalf("el access log no tiene request_id=%q: %v", id1, records[1])
	}
}

func TestWrapAccessLogLevelsAndDefault200(t *testing.T) {
	cases := []struct {
		name      string
		path      string
		wantCode  int
		wantLevel string
		wantBytes float64
	}{
		{"200 sin WriteHeader explícito", "/implicit-200", http.StatusOK, "INFO", 0},
		{"200 con cuerpo", "/ok", http.StatusOK, "INFO", 2},
		{"302 redirección", "/redirect", http.StatusFound, "INFO", 0},
		{"404 no encontrado", "/no-existe", http.StatusNotFound, "WARN", 19},
		{"500 error", "/boom-status", http.StatusInternalServerError, "ERROR", 4},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := logging.New(&buf, slog.LevelDebug)

			mux := http.NewServeMux()
			mux.HandleFunc("GET /implicit-200", func(w http.ResponseWriter, r *http.Request) {})
			mux.HandleFunc("GET /ok", func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, "ok")
			})
			mux.HandleFunc("GET /redirect", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusFound)
			})
			mux.HandleFunc("GET /boom-status", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, "fail")
			})

			wrapped := Wrap(mux, logger)
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			wrapped.ServeHTTP(rec, req)

			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, se esperaba %d", rec.Code, tc.wantCode)
			}

			records := parseJSONLogLines(t, buf.String())
			if len(records) != 1 {
				t.Fatalf("se esperaba 1 access log, llegaron %d: %s", len(records), buf.String())
			}

			entry := records[0]
			if entry["level"] != tc.wantLevel {
				t.Fatalf("level = %v, se esperaba %s", entry["level"], tc.wantLevel)
			}
			if entry["method"] != http.MethodGet {
				t.Fatalf("method = %v, se esperaba GET", entry["method"])
			}
			if entry["path"] != tc.path {
				t.Fatalf("path = %v, se esperaba %s", entry["path"], tc.path)
			}
			if entry["status"] != float64(tc.wantCode) {
				t.Fatalf("status = %v, se esperaba %d", entry["status"], tc.wantCode)
			}
			if entry["bytes"] != tc.wantBytes {
				t.Fatalf("bytes = %v, se esperaba %v", entry["bytes"], tc.wantBytes)
			}
			if _, ok := entry["duration_ms"].(float64); !ok {
				t.Fatalf("falta duration_ms numérico: %v", entry)
			}
			if entry["request_id"] == "" || entry["request_id"] == nil {
				t.Fatalf("falta request_id en access log: %v", entry)
			}
		})
	}
}

func TestWrapAccessLogRedactsSecretPathAndOmitsQueryAndHeaders(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.New(&buf, slog.LevelDebug)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /callbacks/batidos/{secret}/{orderId}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := Wrap(mux, logger)
	req := httptest.NewRequest(http.MethodPost, "/callbacks/batidos/secreto-super-privado/ord-99?token=x&sig=y", nil)
	req.Header.Set("X-Signature", "ts=1,v1=deadbeef")
	req.Header.Set("Authorization", "Bearer secreto-token")

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	raw := buf.String()
	for _, forbidden := range []string{"secreto-super-privado", "token=x", "sig=y", "X-Signature", "deadbeef", "Authorization", "secreto-token"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("el access log filtró %q: %s", forbidden, raw)
		}
	}

	records := parseJSONLogLines(t, raw)
	if len(records) != 1 {
		t.Fatalf("se esperaba 1 access log, llegaron %d", len(records))
	}
	if records[0]["path"] != "/callbacks/batidos/{secret}/{orderId}" {
		t.Fatalf("path = %v, se esperaba /callbacks/batidos/{secret}/{orderId}", records[0]["path"])
	}
	if records[0]["route"] != "POST /callbacks/batidos/{secret}/{orderId}" {
		t.Fatalf("route = %v, se esperaba POST /callbacks/batidos/{secret}/{orderId}", records[0]["route"])
	}
}

func TestWrapAccessLogNeverLeaksSecretOnCraftedAndUnmatchedPaths(t *testing.T) {
	const secret = "secreto-ultra-privado"

	cases := []struct {
		name      string
		method    string
		target    string
		wantRoute string
	}{
		{
			name:      "ruta coincidente batidos",
			method:    http.MethodPost,
			target:    "/callbacks/batidos/" + secret + "/ord-1",
			wantRoute: "POST /callbacks/batidos/{secret}/{orderId}",
		},
		{
			name:      "ruta coincidente snacks",
			method:    http.MethodPost,
			target:    "/callbacks/snacks/" + secret,
			wantRoute: "POST /callbacks/snacks/{secret}",
		},
		{
			name:      "ruta coincidente mercadopago",
			method:    http.MethodPost,
			target:    "/callbacks/mercadopago/" + secret,
			wantRoute: "POST /callbacks/mercadopago/{secret}",
		},
		{
			name:      "doble barra inicial (redirect con pattern)",
			method:    http.MethodPost,
			target:    "//callbacks/mercadopago/" + secret,
			wantRoute: "POST /callbacks/mercadopago/{secret}",
		},
		{
			name:      "path traversal con punto punto (redirect con pattern)",
			method:    http.MethodPost,
			target:    "/x/../callbacks/snacks/" + secret,
			wantRoute: "POST /callbacks/snacks/{secret}",
		},
		{
			name:      "segmento punto (redirect con pattern)",
			method:    http.MethodPost,
			target:    "/./callbacks/batidos/" + secret + "/ord-1",
			wantRoute: "POST /callbacks/batidos/{secret}/{orderId}",
		},
		{
			name:   "doble barra inicial sin ruta coincidente (404)",
			method: http.MethodPost,
			target: "//callbacks/snacks/" + secret + "/extra",
		},
		{
			name:   "path traversal sin ruta coincidente (404)",
			method: http.MethodPost,
			target: "/x/../callbacks/mercadopago/" + secret + "/extra",
		},
		{
			name:   "segmento callbacks anidado (404)",
			method: http.MethodPost,
			target: "/v1/callbacks/snacks/" + secret,
		},
		{
			name:   "mayúsculas en callbacks (404)",
			method: http.MethodPost,
			target: "/Callbacks/snacks/" + secret,
		},
		{
			name:   "doble barra tras callbacks (redirect)",
			method: http.MethodPost,
			target: "/callbacks//" + secret,
		},
		{
			name:      "percent encoding en callbacks (match)",
			method:    http.MethodPost,
			target:    "/%63allbacks/snacks/" + secret,
			wantRoute: "POST /callbacks/snacks/{secret}",
		},
		{
			name:   "doble percent encoding en callbacks (404)",
			method: http.MethodPost,
			target: "/%2563allbacks/snacks/" + secret,
		},
		{
			name:      "percent encoding en secreto (match)",
			method:    http.MethodPost,
			target:    "/callbacks/snacks/%73ecreto-ultra-privado",
			wantRoute: "POST /callbacks/snacks/{secret}",
		},
		{
			name:   "barra final en snacks (redirect)",
			method: http.MethodPost,
			target: "/callbacks/snacks/" + secret + "/",
		},
		{
			name:   "barra final en batidos (redirect)",
			method: http.MethodPost,
			target: "/callbacks/batidos/" + secret + "/ord-1/",
		},
		{
			name:   "método equivocado 405 en ruta con secreto",
			method: http.MethodGet,
			target: "/callbacks/mercadopago/" + secret,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := logging.New(&buf, slog.LevelDebug)

			mux := http.NewServeMux()
			mux.HandleFunc("POST /callbacks/batidos/{secret}/{orderId}", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
			mux.HandleFunc("POST /callbacks/snacks/{secret}", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
			mux.HandleFunc("POST /callbacks/mercadopago/{secret}", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			wrapped := Wrap(mux, logger)
			req := httptest.NewRequest(tc.method, tc.target+"?token=x", nil)
			rec := httptest.NewRecorder()
			wrapped.ServeHTTP(rec, req)

			raw := buf.String()
			if strings.Contains(raw, secret) || strings.Contains(raw, "%73ecreto") || strings.Contains(raw, "token=x") {
				t.Fatalf("el access log filtró el secreto o la query: %s", raw)
			}

			records := parseJSONLogLines(t, raw)
			if len(records) != 1 {
				t.Fatalf("se esperaba 1 access log, llegaron %d: %s", len(records), raw)
			}

			if tc.wantRoute != "" {
				if records[0]["route"] != tc.wantRoute {
					t.Fatalf("route = %v, se esperaba %q", records[0]["route"], tc.wantRoute)
				}
			} else if _, hasRoute := records[0]["route"]; hasRoute {
				t.Fatalf("no debía incluir route cuando no hubo match de patrón: %v", records[0])
			}
		})
	}
}

func TestWrapRecoversFromPanicLogsStackAndKeepsServing(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.New(&buf, slog.LevelDebug)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /callbacks/snacks/{secret}", func(w http.ResponseWriter, r *http.Request) {
		panic("explosion inesperada")
	})
	mux.HandleFunc("GET /ok", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	wrapped := Wrap(mux, logger)

	panicReq := httptest.NewRequest(http.MethodPost, "/callbacks/snacks/mi-secreto-oculto?token=x", nil)
	panicRec := httptest.NewRecorder()
	wrapped.ServeHTTP(panicRec, panicReq)

	if panicRec.Code != http.StatusInternalServerError {
		t.Fatalf("status tras panic = %d, se esperaba 500", panicRec.Code)
	}

	var body map[string]string
	if err := json.Unmarshal(panicRec.Body.Bytes(), &body); err != nil {
		t.Fatalf("respuesta de panic no es JSON: %v (%s)", err, panicRec.Body.String())
	}
	if body["error"] != "no se pudo completar la operación" {
		t.Fatalf("error = %q, se esperaba %q", body["error"], "no se pudo completar la operación")
	}

	reqID := panicRec.Header().Get("X-Request-Id")
	if reqID == "" {
		t.Fatal("falta X-Request-Id en respuesta 500 tras panic")
	}

	raw := buf.String()
	if strings.Contains(raw, "mi-secreto-oculto") || strings.Contains(raw, "token=x") {
		t.Fatalf("el log tras panic filtró secretos o query: %s", raw)
	}

	records := parseJSONLogLines(t, raw)
	if len(records) != 2 {
		t.Fatalf("se esperaban 2 logs (recover + access log), llegaron %d: %s", len(records), raw)
	}

	recoverLog := records[0]
	accessLog := records[1]

	if recoverLog["level"] != "ERROR" {
		t.Fatalf("recover level = %v, se esperaba ERROR", recoverLog["level"])
	}
	if recoverLog["request_id"] != reqID {
		t.Fatalf("recover request_id = %v, se esperaba %q", recoverLog["request_id"], reqID)
	}
	if recoverLog["method"] != http.MethodPost || recoverLog["path"] != "/callbacks/snacks/{secret}" {
		t.Fatalf("recover method/path inesperados: %v", recoverLog)
	}
	if recoverLog["route"] != "POST /callbacks/snacks/{secret}" {
		t.Fatalf("recover route = %v, se esperaba POST /callbacks/snacks/{secret}", recoverLog["route"])
	}
	if stack, _ := recoverLog["stack"].(string); stack == "" {
		t.Fatalf("falta stack en el log de panic: %v", recoverLog)
	}

	if accessLog["level"] != "ERROR" {
		t.Fatalf("access log level = %v, se esperaba ERROR", accessLog["level"])
	}
	if accessLog["status"] != float64(http.StatusInternalServerError) {
		t.Fatalf("access log status = %v, se esperaba 500", accessLog["status"])
	}
	if accessLog["request_id"] != reqID {
		t.Fatalf("access log request_id = %v, se esperaba %q", accessLog["request_id"], reqID)
	}

	nextReq := httptest.NewRequest(http.MethodGet, "/ok", nil)
	nextRec := httptest.NewRecorder()
	wrapped.ServeHTTP(nextRec, nextReq)

	if nextRec.Code != http.StatusOK {
		t.Fatalf("el siguiente pedido falló con %d tras el panic", nextRec.Code)
	}
}

func TestWrapPreservesResponseControllerUnwrap(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.New(&buf, slog.LevelInfo)

	flushed := false
	mux := http.NewServeMux()
	mux.HandleFunc("GET /flush", func(w http.ResponseWriter, r *http.Request) {
		rc := http.NewResponseController(w)
		if err := rc.Flush(); err != nil {
			t.Fatalf("ResponseController.Flush falló: %v", err)
		}
		flushed = true
	})

	wrapped := Wrap(mux, logger)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/flush", nil))

	if !flushed || !rec.Flushed {
		t.Fatal("el wrapper no preservó Unwrap() para http.ResponseController")
	}
}

func TestWrapPanicAfterHeadersWrittenAbortsConnectionAndLogsError(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.New(&buf, slog.LevelDebug)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /partial", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "parcial")
		panic("fallo tardío")
	})

	wrapped := Wrap(mux, logger)
	rec := httptest.NewRecorder()

	var caught any
	func() {
		defer func() {
			caught = recover()
		}()
		wrapped.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/partial", nil))
	}()

	if caught != http.ErrAbortHandler {
		t.Fatalf("se esperaba re-panic de http.ErrAbortHandler tras cabeceras enviadas, llegó %v", caught)
	}
	if rec.Body.String() != "parcial" {
		t.Fatalf("body = %q, no debía anexarse JSON de error tras cabecera enviada", rec.Body.String())
	}

	records := parseJSONLogLines(t, buf.String())
	if len(records) != 2 {
		t.Fatalf("se esperaban 2 logs (recover + access log), llegaron %d: %s", len(records), buf.String())
	}
	if records[0]["level"] != "ERROR" {
		t.Fatalf("recover level = %v, se esperaba ERROR", records[0]["level"])
	}
	if records[1]["level"] != "ERROR" {
		t.Fatalf("access log level = %v, se esperaba ERROR", records[1]["level"])
	}
	if records[1]["aborted"] != true {
		t.Fatalf("access log debe marcar aborted=true: %v", records[1])
	}
}

func TestWrapRePanicsErrAbortHandlerAndLogsAbortedError(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.New(&buf, slog.LevelDebug)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /abort", func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	})

	wrapped := Wrap(mux, logger)

	var caught any
	func() {
		defer func() {
			caught = recover()
		}()
		wrapped.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/abort", nil))
	}()

	if caught != http.ErrAbortHandler {
		t.Fatalf("se esperaba re-panic de http.ErrAbortHandler, llegó %v", caught)
	}

	records := parseJSONLogLines(t, buf.String())
	if len(records) != 1 {
		t.Fatalf("se esperaba 1 access log, llegaron %d: %s", len(records), buf.String())
	}
	if records[0]["level"] != "ERROR" {
		t.Fatalf("access log level = %v, se esperaba ERROR (no 200/INFO)", records[0]["level"])
	}
	if records[0]["aborted"] != true {
		t.Fatalf("access log debe marcar aborted=true: %v", records[0])
	}
}
