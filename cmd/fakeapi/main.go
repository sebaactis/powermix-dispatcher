// Command fakeapi levanta los dos sustitutos locales de los backends de máquinas.
//
// Permite empezar hoy con el trabajo de cliente: la API de batidos y la de snacks
// responden en dos puertos, exigiendo las firmas reales, el código de canje de un
// solo uso y la regla de replay de nonce.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"compras-remotas/internal/fakeapi"
	"compras-remotas/internal/logging"
)

func main() {
	var (
		batidosAddr     string
		snacksAddr      string
		batidosKey      string
		batidosSecret   string
		snacksAccessKey string
		snacksSecret    string
	)

	flag.StringVar(&batidosAddr, "batidos-addr", "127.0.0.1:9401", "dirección para la API falsa de batidos")
	flag.StringVar(&snacksAddr, "snacks-addr", "127.0.0.1:9402", "dirección para la API falsa de snacks")
	flag.StringVar(&batidosKey, "batidos-key", "mykey", "key que espera la API falsa de batidos")
	flag.StringVar(&batidosSecret, "batidos-secret", "mysecret", "secret usado para verificar el key-md5")
	flag.StringVar(&snacksAccessKey, "snacks-access-key", "ak_test", "accessKey que espera la API falsa de snacks")
	flag.StringVar(&snacksSecret, "snacks-secret", "testsecret", "appSecret usado para verificar la firma HMAC")
	flag.Parse()

	logger := logging.New(os.Stdout, slog.LevelInfo)

	servers := []*http.Server{
		{Addr: batidosAddr, Handler: fakeapi.NewBatidos(batidosKey, batidosSecret), ReadHeaderTimeout: 5 * time.Second},
		{Addr: snacksAddr, Handler: fakeapi.NewSnacks(snacksAccessKey, snacksSecret), ReadHeaderTimeout: 5 * time.Second},
	}

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
