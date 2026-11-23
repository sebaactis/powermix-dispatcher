# Revisión para producción

Plan de la revisión «sistema 100% funcional»: hallazgos priorizados, cuáles se corrigieron y desde dónde retomar.
Modo de trabajo acordado: un fix por vez, primero se discute si corresponde según el negocio y después se implementa.

## Cómo retomar

1. Leer este documento y `docs/pendientes.md`.
2. **Estado al 2026-11-22:** publicados en `main` F1–F3, F5–F9, F11–F14 y F16. Bloqueados por el proveedor de snacks: **F4** y **F10**. Abiertos: **F7b**, **F15**, **D3–D5** y **P3**. Cada fix nuevo va en una rama desde `main`; sus tests, sólo en `archive/tests`.
3. **Publicado (2026-11-22):** `fix/partial-order-alerts` (F1-F5 y los 8 commits de Telegram que estaban sólo en local) se mergeó a `main` (4afadaf) y se pusheó; `archive/tests` se sincronizó con `main` (b873113) y se pusheó. Regla de entrega: en `main` va todo lo que es de la app; los tests van sólo en `archive/tests`.
4. Traer los tests: `git checkout archive/tests -- $(git ls-tree -r --name-only archive/tests | grep '_test.go$')`. Al terminar, archivarlos en `archive/tests` (merge de la rama con los tests restaurados) y sacarlos del árbol con `git rm -q -f`.

## Partes de la revisión

- [x] P1 — Entrada del pedido, validaciones, proveedores, pago, despacho y logueo: revisado el 2026-11-21 (hallazgos abajo).
- [x] P2 — Hecho (`odd/tasks/documentacion-y-grafico.md`, commits 70d32b4 y 7dcc10e). Original: Documentación (README, `docs/arquitectura.md`, `docs/pendientes.md`, `docs/comentarios-codigo.md` de 2620 líneas): que esté actualizada, clara y concisa. Sin empezar.
- [ ] P3 — Buenas prácticas del código (detalle en H-BP). Sin empezar más allá del adelanto.
- [x] P4 — Hecho (`odd/tasks/documentacion-y-grafico.md`, commit 40f5428). Original: Gráfico `docs/flujo-del-sistema.svg`: actualizado (Mercado Pago, Telegram, partial, in_doubt), claro y moderno. Sin empezar.

## Fixes

### Críticos
- [x] F1 — Avisos de pedidos cobrados sin entrega completa: `partial` no avisaba, y un `failed` en el despacho inicial tampoco. Hecho: `odd/tasks/avisos-entrega-parcial.md`, commits 8931cb3, ab28962, 509e7fc.
- [x] F2 — Errores 5xx/429/ilegibles del proveedor reintentados como rechazo (riesgo de doble despacho). Hecho: `odd/tasks/despacho-errores-ambiguos.md`, commits 9996d4e, 3ca699c; verificación independiente aprobada.
- [x] F3 — Sin `recover()` en goroutines de fondo: un panic tira el servidor (`internal/order/engine.go:171-180` `spawn`, `internal/order/payment.go:65-70` `dispatchLines`, `internal/preflight/checker.go:104-111`). Decisión: «in_doubt + aviso a soporte (recomendado)». Hecho: `odd/tasks/recuperar-panics.md`, commit 1915a23; verificación independiente aprobada.
- [ ] F4 — **Próximo.** `Reconcile` de snacks siempre devuelve `InDoubt` y busca sólo en la página 1 sin filtro (`internal/dispenser/snacks/dispenser.go:106-139`). Puede ser deliberado (la API no informa el resultado del despacho). Confirmar con el proveedor antes de decidir.

