# Recuperación de panics en goroutines de fondo

Los panics en goroutines de fondo se recuperan y se loguean con contexto sin tumbar el servidor.
Rama: `fix/partial-order-alerts`. Tests: flujo `archive/tests` (`docs/arquitectura.md`, «Tests: dónde viven y cómo se usan»).

## Specs

- **S1** — Un panic dentro de una goroutine de fondo (`internal/order/engine.go` `spawn`, `internal/order/payment.go` `dispatchLines`, `internal/preflight/checker.go:104-111`) no debe caer el proceso.
- **S2** — Cuando el panic interrumpe el despacho de una línea del pedido, esa línea queda `in_doubt` (entrando en el circuito de reconciliación existente) y soporte recibe el aviso de panic (`KindDispatchPanic`, identificando la línea/producto afectado) con su propia clave de deduplicación, de modo que tanto ese aviso como el aviso posterior de la orden `in_doubt` (`KindOrderInDoubt`) se entregan correlacionados por `order_id` mediante el puerto `Notifier` existente.
- **S3** — El panic se registra con `slog` estructurado incluyendo el valor del panic y el stack, además de los ids de orden y línea cuando estén disponibles.
- **S4** — En preflight (`internal/preflight/checker.go`), un panic se recupera y sale como error hacia el invocador, sin caer el proceso.

## Tasks

- [x] T1 — S1, S2, S3, S4 — recuperación de panics en `internal/order/engine.go` (`spawn`), `internal/order/payment.go` (`dispatchLines`), `internal/preflight/checker.go`, aviso dedicado `KindDispatchPanic` en `internal/notify/` y cableado de `logger` en `cmd/api/main.go` — worker — commit: 1915a23

## Log

- **L1** — F3 en `odd/tasks/revision-produccion.md`: «Sin `recover()` en goroutines de fondo: un panic tira el servidor (`internal/order/engine.go:171-180` `spawn`, `internal/order/payment.go:65-70` `dispatchLines`, `internal/preflight/checker.go:104-111`). Decisión de negocio pendiente: qué estado queda en un pedido cuyo despacho se interrumpe por un panic (propuesta: línea `in_doubt` + aviso a soporte).»
- **L2** — Decisión del usuario: «in_doubt + aviso a soporte (recomendado)» — "No sabemos si la máquina entregó. La línea entra en el circuito de reconciliación que ya existe y soporte recibe un aviso por Telegram."
- **L3** — T1: RED en `TestEngineMarkPaidRecoversDispenserPanic`, `TestEngineSyncPaymentRecoversBackgroundDispatchPanic` (`panic: batidos boom`) y `TestCheckerRecoversPanicFromProviderGoroutines` (`panic: batidos preflight boom`); GREEN `go build ./...`, `go vet ./...` y `go test -count=1 ./...` en verde (`-race` no disponible en Windows sin CGO).
- **L4** — Cableado en `cmd/api/main.go`: `preflight.WithLogger(logger)` en `preflight.New` y `order.WithLogger(logger)` en `engineOpts`; `go build ./...`, `go vet ./...` y `go test -count=1 ./...` en verde.
- **L5** — Decisión del usuario (D1): «Vamos con A. Pero que los avisos esten interrelacionados. Es decir, tengamos un request id (si ya no lo tenemos) que matchee una linea de mensajes/logs. Entonces: 1) Recibimos el aviso de la duda del despacho del batido con un request id 123 2) Que el aviso del pedido en duda por este tema sea tambien id 123.»
- **L6** — Decisión de seguimiento (D1): correlation ID = el `order_id` existente (ya impreso en las alertas de Telegram en `internal/notify/telegram.go:160` y logueado como `order_id`); un nuevo `request_id` HTTP queda diferido a F9.
- **L7** — D1 y D2: RED observada (`undefined: KindDispatchPanic` en `internal/notify` e `internal/order`); agregado `notify.KindDispatchPanic` con estilo en `internal/notify/telegram.go` y clave de deduplicación propia en `internal/order/payment.go`; renombrados los helpers de `internal/preflight/checker_panic_test.go` e `internal/order/panic_recovery_test.go` para evitar colisiones con `archive/tests`.
- **L8** — Verificación independiente aprobada para S1-S4 (`go build ./...`, `go vet ./...` y `go test -count=1 ./...` en verde; `-race` no disponible con `CGO_ENABLED=0`). D1 (aviso dedicado `dispatch_panic`, correlacionado por `order_id`) y D2 (helpers de test renombrados para evitar colisiones con `archive/tests`) quedaron corregidos en el commit `1915a23`. Nota: la rama superó el límite de ~400 líneas por slice (1522 líneas propias en 7 commits desde `main`), mayormente tests.
