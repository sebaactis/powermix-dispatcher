# F7 — Apagado ordenado

Fix F7 de `odd/tasks/revision-produccion.md`, rama `fix/graceful-shutdown` desde `main`.
Hoy un plazo único de 10 s (`cmd/api/main.go:246`) más el SIGKILL de Docker a los 10 s cortan despachos en curso (batidos hasta 40 s, snacks 20 s, Telegram 13,5 s): no hay doble despacho, pero la línea termina `in_doubt` con aviso a soporte aunque la máquina haya entregado.

## Specs

- **S1** — Plazos separados: «unos segundos para el HTTP y hasta ~60 s para los despachos en curso». `server.Shutdown` con su propio plazo (10 s); después, un plazo de drenaje de 60 s compartido por la espera del barrido y la de `engine.Wait()`, en ese orden (el barrido puede crear despachos nuevos vía `SyncPendingPayments` → `acceptPayment` → `spawn`, así que se espera al barrido antes de `engine.Wait()`).
- **S2** — «El barrido no arranca trabajo nuevo una vez llegada la señal»: con el contexto cancelado, `SyncOverdueLines`, `SyncPendingPayments` y cualquier bucle de una vuelta del barrido dejan de tomar ítems nuevos. Un despacho que el barrido ya empezó (`resumeStalledLine` → `dispatchWithinDeadline`) no se corta por la señal: termina dentro de su propio plazo de línea y guarda su resultado, igual que los despachos de fondo (`context.WithoutCancel`).
- **S3** — «La base se cierra recién cuando terminan los despachos»: `closeStore` corre después del drenaje. Si vence el plazo de drenaje, se loguea qué quedó pendiente (el mensaje actual sobre el barrido que los retoma) y se cierra igual.
- **S4** — «`stop_grace_period: 75s` en `docker-compose.yml`» para el servicio `api`.
- **S6** — «El motor deja de aceptar despachos nuevos cuando empieza el apagado»: `Engine.Wait()` marca el motor como cerrado y `spawn` ya no arranca trabajo (lo loguea); un mutex garantiza que ningún `inFlight.Add` corra en paralelo con `Wait`. Un pago aceptado en ese momento queda `dispatching` con líneas `pending` y el barrido lo despacha al reiniciar (`StalledLines` → `resumeStalledLine`). Corregir también las líneas de `main.go` desfasadas en `docs/arquitectura.md`.
- **S5** — Documentar en `README.md` y `docs/arquitectura.md` el requisito para cualquier orquestador: al menos 75 s entre SIGTERM y SIGKILL (por ejemplo `terminationGracePeriodSeconds` en Kubernetes, `TimeoutStopSec` en systemd), porque producción todavía no está definida.

Fuera de alcance (F7b, fix aparte): guardar el código de campaña de batidos antes de `ProduceCoffee` para poder reconciliar después de un corte abrupto.

## Tasks

- [ ] T1 — S1-S6 — writer `gentle-ai-worker` — commit: pendiente.

## Log

- **L1** — «Si. Mergea, y despues F7.» (2026-11-22)
- **L2** — Decisión: «Opción 1 ahora, la 2 como fix aparte».
- **L3** — Dónde corre producción: «Todavía no lo sé» → se configura docker-compose y se documenta el requisito.
- **L4** — Mapa verificado: el plazo de línea se guarda antes de llamar a la máquina (`internal/order/dispatch.go:36-39`), así que un corte no causa doble despacho: `OverdueLines` reconcilia la línea. `docker-compose.yml` y `Dockerfile` no definen período de gracia.
- **L5** — La verificación independiente dio S1, S2, S4 y S5 PASS y S3 PARTIAL, sin bloqueantes. Hallazgo real en producción: si `server.Shutdown` vence con un webhook de Mercado Pago activo, `spawn` puede llamar `inFlight.Add` en paralelo con `Engine.Wait()` y hacer panic (ya pasaba antes del cambio). `MarkPaid` dentro del handler corre sólo en sandbox (`internal/httpapi/orders.go:79-82`), así que es menor. Decisión del usuario: «Sí, agregarlo a F7» → S6.