### Importantes
- [x] F5 — Hecho: 409 si la clave llega con otro combo o mueble, 400 si supera 128 caracteres, contrato del kiosco documentado (`odd/tasks/client-key-conflicto.md`, commit e8724a9). Original: `client_key` reutilizado con otro combo devuelve el pedido viejo sin avisar, y no tiene largo máximo (`internal/order/place.go:17-25`, `internal/httpapi/draft.go:20,70`).
- [x] F6 — Hecho: 502 si un proveedor no responde en el chequeo previo, `combo_id` recortado; 413 queda fuera por decisión del usuario (`odd/tasks/codigos-http.md`, commit 980b67b). Original: Códigos HTTP: caída del proveedor en el chequeo previo devuelve 500 en vez de 502/503 (`internal/preflight/checker.go:114-116`, `internal/gate/gate.go:76-81`); cuerpo grande devuelve 400 en vez de 413 (`draft.go:29-31`); `combo_id` no se recorta antes de buscarlo (`draft.go:37-41`).
- [x] F7 — Hecho: 10 s para HTTP y 60 s de drenaje (barrido y después despachos), la base se cierra al final, el barrido no toma trabajo nuevo, el motor rechaza despachos nuevos al cerrarse y `stop_grace_period: 75s` (`odd/tasks/apagado-ordenado.md`, commit 3f42c8c; verificación independiente sin bloqueantes).
- [ ] F7b — Si el proceso muere de golpe a mitad de un despacho de batidos, el código de campaña no queda guardado (`internal/dispenser/batidos/dispenser.go:112-135`, se guarda recién en `internal/order/dispatch.go:99,119`) y la línea termina `in_doubt` aunque la máquina haya entregado. Cambia el contrato de `Dispatch`.
- Original F7 — Apagado: el plazo de 10 s (`cmd/api/main.go:245`) puede vencer con despachos de hasta 2 min en curso y `closeStore()` cierra Postgres debajo de ellos.
- [x] F8 — Hecho: `markBrewing` devuelve la línea guardada y, ante stale, recarga y reintenta (`odd/tasks/callback-version-vieja.md`, commit 6bef9cd; verificación independiente sin bloqueantes). Original: El callback de batidos usa una versión vieja de la línea después de `markBrewing` y la extensión del plazo se pierde sin error (`internal/order/callback.go:65-78`, `internal/order/reconcile.go:223-226`).
- [x] F9 — Hecho: JSON, `LOG_LEVEL`, `X-Request-Id`, access log sin secretos, recover HTTP, `ErrorLog` estructurado y `order_id` donde se conoce (`odd/tasks/logueo.md`, commits bd6c573 y 8c6952b; verificación independiente con un bloqueante corregido). Original: Logueo: falta middleware de access log y de recover HTTP, `request_id`/`order_id` en el contexto, nivel configurable (`cmd/api/main.go:48` fijo en Info) y `http.Server.ErrorLog` estructurado (`cmd/api/server.go:16-25`).
- [ ] F10 — Qué precio se manda a Snacks como `RetailPrice` (hoy 0 fijo, `internal/dispenser/snacks/dispenser.go`), pendiente de la duda del proveedor en `docs/pendientes.md` sección C.

### Menores
- [x] F11 — Hecho (2026-11-22, rama `fix/f11-closed-at`): `markClosed` carga `ClosedAt` al pasar a `delivered`, `partial`, `failed` o `cancelled` (no en `in_doubt`) y nunca pisa un cierre previo; se llama desde `refreshOrderStatus` y `closeUnpaid`. Original: `order.ClosedAt` nunca se carga (`internal/store/store.go:36`).
- [x] F12 — Hecho (2026-11-22, rama `fix/f12-settle-retries`): `refreshOrderStatus` hace hasta `maxSettleAttempts = 3` intentos ante `ErrStale` (antes 2); el caso residual de cabecera desfasada quedó como deuda en `docs/pendientes.md`. Original: `refreshOrderStatus` reintenta `ErrStale` una sola vez (`internal/order/settle.go:46`).
- [x] F13 — Hecho (2026-11-22, rama `fix/f13-webhook-mp`): `ts` en segundos o milisegundos (`signatureTime`, corte en 1e12) y, si la query no trae `data.id`, se toma del cuerpo con un `WARN`; la firma se verifica con el `data.id` de la query, como la calcula Mercado Pago. Original: Webhook de Mercado Pago: `data.id` sólo desde la query (`internal/httpapi/mercadopago.go:85`); `ts` asumido en milisegundos (`internal/payments/mercadopago/signature.go:30,43`).
- [x] F14 — Hecho (2026-11-22, rama `fix/f14-response-limit`): se lee `maxResponseBytes+1` y una respuesta mayor a 1 MB devuelve un error común (no `*APIError`), así el despacho la trata como resultado desconocido; mismo patrón que `mercadopago/client.go`. Telegram (`notify/telegram.go:241`) queda como deuda. Original: Respuestas de más de 1 MB se truncan sin error en batidos y snacks (`batidos/client.go:321`, `snacks/client.go:268,428`).
- [ ] F15 — El aviso de entrega parcial muestra el SKU (`DEMO-SNACK-ALFAJOR`) y no el nombre del producto: haría falta el nombre en el catálogo.
- [x] F16 — Hecho (2026-11-22, rama `fix/f16-created-and-sweep-budget`): `POST /orders` responde `201` siempre (también el reintento idempotente) y cada etapa del barrido corre con su propio plazo igual al intervalo (`runStage`), con `WARN` al agotarse. Original: `POST /orders` responde 200 en vez de 201 (`internal/httpapi/orders.go:53`); el barrido no tiene plazo por vuelta (`internal/sweep/runner.go:92-104`).
- [ ] D3 — Los manejadores de recuperación no se protegen a sí mismos: un segundo panic dentro de `markPanickedLineInDoubt` o `notifyOnce` (`internal/order/payment.go:73-78,98,133`) todavía tira el proceso.
- [ ] D4 — Un panic dentro de `spawn` (`internal/order/engine.go:192-211`) pero fuera de una línea (por ejemplo en `settleOrder`, `internal/order/payment.go:88`, `internal/order/settle.go:18`) sólo se loguea: no avisa ni recalcula el estado del pedido; falta confirmar si el barrido lo corrige.
- [ ] D5 — El intento que entró en panic queda `OutcomeAmbiguous` sin `FinishedAt` (`internal/order/dispatch.go:49-56,94`) y no incrementa `line.Attempts` (`internal/order/payment.go:133-167`), distinto de las líneas de F2.

