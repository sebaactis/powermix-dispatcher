package main

import (
	"context"
	"io"
	"log/slog"
	"time"
)

const (
	httpShutdownTimeout = 10 * time.Second
	drainTimeout        = 60 * time.Second
)

type shutdownDeps struct {
	shutdownHTTP   func(context.Context) error
	sweepDone      <-chan struct{}
	waitDispatches func()
	closeStore     func()
	httpTimeout    time.Duration
	drainTimeout   time.Duration
	logger         *slog.Logger
}

func shutdown(deps shutdownDeps) {
	logger := deps.logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}

	if deps.shutdownHTTP != nil {
		httpCtx, cancelHTTP := context.WithTimeout(context.Background(), deps.httpTimeout)
		if err := deps.shutdownHTTP(httpCtx); err != nil {
			logger.Error("no se pudo cerrar de forma ordenada", "error", err)
		}
		cancelHTTP()
	}

	drainCtx, cancelDrain := context.WithTimeout(context.Background(), deps.drainTimeout)
	defer cancelDrain()

	sweepStopped := true
	if deps.sweepDone != nil {
		select {
		case <-deps.sweepDone:
			logger.Info("barrido de reconciliación detenido")
		case <-drainCtx.Done():
			sweepStopped = false
			logger.Error("el barrido de reconciliación no se detuvo a tiempo")
		}
	}

	if !sweepStopped {
		logger.Error("quedaron despachos en curso al apagar: el barrido los retoma al volver a arrancar")
	} else if deps.waitDispatches != nil {
		dispatchDone := make(chan struct{})
		go func() {
			deps.waitDispatches()
			close(dispatchDone)
		}()

		select {
		case <-dispatchDone:
			logger.Info("despachos en segundo plano terminados")
		case <-drainCtx.Done():
			logger.Error("quedaron despachos en curso al apagar: el barrido los retoma al volver a arrancar")
		}
	}

	if deps.closeStore != nil {
		deps.closeStore()
	}
}
