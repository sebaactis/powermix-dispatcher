// Command api es el backend con el que habla la tablet.
//
// En esta etapa solo expone un endpoint de salud: el sentido de este esqueleto es
// que la topología venga de la configuración y se valide al arrancar, para que un
// mueble mal configurado falle acá y no delante de un cliente.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"compras-remotas/internal/config"
	"compras-remotas/internal/logging"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", envOr("CONFIG_PATH", "config.local.json"), "ruta al archivo JSON de topología")
	flag.Parse()

	logger := logging.New(os.Stdout, slog.LevelInfo)

	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Error("no se pudo cargar la configuración", "path", configPath, "error", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	// Los nombres de los campos JSON se mantienen estables: son un contrato
	// legible por máquinas, no texto para una persona.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"env":    cfg.Env,
			"kiosks": len(cfg.Kiosks),
		})
	})

	server := &http.Server{
		Addr:              envOr("HTTP_ADDR", "127.0.0.1:8080"),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("API escuchando", "addr", server.Addr, "env", cfg.Env, "kiosks", len(cfg.Kiosks))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("el servidor se detuvo de forma inesperada", "error", err)
			stop()
		}
	}()

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("no se pudo cerrar de forma ordenada", "error", err)
	}
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