### H-BP — Buenas prácticas (insumo de P3)
- Funciones largas: `main` de `cmd/api/main.go` (232 líneas), `(*Engine).dispatchLine` en `internal/order/dispatch.go` (~100 líneas).
- Código muerto: `Dispenser.Check` y sus dos implementaciones (`internal/dispenser/dispenser.go:55`, `batidos/dispenser.go:71-93`, `snacks/dispenser.go:52-85`), reemplazado por `preflight.Checker`.
- Repetición en 4 lugares del armado request HTTP + `LimitReader` + JSON (`batidos/client.go:293`, `snacks/client.go:244`, `mercadopago/client.go:146`, `notify/telegram.go:221`): candidata a abstracción.
- `isClearBusinessRejection` duplicada en `batidos/dispenser.go` y `snacks/dispenser.go` (2 lugares: se deja así por ahora).
- Errores ignorados con `_ =`: `cmd/api/main.go:198`, `internal/httpapi/respond.go:14`, `internal/notify/telegram.go:116,245`, `internal/payments/mercadopago/amount.go:26`.

## No verificado
- Los tests de Postgres se saltaron en estos fixes (`TEST_DATABASE_URL` sin definir).
- El aviso de entrega parcial no se mandó a Telegram real.

## Log

- **L1** — Pedido original: «Podemos revisar que tenemos pendiente para que mi sistema sea 100% funcional? La entrada del pedido, las validaciones, las comunicaciones con los proveedores de maquina, con el proveedor de pago, y el despacho. Además, logueos. Revisemos que mi doc esté actualizada, clara, y consisa. Que mi codigo sea usando buenas practicas Y que mi grafico visual tambien esté actualizado y sea claro y moderno. Vamos por partes y me vas contando.»
- **L2** — «Planifiquemos cada fix pero abordemoslos juntos para ver si corresponde o no el fix segun el negocio. Vamos 1 por 1.»
- **L3** — «para el catalogo, pone el price_cents porque son datos mockeados, por ahora no son los datos reales.» Hecho en `combos.local.json` (50 + 50 = 100; archivo fuera de Git).
- **L4** — «Me tengo que ir. Frenemos aca, actrualizá los docs para que sepamos de donde retomar despues. Es edecir, el plan, y lo que queda pendiente de ese plan.» Pausa el 2026-11-21 antes de F3.
- **L5** — Decisión sobre F3: «in_doubt + aviso a soporte (recomendado)» — "No sabemos si la máquina entregó. La línea entra en el circuito de reconciliación que ya existe y soporte recibe un aviso por Telegram." En curso en `odd/tasks/recuperar-panics.md`.
- **L6** — «Cerramos por hoy. Guardá todo en la doc.» Pausa el 2026-11-21 después de cerrar F3 (commits 1915a23, 58b3984).
- **L7** — 2026-11-22. F4: el proveedor «Todavía no» respondió. Entrega: «Uno solo. si la mayoria son test acordate que tiene su rama aparte.» Los 3 tests de F3 que habían quedado en la rama se movieron a `archive/tests` (commit 36bc607 en la rama + merge `-s ours` en `archive/tests`); la rama queda en ~600 líneas contra `main`.
- **L8** — F5: «No puede haber una clave con otro combo. El que va a crear esa key (el cliente = kiosk) es una app nuestra tambien. Asi que la idea seria que se cree una vez confirmado el epdido. Si el usuario arranca y cancela no deberia crearla.» Decisión «Sí, así». Detalle en `odd/tasks/client-key-conflicto.md`.
- **L9** — «Pushiemos lo actual antes de seguir.» y «En main todo lo que vaya a estar en la app principal (si tener que mergear ramas mergea) y obvio la de tests separados los tests.»
