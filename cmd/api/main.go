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
	"compras-remotas/internal/payments/mercadopago"
	"compras-remotas/internal/preflight"
	"compras-remotas/internal/store"
	"compras-remotas/internal/store/postgres"
	"compras-remotas/internal/sweep"
)

const (
	sweepInterval = 30 * time.Second
	sweepLimit    = 10
	seedTimeout   = 30 * time.Second

	paymentProvider = "mercadopago"
)

func main() {
	configPath, combosPath := parseFlags()

	logger := newLogger()

	cfg, err := config.Load(configPath)
	if err != nil {
		exitWithError(logger, "no se pudo cargar la configuración", "path", configPath, "error", err)
	}

	warnMissingCallbackSecrets(cfg, logger)

	combos, comboCatalog := loadCatalog(combosPath, logger)

	batidosClient := batidos.NewClient(cfg.BatidosBaseURL, cfg.BatidosKey, cfg.BatidosSecret)
	snacksClient := snacks.NewClient(cfg.SnacksBaseURL, cfg.SnacksAccessKey, cfg.SnacksAppSecret)

	machines := buildMachines(cfg, batidosClient, snacksClient, logger)

	orders, closeStore, err := openStore(cfg, combos, logger)
	if err != nil {
		exitWithError(logger, "no se pudo preparar el store", "store", cfg.Store, "error", err)
	}

	availability := gate.New(preflight.New(batidosClient, snacksClient, preflight.WithLogger(logger)))

	engine := buildEngine(cfg, orders, machines, availability, buildNotifier(cfg, logger), logger)

	runner, apiDeps := buildSweepAndAPIDeps(cfg, engine, orders, comboCatalog, logger)

	tabletAPI, err := httpapi.New(apiDeps, httpapi.WithLogger(logger))
	if err != nil {
		logger.Error("no se pudo construir la API de la tablet", "error", err)
		closeStore()
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

	mux := buildMux(cfg, engine, comboCatalog, tabletAPI, logger)

	server := newServer(envOr("HTTP_ADDR", "127.0.0.1:8080"), httpapi.Wrap(mux, logger), logger)

	serve(server, runner, engine, closeStore, cfg, logger)
}

func parseFlags() (configPath, combosPath string) {
	flag.StringVar(&configPath, "config", envOr("CONFIG_PATH", "config.local.json"), "ruta al archivo JSON de topología")
	flag.StringVar(&combosPath, "combos", envOr("COMBOS_PATH", "combos.local.json"), "ruta al archivo JSON de combos")
	flag.Parse()

	return configPath, combosPath
}

func newLogger() *slog.Logger {
	logger, err := logging.NewFromEnv(os.Stdout)
	if err != nil {
		exitWithError(logging.New(os.Stdout, slog.LevelInfo), "nivel de log inválido", "error", err)
	}

	return logger
}

func exitWithError(logger *slog.Logger, msg string, args ...any) {
	logger.Error(msg, args...)
	os.Exit(1)
}

func warnMissingCallbackSecrets(cfg *config.Config, logger *slog.Logger) {
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
}

func loadCatalog(path string, logger *slog.Logger) ([]catalog.Combo, *catalog.Memory) {
	combos, err := catalog.LoadFile(path)
	if err != nil {
		exitWithError(logger, "no se pudo cargar el catálogo de combos", "path", path, "error", err)
	}

	comboCatalog, err := catalog.NewMemory(combos)
	if err != nil {
		exitWithError(logger, "no se pudo construir el catálogo en memoria", "path", path, "error", err)
	}

	return combos, comboCatalog
}

func buildMachines(cfg *config.Config, batidosClient *batidos.Client, snacksClient *snacks.Client, logger *slog.Logger) map[machine.Kind]dispenser.Dispenser {
	batidosNotifyURL := func(orderID string) string {
		return httpapi.BatidosCallbackURL(cfg.PublicBaseURL, cfg.BatidosCallbackSecret, orderID)
	}

	if cfg.PublicBaseURL == "" || cfg.BatidosCallbackSecret == "" {
		logger.Warn("la campaña de batidos se va a crear sin notifyUrl: el callback de batidos no va a llegar",
			"base_url_env", "PUBLIC_BASE_URL",
			"secret_env", "BATIDOS_CALLBACK_SECRET",
		)
	}

	return map[machine.Kind]dispenser.Dispenser{
		machine.Batidos: batidos.NewDispenser(batidosClient, batidos.WithNotifyURL(batidosNotifyURL)),
		machine.Snacks:  snacks.NewDispenser(snacksClient),
	}
}

func buildNotifier(cfg *config.Config, logger *slog.Logger) notify.Notifier {
	if !cfg.TelegramEnabled() {
		logger.Warn("Telegram no está configurado: los avisos a soporte quedan sólo en el log",
			"token_env", "TELEGRAM_BOT_TOKEN",
			"chat_env", "TELEGRAM_CHAT_ID",
		)

		return notify.NewLog(logger)
	}

	kioskNames := make(map[string]string, len(cfg.Kiosks))
	for _, kiosk := range cfg.Kiosks {
		kioskNames[kiosk.ID] = kiosk.Name
	}

	notifier := notify.NewTelegram(logger, cfg.TelegramBaseURL, cfg.TelegramBotToken, cfg.TelegramChatID, cfg.Env,
		notify.WithTelegramKiosks(kioskNames),
	)
	logger.Info("avisos a soporte por Telegram activos", "base_url", cfg.TelegramBaseURL)

	return notifier
}

func buildEngine(cfg *config.Config, orders store.Store, machines map[machine.Kind]dispenser.Dispenser, availability *gate.Gate, notifier notify.Notifier, logger *slog.Logger) *order.Engine {
	engineOpts := []order.Option{
		order.WithLogger(logger),
		order.WithNotifier(notifier),
		order.WithAvailabilityChecker(availability),
		order.WithBackgroundErrors(func(orderID string, err error) {
			logger.Error("el despacho en segundo plano terminó con error", "order_id", orderID, "error", err)
		}),
	}

	if cfg.MercadoPagoEnabled() {
		mpClient := mercadopago.NewClient(cfg.MercadoPagoBaseURL, cfg.MercadoPagoAccessToken)
		engineOpts = append(engineOpts, order.WithQRPayments(paymentProvider, mpClient))
	}

	return order.New(orders, machines, engineOpts...)
}

func buildSweepAndAPIDeps(cfg *config.Config, engine *order.Engine, orders store.Store, comboCatalog *catalog.Memory, logger *slog.Logger) (*sweep.Runner, httpapi.Deps) {
	runnerOpts := []sweep.Option{sweep.WithInterval(sweepInterval), sweep.WithLimit(sweepLimit)}
	apiDeps := httpapi.Deps{
		Catalog:      comboCatalog,
		OrderCreator: engine,
		PaidMarker:   engine,
		Orders:       orders,
		Kiosks:       cfg.Kiosks,
		Env:          cfg.Env,
	}

	if cfg.MercadoPagoEnabled() {
		runnerOpts = append(runnerOpts, sweep.WithPaymentSync(engine))
		apiDeps.QRRequester = engine
		apiDeps.Payments = orders

		logger.Info("cobro con QR de Mercado Pago activo: el pago simulado está apagado",
			"base_url", cfg.MercadoPagoBaseURL,
			"webhook_path", "/callbacks/mercadopago/***",
		)
	} else {
		logger.Warn("Mercado Pago no está configurado: se cobra con el pago simulado (POST /orders/{id}/pay)",
			"token_env", "MP_ACCESS_TOKEN",
		)
	}

	return sweep.New(engine, logger, runnerOpts...), apiDeps
}

func buildMux(cfg *config.Config, engine *order.Engine, comboCatalog *catalog.Memory, tabletAPI *httpapi.Handler, logger *slog.Logger) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", healthHandler(cfg, comboCatalog, logger))

	tabletAPI.Register(mux)
	httpapi.NewCallbacks(engine, httpapi.CallbackSecrets{
		Batidos: cfg.BatidosCallbackSecret,
		Snacks:  cfg.SnacksCallbackSecret,
	}, httpapi.WithCallbacksLogger(logger)).Register(mux)

	if cfg.MercadoPagoEnabled() {
		httpapi.NewMercadoPagoWebhook(engine, httpapi.MercadoPagoSecrets{
			Callback: cfg.MercadoPagoCallbackSecret,
			Webhook:  cfg.MercadoPagoWebhookSecret,
		}, httpapi.WithMercadoPagoLogger(logger)).Register(mux)
	}

	return mux
}

func healthHandler(cfg *config.Config, comboCatalog *catalog.Memory, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		available, err := comboCatalog.Combos(r.Context())
		if err != nil {
			logger.ErrorContext(r.Context(), "no se pudo leer el catálogo para el chequeo de salud", "error", err)
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
	}
}

func serve(server *http.Server, runner *sweep.Runner, engine *order.Engine, closeStore func(), cfg *config.Config, logger *slog.Logger) {
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

	shutdown(shutdownDeps{
		shutdownHTTP:   server.Shutdown,
		sweepDone:      sweepDone,
		waitDispatches: engine.Wait,
		closeStore:     closeStore,
		httpTimeout:    httpShutdownTimeout,
		drainTimeout:   drainTimeout,
		logger:         logger,
	})
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
