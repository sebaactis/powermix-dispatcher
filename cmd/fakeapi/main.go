package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"compras-remotas/internal/dispenser/batidos"
	"compras-remotas/internal/fakeapi"
	"compras-remotas/internal/logging"
)

func main() {
	var (
		batidosAddr     string
		snacksAddr      string
		adminAddr       string
		batidosKey      string
		batidosSecret   string
		batidosDeviceID string
		batidosGoodsID  string
		snacksAccessKey string
		snacksSecret    string
		mpAddr          string
		mpAccessToken   string
		mpWebhookSecret string
		mpWebhookURL    string
	)

	flag.StringVar(&batidosAddr, "batidos-addr", "127.0.0.1:9401", "dirección para la API falsa de batidos")
	flag.StringVar(&snacksAddr, "snacks-addr", "127.0.0.1:9402", "dirección para la API falsa de snacks")
	flag.StringVar(&adminAddr, "admin-addr", "127.0.0.1:9403", "dirección loopback para la administración del falso (sólo pruebas locales)")
	flag.StringVar(&batidosKey, "batidos-key", "mykey", "key que espera la API falsa de batidos")
	flag.StringVar(&batidosSecret, "batidos-secret", "mysecret", "secret usado para verificar el key-md5")
	flag.StringVar(&batidosDeviceID, "batidos-device", "dev-e2e-1", "device id de batidos para el que se siembra el catálogo")
	flag.StringVar(&batidosGoodsID, "batidos-goods", "goods-1", "goodsId listado y con stock que se siembra al arrancar; vacío desactiva la siembra")
	flag.StringVar(&snacksAccessKey, "snacks-access-key", "ak_test", "accessKey que espera la API falsa de snacks")
	flag.StringVar(&snacksSecret, "snacks-secret", "testsecret", "appSecret usado para verificar la firma HMAC")
	flag.StringVar(&mpAddr, "mp-addr", "127.0.0.1:9404", "dirección para el Mercado Pago falso")
	flag.StringVar(&mpAccessToken, "mp-access-token", "TEST-fake-token", "access token que espera el Mercado Pago falso")
	flag.StringVar(&mpWebhookSecret, "mp-webhook-secret", "fake-webhook-secret", "secreto con el que el Mercado Pago falso firma los webhooks")
	flag.StringVar(&mpWebhookURL, "mp-webhook-url", "", "URL completa del webhook, con el secreto de ruta (ej. http://127.0.0.1:8080/callbacks/mercadopago/<secreto>); vacía no envía avisos")
	flag.Parse()

	logger := logging.New(os.Stdout, slog.LevelInfo)

	if err := requireLoopback(adminAddr); err != nil {
		logger.Error("la administración del falso sólo puede escuchar en loopback", "addr", adminAddr, "error", err)
		os.Exit(1)
	}

	batidosFake := fakeapi.NewBatidos(batidosKey, batidosSecret)
	snacksFake := fakeapi.NewSnacks(snacksAccessKey, snacksSecret)

	if batidosGoodsID != "" {
		batidosFake.SetDeviceGoods(batidosDeviceID, []batidos.DeviceGoods{{ID: batidosGoodsID, Listing: true}})
		logger.Info("catálogo de batidos sembrado",
			"device_id", batidosDeviceID,
			"goods_id", batidosGoodsID,
		)
	} else {
		logger.Warn("la siembra del catálogo de batidos está desactivada: los productos hay que cargarlos por la administración",
			"path", "/admin/batidos/goods",
			"admin_url", "http://"+adminAddr,
		)
	}

	mpFake := fakeapi.NewMercadoPago(mpAccessToken, mpWebhookSecret, mpWebhookURL)
	if mpWebhookURL == "" {
		logger.Warn("el Mercado Pago falso no tiene URL de webhook: los pagos simulados no avisan a la API",
			"flag", "-mp-webhook-url",
		)
	}

	admin := fakeapi.NewAdmin(batidosFake, snacksFake, mpFake)

	servers := []*http.Server{
		{Addr: batidosAddr, Handler: batidosFake, ReadHeaderTimeout: 5 * time.Second},
		{Addr: snacksAddr, Handler: snacksFake, ReadHeaderTimeout: 5 * time.Second},
		{Addr: mpAddr, Handler: mpFake, ReadHeaderTimeout: 5 * time.Second},
		{Addr: adminAddr, Handler: admin, ReadHeaderTimeout: 5 * time.Second},
	}

	logger.Warn("la administración del falso no tiene autenticación y sólo escucha en loopback: no la expongas fuera de esta máquina",
		"addr", adminAddr,
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup
	for _, server := range servers {
		wg.Add(1)
		go func(server *http.Server) {
			defer wg.Done()
			logger.Info("API falsa escuchando", "base_url", "http://"+server.Addr)
			if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("la API falsa se detuvo de forma inesperada", "addr", server.Addr, "error", err)
				stop()
			}
		}(server)
	}

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, server := range servers {
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("no se pudo cerrar de forma ordenada", "addr", server.Addr, "error", err)
		}
	}
	wg.Wait()
}

func requireLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}

	if host == "" {
		return errors.New("falta la dirección: una dirección vacía escucha en todas las interfaces")
	}

	if host == "localhost" {
		return nil
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("%q no es una dirección IP ni localhost", host)
	}

	if !ip.IsLoopback() {
		return fmt.Errorf("%q no es loopback", host)
	}

	return nil
}
