package main

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"compras-remotas/internal/logging"
)

func TestNewServerSetsTheTimeoutsAndTheHandler(t *testing.T) {
	handler := http.NewServeMux()

	server := newServer("127.0.0.1:9999", handler)

	if server.Addr != "127.0.0.1:9999" {
		t.Fatalf("Addr quedó en %q, se esperaba %q", server.Addr, "127.0.0.1:9999")
	}
	if server.Handler != handler {
		t.Fatalf("Handler no es el mux que se pasó: se esperaba la misma identidad")
	}
	if server.ReadHeaderTimeout != 5*time.Second {
		t.Fatalf("ReadHeaderTimeout quedó en %v, se esperaban 5s", server.ReadHeaderTimeout)
	}
	if server.ReadTimeout != 10*time.Second {
		t.Fatalf("ReadTimeout quedó en %v, se esperaban 10s", server.ReadTimeout)
	}
	if server.WriteTimeout != 60*time.Second {
		t.Fatalf("WriteTimeout quedó en %v, se esperaban 60s", server.WriteTimeout)
	}
	if server.IdleTimeout != 60*time.Second {
		t.Fatalf("IdleTimeout quedó en %v, se esperaban 60s", server.IdleTimeout)
	}
	if server.MaxHeaderBytes != 64<<10 {
		t.Fatalf("MaxHeaderBytes quedó en %d, se esperaban %d", server.MaxHeaderBytes, 64<<10)
	}
}

func TestShutdownConstantsMatchGracePeriodBudget(t *testing.T) {
	if httpShutdownTimeout != 10*time.Second {
		t.Fatalf("httpShutdownTimeout = %v, se esperaban 10s", httpShutdownTimeout)
	}
	if drainTimeout != 60*time.Second {
		t.Fatalf("drainTimeout = %v, se esperaban 60s", drainTimeout)
	}
}

func TestShutdownWaitsSweepThenDispatchesThenClosesStore(t *testing.T) {
	var mu sync.Mutex
	var events []string
	record := func(ev string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, ev)
	}

	sweepDone := make(chan struct{})
	var logs bytes.Buffer

	go func() {
		time.Sleep(10 * time.Millisecond)
		record("sweep_done")
		close(sweepDone)
	}()

	shutdown(shutdownDeps{
		shutdownHTTP: func(ctx context.Context) error {
			record("http_shutdown")
			return nil
		},
		sweepDone: sweepDone,
		waitDispatches: func() {
			select {
			case <-sweepDone:
			default:
				t.Error("waitDispatches empezó antes de que terminara el barrido")
			}
			record("wait_dispatches")
		},
		closeStore: func() {
			record("close_store")
		},
		httpTimeout:  50 * time.Millisecond,
		drainTimeout: 100 * time.Millisecond,
		logger:       logging.New(&logs, slog.LevelInfo),
	})

	want := []string{"http_shutdown", "sweep_done", "wait_dispatches", "close_store"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("secuencia = %v, se esperaba %v", events, want)
	}
}

func TestShutdownGivesDrainFullBudgetAfterSlowHTTP(t *testing.T) {
	sweepDone := make(chan struct{})
	close(sweepDone)

	var logs bytes.Buffer
	storeClosed := false

	shutdown(shutdownDeps{
		shutdownHTTP: func(ctx context.Context) error {
			time.Sleep(25 * time.Millisecond)
			if err := ctx.Err(); err != nil {
				return err
			}
			return nil
		},
		sweepDone: sweepDone,
		waitDispatches: func() {
			time.Sleep(20 * time.Millisecond)
		},
		closeStore: func() {
			storeClosed = true
		},
		httpTimeout:  40 * time.Millisecond,
		drainTimeout: 35 * time.Millisecond,
		logger:       logging.New(&logs, slog.LevelInfo),
	})

	if !storeClosed {
		t.Fatal("el store no se cerró tras el drenaje")
	}

	output := logs.String()
	if strings.Contains(output, "level=ERROR") {
		t.Fatalf("el drenaje no debería expirar por un cierre HTTP lento: %s", output)
	}
	if !strings.Contains(output, "Despachos en segundo plano terminados") {
		t.Fatalf("falta confirmación de despachos terminados: %s", output)
	}
}

func TestShutdownSharesDrainBudgetAndClosesStoreOnTimeout(t *testing.T) {
	sweepDone := make(chan struct{})
	go func() {
		time.Sleep(20 * time.Millisecond)
		close(sweepDone)
	}()

	releaseDispatch := make(chan struct{})
	defer close(releaseDispatch)

	var logs bytes.Buffer
	storeClosed := false

	start := time.Now()
	shutdown(shutdownDeps{
		shutdownHTTP: func(context.Context) error { return nil },
		sweepDone:    sweepDone,
		waitDispatches: func() {
			if storeClosed {
				t.Error("el store se cerró antes de que venciera el drenaje")
			}
			<-releaseDispatch
		},
		closeStore: func() {
			storeClosed = true
		},
		httpTimeout:  20 * time.Millisecond,
		drainTimeout: 35 * time.Millisecond,
		logger:       logging.New(&logs, slog.LevelInfo),
	})
	elapsed := time.Since(start)

	if !storeClosed {
		t.Fatal("el store debe cerrarse aunque venza el plazo de drenaje")
	}
	if elapsed > 120*time.Millisecond {
		t.Fatalf("shutdown tardó %v, no respetó el plazo compartido de drenaje", elapsed)
	}

	output := logs.String()
	if !strings.Contains(output, "Quedaron despachos en curso al apagar: el barrido los retoma al volver a arrancar") {
		t.Fatalf("falta el log de trabajo pendiente al vencer el drenaje: %s", output)
	}
}

func TestShutdownClosesStoreAndSkipsEngineWaitWhenSweepTimesOut(t *testing.T) {
	sweepDone := make(chan struct{})
	waitCalled := false
	storeClosed := false
	var logs bytes.Buffer

	shutdown(shutdownDeps{
		shutdownHTTP: func(context.Context) error { return nil },
		sweepDone:    sweepDone,
		waitDispatches: func() {
			waitCalled = true
		},
		closeStore: func() {
			storeClosed = true
		},
		httpTimeout:  20 * time.Millisecond,
		drainTimeout: 20 * time.Millisecond,
		logger:       logging.New(&logs, slog.LevelInfo),
	})

	if waitCalled {
		t.Fatal("waitDispatches no debe correr si el barrido no se detuvo antes de vencer el drenaje")
	}
	if !storeClosed {
		t.Fatal("el store debe cerrarse aunque el barrido no se haya detenido a tiempo")
	}

	output := logs.String()
	if !strings.Contains(output, "El barrido de reconciliación no se detuvo a tiempo") {
		t.Fatalf("falta log de timeout del barrido: %s", output)
	}
	if !strings.Contains(output, "Quedaron despachos en curso al apagar: el barrido los retoma al volver a arrancar") {
		t.Fatalf("falta log de despachos pendientes: %s", output)
	}
}
