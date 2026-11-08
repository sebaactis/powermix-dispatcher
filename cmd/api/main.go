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
	"compras-remotas/internal/gate"
	"compras-remotas/internal/httpapi"
	"compras-remotas/internal/logging"
	"compras-remotas/internal/machine"
	"compras-remotas/internal/notify"
	"compras-remotas/internal/order"
	"compras-remotas/internal/preflight"
	"compras-remotas/internal/store"
	"compras-remotas/internal/store/postgres"
	"compras-remotas/internal/sweep"
)

const (
	sweepInterval = 30 * time.Second
	sweepLimit    = 10
	seedTimeout   = 30 * time.Second
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

	if cfg.BatidosCallbackSecret == "" {
		logger.Warn("no hay secreto configurado para el callback de batidos: ese endpoint va a rechazar todos los avisos",
			"secret_env", "BATIDOS_CALLBACK_SECRET",
		)
	}

	if cfg.SnacksCallbackSecret == "" {
		logger.Warn("no hay secreto configurado para el callback de snacks: ese endpoint va a rechazar todos los avisos",
			"secret_env", "SNACKS_CALLBACK_SECRET",
		)
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

	batidosNotifyURL := func(orderID string) string {
		return httpapi.BatidosCallbackURL(cfg.PublicBaseURL, cfg.BatidosCallbackSecret, orderID)
	}

	if cfg.PublicBaseURL == "" || cfg.BatidosCallbackSecret == "" {
		logger.Warn("la campaña de batidos se va a crear sin notifyUrl: el callback de batidos no va a llegar",
			"base_url_env", "PUBLIC_BASE_URL",
			"secret_env", "BATIDOS_CALLBACK_SECRET",
		)
	}

	machines := map[machine.Kind]dispenser.Dispenser{
		machine.Batidos: batidos.NewDispenser(batidosClient, batidos.WithNotifyURL(batidosNotifyURL)),
		machine.Snacks:  snacks.NewDispenser(snacksClient),
	}

	orders, closeStore, err := openStore(cfg, combos, logger)
	if err != nil {
		logger.Error("no se pudo preparar el store", "store", cfg.Store, "error", err)
		os.Exit(1)
	}
	defer closeStore()

	availability := gate.New(preflight.New(batidosClient, snacksClient))
	engine := order.New(orders, machines, order.WithNotifier(notify.NewLog(logger)), order.WithGate(availability))
	runner := sweep.New(engine, logger, sweep.WithInterval(sweepInterval), sweep.WithLimit(sweepLimit))

	tabletAPI, err := httpapi.New(httpapi.Deps{
		Catalog:   comboCatalog,
		Placer:    engine,
		Confirmer: engine,
		Orders:    orders,
		Kiosks:    cfg.Kiosks,
		Env:       cfg.Env,
	},
		httpapi.WithLogger(logger),
	)
	if err != nil {
		logger.Error("no se pudo construir la API de la tablet", "error", err)
		os.Exit(1)
	}

	logger.Info("backend construido",
		"env", cfg.Env,
		"kiosks", len(cfg.Kiosks),
		"combos", len(combos),
		"sweep_interval", sweepInterval.String(),
		"store", cfg.Store,
	)

	logger.Info("chequeo previo de disponibilidad activo: no se crea una orden si alguna de las dos máquinas no puede vender",
		"gate", "preflight",
		"machines", len(machines),
	)

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
			"store":  cfg.Store,
		})
	})

	tabletAPI.Register(mux)
	httpapi.NewCallbacks(engine, httpapi.CallbackSecrets{
		Batidos: cfg.BatidosCallbackSecret,
		Snacks:  cfg.SnacksCallbackSecret,
	}, httpapi.WithCallbacksLogger(logger)).Register(mux)

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

func openStore(cfg *config.Config, combos []catalog.Combo, logger *slog.Logger) (store.Store, func(), error) {
	if cfg.Store != config.StorePostgres {
		logger.Warn("el store es en memoria: el estado se pierde al reiniciar y solo puede correr una instancia")

		return store.NewMemory(), func() {}, nil
	}

	database, err := postgres.Open(cfg.DatabaseURL)
	if err != nil {
		return nil, nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), seedTimeout)
	defer cancel()

	if err := database.Seed(ctx, cfg.Kiosks, combos); err != nil {
		database.Close()

		return nil, nil, err
	}

	return database, func() {
		if err := database.Close(); err != nil {
			logger.Error("no se pudo cerrar el store de postgres", "error", err)
		}
	}, nil
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
