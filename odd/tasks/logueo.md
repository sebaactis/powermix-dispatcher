# F9 — Logueo

Fix F9 de `odd/tasks/revision-produccion.md`, rama `fix/logging` desde `main`.
Hoy el nivel está fijo en Info y el formato es texto (`cmd/api/main.go:48`, `internal/logging/logging.go:31`). No hay access log, recover HTTP, `request_id` ni `ErrorLog` estructurado (`cmd/api/server.go:16-25`), y el `order_id` falta en varios logs.

## Specs

- **S1** — Formato: «JSON siempre». `internal/logging` arma un `slog.JSONHandler` (conserva lo que hace hoy `ReplaceAttr`) y lo usan los tres binarios (`cmd/api`, `cmd/migrate`, `cmd/fakeapi`).
- **S2** — Nivel configurable con `LOG_LEVEL` (`debug`, `info`, `warn`, `error`; sin distinguir mayúsculas; por defecto `info`). Un valor inválido hace fallar el arranque con un error claro, igual que el resto de la configuración.
- **S3** — `request_id`: «Lo genera siempre el servidor (recomendado)». Se genera un id nuevo en cada pedido HTTP (no se usa el `X-Request-Id` que manda el cliente) y se devuelve en el header `X-Request-Id` de la respuesta.
- **S4** — `request_id` automático: un handler de slog agrega el `request_id` del contexto a cada log que use `*Context(ctx, ...)` dentro de un pedido. «request_id automático, order_id a mano (recomendado)».
- **S5** — Access log: «Todos; 4xx en warn, 5xx en error (recomendado)». Una línea por pedido con método, ruta **sin secretos** (`redactCallbackPath`), status, duración, bytes y `request_id`. Nunca la query string ni headers (`X-Signature`, `Authorization`).
- **S6** — Recover HTTP: un panic en un handler responde 500 JSON («no se pudo completar la operación», como `fail`), loguea un error con stack, método, ruta sin secretos y `request_id`, y el servidor sigue atendiendo.
- **S7** — `http.Server.ErrorLog` conectado al logger estructurado (`slog.NewLogLogger`, nivel error).
- **S8** — `order_id` a mano en los logs donde falta y está disponible: barrido (`internal/sweep/runner.go`), chequeo previo (`internal/preflight/checker.go`), errores HTTP (`internal/httpapi/errors.go`), webhook de Mercado Pago (`internal/httpapi/mercadopago.go`) y errores de callbacks (`internal/httpapi/callbacks.go`). Donde el dato no esté disponible, no se inventa.

## Tasks

- [ ] T1 — S1-S7 — writer `gentle-ai-worker` — commit: pendiente.
- [ ] T2 — S8 — writer `gentle-ai-worker` (después de T1, comparte `internal/httpapi`) — commit: pendiente.

## Log

- **L1** — «Dale» a publicar F8 y arrancar F9 (2026-11-22).
- **L2** — Decisiones: request_id «Lo genera siempre el servidor (recomendado)»; formato «JSON siempre»; access log «Todos; 4xx en warn, 5xx en error (recomendado)»; order_id «request_id automático, order_id a mano (recomendado)».
- **L3** — Mapa: los callbacks llevan el secreto en la ruta (`/callbacks/{batidos,snacks,mercadopago}/{secret}`); `redactCallbackPath` está en `internal/httpapi/callback_url.go:17-30`. Hoy los routers se registran directo sobre `mux` en `cmd/api/main.go:188-221`.
