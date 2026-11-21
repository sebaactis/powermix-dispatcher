# Avisos de entrega parcial

Soporte se entera de todo pedido cobrado que no se entregó completo, con qué se entregó, qué falló y cuánto vale lo que falló.
Rama: `fix/partial-order-alerts`. Tests: flujo `archive/tests` (`docs/arquitectura.md`, «Tests: dónde viven y cómo se usan»).

## Specs

- **S1** — Precio por ítem. Decisión del usuario: «Precio por ítem en combos.json (recomendado)»: «Cada ítem lleva price_cents y al cargar se valida que la suma dé el precio del combo. El aviso muestra el monto exacto que falló. No cambia lo que se le manda a Snacks; eso queda como fix aparte».
- **S2** — Cada línea del pedido lleva el monto de su ítem (`store.Line.AmountCents`), copiado del catálogo al armar el pedido, y se persiste igual en el store en memoria y en Postgres.
- **S3** — Aviso de entrega parcial. Pregunta: «Cuando un combo pagado se entrega a medias (partial), ¿soporte tiene que enterarse?» Respuesta: «Sí, avisar por Telegram».
- **S4** — Contenido del aviso. Pregunta: «Si se avisa, ¿qué tiene que decir el mensaje sobre qué hacer?» Respuesta: «Qué se entregó y qué falló», con la descripción «Ej.: 'se entregó el batido, falló el snack: compensar al cliente por el snack ($X)'. Con el monto de la línea fallida».
- **S5** — Un pedido que termina `failed` en el despacho inicial (`internal/order/payment.go`, `dispatchLines`) avisa igual que cuando llega a `failed` por callback o barrido (bug: hoy no avisa).
- **S6** — Fuera de alcance: lo que se envía a Snacks como `RetailPrice` no cambia en esta feature.

## Tasks

- [x] T1 — S1, S2, S6 — catálogo con `price_cents` por ítem, validación de suma, monto en cada línea — worker — commit: 8931cb3
- [x] T2 — S3, S4, S5 — aviso `partial` con detalle y monto; `dispatchLines` usa `settleOrder` — worker — commit: ab28962

## Log

- **L1** — Pedido original del usuario: «Podemos revisar que tenemos pendiente para que mi sistema sea 100% funcional? La entrada del pedido, las validaciones, las comunicaciones con los proveedores de maquina, con el proveedor de pago, y el despacho. Además, logueos. Revisemos que mi doc esté actualizada, clara, y consisa. Que mi codigo sea usando buenas practicas Y que mi grafico visual tambien esté actualizado y sea claro y moderno. Vamos por partes y me vas contando.»
- **L2** — Sobre los hallazgos: «Planifiquemos cada fix pero abordemoslos juntos para ver si corresponde o no el fix segun el negocio. Vamos 1 por 1.»
- **L3** — Evidencia: `notifyOrder` (`internal/order/settle.go:52-71`) sólo avisa `in_doubt` y `failed`; `dispatchLines` (`internal/order/payment.go:76`) llama a `refreshOrderStatus` sin aviso; `catalog.Item` no tiene precio y `Line.AmountCents` queda en 0 (`internal/httpapi/draft.go:88-99`).
- **L4** — Línea de base: con los tests de `archive/tests` restaurados, `go test ./...` pasa en todos los paquetes.
- **L5** — T1: RED `TestLoadFileRejectsInvalidItemPrices` y `TestCreateOrderBuildsTheDraftFromTheComboAndTheKiosk` (AmountCents=0); GREEN `go test -count=1 ./...` exit 0. No hizo falta migración: `order_line.amount_cents` ya existía (`migrations/0001_init.sql:80`). S6: Snacks envía `RetailPrice: 0` explícito (test con AmountCents=1550). Tests de Postgres compilan pero se saltan sin `TEST_DATABASE_URL`. `combos.local.json` necesita `price_cents` por ítem (lo edita el usuario).
- **L6** — T2: RED en `TestEngineMarkPaidNotifiesWhenInitialDispatchFailsAndDeduplicates`, `TestEngineNotifiesPartialDeliveryWithLineDetailsAndFailedAmountOnce` y `TestTelegramRendersPartialOrderAlert`; GREEN `go test -count=1 ./...` exit 0. Nuevo tipo `order_partial`.
- **L7** — Tests de T1 y T2 archivados en `archive/tests` (commit 7f81151, merge de la rama con los tests restaurados; suite completa en verde). Sin push.
