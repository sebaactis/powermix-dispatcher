# F6 — Códigos HTTP del pedido

Fix F6 de `odd/tasks/revision-produccion.md`, rama `fix/http-status-codes` desde `main`.
Cuando un proveedor de máquinas no responde, el kiosco tiene que poder distinguirlo de un bug nuestro.

## Specs

- **S1** — Si un proveedor de máquinas (batidos o snacks) no responde o devuelve error en el chequeo previo (`internal/preflight/checker.go`, `internal/gate/gate.go`), `POST /orders` responde **502**, «igual que el QR» (`order.ErrQRUnavailable` → 502 en `internal/httpapi/errors.go`), con un mensaje claro y un log de error. No se crea ningún pedido. Opción elegida: «502, igual que el QR (recomendado)».
- **S2** — Siguen respondiendo **500**: un panic dentro del chequeo previo (`runCheck`), la falta de verificador configurado (`errNoChecker`) y un combo que no se puede evaluar (`gate.request`). Son bugs o configuración nuestra, no del proveedor.
- **S3** — `combo_id` se recorta (`strings.TrimSpace`) antes de buscarlo en el catálogo: `" combo-1 "` encuentra `combo-1`, igual que `kiosk_id` y `client_key`.
- **S4** — Revisar sin cambiar código: ¿el webhook de Mercado Pago (`internal/httpapi/mercadopago.go`) lee el cuerpo sin límite? Informar el hallazgo con ruta y línea.

Fuera de alcance: 413 para cuerpos de más de 64 KB. «Por ahora esto no lo toques, porque desconozco bien aun el body que va a venir con exactitud.»

## Tasks

- [x] T1 — S1-S4 — writer `gentle-ai-worker` — commit: 980b67b (código y docs); tests en `archive/tests` 1608e9f.

## Log

- **L1** — Pedido: «Si.» a seguir con F6 (código HTTP), 2026-11-22.
- **L2** — Decisión del proveedor caído: «502, igual que el QR (recomendado)».
- **L3** — 413: «Por ahora esto no lo toques, porque desconozco bien aun el body que va a venir con exactitud.»
- **L4** — Evidencia T1: RED, sentinels sin definir; 500 en vez de 502 en los 3 subcasos; 404 con `combo_id` con espacios. GREEN en `preflight`, `gate` y `httpapi`; `go test ./...` en `archive/tests` con exit 0. Decisiones: `order.ErrAvailabilityUnavailable` (junto a `ErrNotSellable`, así `httpapi` no importa `preflight`); `preflight.ErrPanic` marca el panic para que siga en 500, aunque se mezcle con un error del otro proveedor. S4: el webhook de Mercado Pago no lee el cuerpo (`internal/httpapi/mercadopago.go:77-140` usa sólo ruta, query y headers), así que no tiene riesgo de lectura sin límite. Deuda menor: el test `TestCreateOrderReturnsInternalErrorWhenTheProviderFailsDuringTheCheck` (`internal/httpapi/gate_test.go`, en `archive/tests`) ahora usa un nombre engañoso: prueba que un error genérico cae en 500.
