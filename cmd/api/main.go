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

	"compras-remotas/internal/catalog"
	"compras-remotas/internal/config"
	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/dispenser/batidos"
	"compras-remotas/internal/dispenser/snacks"
	"compras-remotas/internal/logging"
	"compras-remotas/internal/notify"
	"compras-remotas/internal/order"
	"compras-remotas/internal/store"
	"compras-remotas/internal/sweep"
)

const (
	sweepInterval = 30 * time.Second
	sweepLimit    = 10
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", envOr("CONFIG_PATH", "config.local.json"), "ruta al archivo JSON de topología")
	var combosPath string
	flag.StringVar(&combosPath, "combos", envOr("COMBOS_PATH", "combos.local.json"), "ruta al archivo JSON de combos")
	flag.Parse()

	logger := logging.New(os.Stdout, slog.LevelInfo)

	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Error("no se pudo cargar la configuración", "path", configPath, "error", err)
		os.Exit(1)
	}

	combos, err := catalog.LoadFile(combosPath)
	if err != nil {
		logger.Error("no se pudo cargar el catálogo de combos", "path", combosPath, "error", err)
		os.Exit(1)
	}

	comboCatalog, err := catalog.NewMemory(combos)
	if err != nil {
		logger.Error("no se pudo construir el catálogo en memoria", "path", combosPath, "error", err)
		os.Exit(1)
	}

	batidosClient := batidos.NewClient(cfg.BatidosBaseURL, cfg.BatidosKey, cfg.BatidosSecret)
	snacksClient := snacks.NewClient(cfg.SnacksBaseURL, cfg.SnacksAccessKey, cfg.SnacksAppSecret)

	machines := map[string]dispenser.Dispenser{
		string(config.MachineBatidos): batidos.NewDispenser(batidosClient),
		string(config.MachineSnacks):  snacks.NewDispenser(snacksClient),
	}

	memory := store.NewMemory()

	engine := order.New(memory, machines, order.WithNotifier(notify.NewLog(logger)))

	runner := sweep.New(engine, logger, sweep.WithInterval(sweepInterval), sweep.WithLimit(sweepLimit))

	logger.Info("backend construido",
		"env", cfg.Env,
		"kiosks", len(cfg.Kiosks),
		"combos", len(combos),
		"sweep_interval", sweepInterval.String(),
		"store", "memory",
	)

	logger.Warn("el store es en memoria: el estado se pierde al reiniciar y solo puede correr una instancia")

	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		available, err := comboCatalog.Combos(r.Context())
		if err != nil {
			logger.Error("no se pudo leer el catálogo para el chequeo de salud", "error", err)
			http.Error(w, "no se pudo leer el catálogo", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"env":    cfg.Env,
			"kiosks": len(cfg.Kiosks),
			"combos": len(available),
			"store":  "memory",
		})
	})

	server := &http.Server{
		Addr:              envOr("HTTP_ADDR", "127.0.0.1:8080"),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sweepDone := make(chan struct{})

	go func() {
		defer close(sweepDone)

		if err := runner.Run(ctx); err != nil {
			logger.Error("el barrido de reconciliación terminó con error", "error", err)
		}
	}()

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

	select {
	case <-sweepDone:
		logger.Info("barrido de reconciliación detenido")
	case <-shutdownCtx.Done():
		logger.Error("el barrido de reconciliación no se detuvo a tiempo")
	}
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
