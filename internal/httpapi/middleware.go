package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"compras-remotas/internal/logging"
)

const requestIDHeader = "X-Request-Id"

func Wrap(next http.Handler, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	return requestIDMiddleware(accessLogMiddleware(logger, recoverMiddleware(logger, next)))
}

func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		w.Header().Set(requestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(logging.WithRequestID(r.Context(), id)))
	})
}

func accessLogMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{w: w, status: http.StatusOK}
		completed := false

		defer func() {
			aborted := !completed
			level := accessLogLevel(rec.status, aborted)
			safePath, route := logPathAndRoute(r)
			attrs := []any{
				"method", r.Method,
				"path", safePath,
				"status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(),
				"bytes", rec.bytes,
			}
			if route != "" {
				attrs = append(attrs, "route", route)
			}
			if aborted {
				attrs = append(attrs, "aborted", true)
			}
			logger.Log(r.Context(), level, "pedido HTTP", attrs...)
		}()

		next.ServeHTTP(rec, r)
		completed = true
	})
}

func recoverMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			panicked := recover()
			if panicked == nil {
				return
			}
			if panicked == http.ErrAbortHandler {
				panic(panicked)
			}

			safePath, route := logPathAndRoute(r)
			attrs := []any{
				"method", r.Method,
				"path", safePath,
				"panic", fmt.Sprint(panicked),
				"stack", string(debug.Stack()),
			}
			if route != "" {
				attrs = append(attrs, "route", route)
			}
			logger.ErrorContext(r.Context(), "panic al atender el pedido HTTP", attrs...)

			if sr, ok := w.(*statusRecorder); ok && sr.wroteHeader {
				panic(http.ErrAbortHandler)
			}
			writeError(w, http.StatusInternalServerError, "no se pudo completar la operación")
		}()

		next.ServeHTTP(w, r)
	})
}

func logPathAndRoute(r *http.Request) (safePath, route string) {
	if r.Pattern != "" {
		if _, patternPath, ok := strings.Cut(r.Pattern, " "); ok {
			return patternPath, r.Pattern
		}
		return r.Pattern, r.Pattern
	}
	return redactCallbackPath(r.URL.Path), ""
}

func accessLogLevel(status int, aborted bool) slog.Level {
	if aborted || status >= http.StatusInternalServerError {
		return slog.LevelError
	}
	if status >= http.StatusBadRequest {
		return slog.LevelWarn
	}
	return slog.LevelInfo
}

func newRequestID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(buf[:])
}

type statusRecorder struct {
	w           http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (r *statusRecorder) Header() http.Header {
	return r.w.Header()
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.wroteHeader {
		return
	}
	r.status = code
	r.wroteHeader = true
	r.w.WriteHeader(code)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	if !r.wroteHeader {
		r.status = http.StatusOK
		r.wroteHeader = true
	}
	n, err := r.w.Write(p)
	r.bytes += n
	return n, err
}

func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.w
}
