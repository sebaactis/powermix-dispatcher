# Revisión para producción

Plan de la revisión «sistema 100% funcional»: hallazgos priorizados, cuáles se corrigieron y desde dónde retomar.
Modo de trabajo acordado: un fix por vez, primero se discute si corresponde según el negocio y después se implementa.

## Cómo retomar

1. Leer este documento y `docs/pendientes.md`.
2. **Próximo: F4**, bloqueado hasta confirmar con el proveedor de snacks si su API informa el resultado del despacho (pregunta pendiente para el usuario). Trabajarlo en una rama nueva desde `main` una vez resuelta `fix/partial-order-alerts`. Mientras tanto se puede avanzar con **F5** (`client_key` reutilizado con otro combo / sin largo máximo) o **F6** (códigos HTTP); el usuario elige cuál.
3. **Decisión de entrega pendiente:** la rama `fix/partial-order-alerts` tiene 8 commits y ~1526 líneas desde `main` (mayoría tests); falta decidir si se publica como un PR o se parte en PRs encadenados. Nada fue pusheado (push y merge de `fix/partial-order-alerts` y de `archive/tests` los decide el usuario).
4. **Correlación de avisos:** se usa `order_id`; el `request_id` por pedido HTTP queda para **F9**.
5. Traer los tests: `git checkout archive/tests -- $(git ls-tree -r --name-only archive/tests | grep '_test.go$')`. Al terminar, archivarlos en `archive/tests` (merge de la rama con los tests restaurados) y sacarlos del árbol con `git rm -q -f`.

## Partes de la revisión

- [x] P1 — Entrada del pedido, validaciones, proveedores, pago, despacho y logueo: revisado el 2026-11-21 (hallazgos abajo).
- [ ] P2 — Documentación (README, `docs/arquitectura.md`, `docs/pendientes.md`, `docs/comentarios-codigo.md` de 2620 líneas): que esté actualizada, clara y concisa. Sin empezar.
- [ ] P3 — Buenas prácticas del código (detalle en H-BP). Sin empezar más allá del adelanto.
- [ ] P4 — Gráfico `docs/flujo-del-sistema.svg`: actualizado (Mercado Pago, Telegram, partial, in_doubt), claro y moderno. Sin empezar.

## Fixes

### Críticos
- [x] F1 — Avisos de pedidos cobrados sin entrega completa: `partial` no avisaba, y un `failed` en el despacho inicial tampoco. Hecho: `odd/tasks/avisos-entrega-parcial.md`, commits 8931cb3, ab28962, 509e7fc.
- [x] F2 — Errores 5xx/429/ilegibles del proveedor reintentados como rechazo (riesgo de doble despacho). Hecho: `odd/tasks/despacho-errores-ambiguos.md`, commits 9996d4e, 3ca699c; verificación independiente aprobada.
- [x] F3 — Sin `recover()` en goroutines de fondo: un panic tira el servidor (`internal/order/engine.go:171-180` `spawn`, `internal/order/payment.go:65-70` `dispatchLines`, `internal/preflight/checker.go:104-111`). Decisión: «in_doubt + aviso a soporte (recomendado)». Hecho: `odd/tasks/recuperar-panics.md`, commit 1915a23; verificación independiente aprobada.
- [ ] F4 — **Próximo.** `Reconcile` de snacks siempre devuelve `InDoubt` y busca sólo en la página 1 sin filtro (`internal/dispenser/snacks/dispenser.go:106-139`). Puede ser deliberado (la API no informa el resultado del despacho). Confirmar con el proveedor antes de decidir.

### Importantes
- [ ] F5 — `client_key` reutilizado con otro combo devuelve el pedido viejo sin avisar, y no tiene largo máximo (`internal/order/place.go:17-25`, `internal/httpapi/draft.go:20,70`).
- [ ] F6 — Códigos HTTP: caída del proveedor en el chequeo previo devuelve 500 en vez de 502/503 (`internal/preflight/checker.go:114-116`, `internal/gate/gate.go:76-81`); cuerpo grande devuelve 400 en vez de 413 (`draft.go:29-31`); `combo_id` no se recorta antes de buscarlo (`draft.go:37-41`).
- [ ] F7 — Apagado: el plazo de 10 s (`cmd/api/main.go:245`) puede vencer con despachos de hasta 2 min en curso y `closeStore()` cierra Postgres debajo de ellos.
- [ ] F8 — El callback de batidos usa una versión vieja de la línea después de `markBrewing` y la extensión del plazo se pierde sin error (`internal/order/callback.go:65-78`, `internal/order/reconcile.go:223-226`).
- [ ] F9 — Logueo: falta middleware de access log y de recover HTTP, `request_id`/`order_id` en el contexto, nivel configurable (`cmd/api/main.go:48` fijo en Info) y `http.Server.ErrorLog` estructurado (`cmd/api/server.go:16-25`).
- [ ] F10 — Qué precio se manda a Snacks como `RetailPrice` (hoy 0 fijo, `internal/dispenser/snacks/dispenser.go`), pendiente de la duda del proveedor en `docs/pendientes.md` sección C.

### Menores
- [ ] F11 — `order.ClosedAt` nunca se carga (`internal/store/store.go:36`).
- [ ] F12 — `refreshOrderStatus` reintenta `ErrStale` una sola vez (`internal/order/settle.go:46`).
- [ ] F13 — Webhook de Mercado Pago: `data.id` sólo desde la query (`internal/httpapi/mercadopago.go:85`); `ts` asumido en milisegundos (`internal/payments/mercadopago/signature.go:30,43`).
- [ ] F14 — Respuestas de más de 1 MB se truncan sin error en batidos y snacks (`batidos/client.go:321`, `snacks/client.go:268,428`).
- [ ] F15 — El aviso de entrega parcial muestra el SKU (`DEMO-SNACK-ALFAJOR`) y no el nombre del producto: haría falta el nombre en el catálogo.
- [ ] F16 — `POST /orders` responde 200 en vez de 201 (`internal/httpapi/orders.go:53`); el barrido no tiene plazo por vuelta (`internal/sweep/runner.go:92-104`).
- [ ] D3 — Los manejadores de recuperación no se protegen a sí mismos: un segundo panic dentro de `markPanickedLineInDoubt` o `notifyOnce` (`internal/order/payment.go:73-78,98,133`) todavía tira el proceso.
- [ ] D4 — Un panic dentro de `spawn` (`internal/order/engine.go:192-211`) pero fuera de una línea (por ejemplo en `settleOrder`, `internal/order/payment.go:88`, `internal/order/settle.go:16`) sólo se loguea: no avisa ni recalcula el estado del pedido; falta confirmar si el barrido lo corrige.
- [ ] D5 — El intento que entró en panic queda `OutcomeAmbiguous` sin `FinishedAt` (`internal/order/dispatch.go:49-56,94`) y no incrementa `line.Attempts` (`internal/order/payment.go:133-167`), distinto de las líneas de F2.

### H-BP — Buenas prácticas (insumo de P3)
- Funciones largas: `main` de `cmd/api/main.go` (232 líneas), `(*Engine).dispatchLine` en `internal/order/dispatch.go` (~100 líneas).
- Código muerto: `Dispenser.Check` y sus dos implementaciones (`internal/dispenser/dispenser.go:55`, `batidos/dispenser.go:71-93`, `snacks/dispenser.go:52-85`), reemplazado por `preflight.Checker`.
- Repetición en 4 lugares del armado request HTTP + `LimitReader` + JSON (`batidos/client.go:292`, `snacks/client.go:243`, `mercadopago/client.go:146`, `notify/telegram.go:221`): candidata a abstracción.
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
