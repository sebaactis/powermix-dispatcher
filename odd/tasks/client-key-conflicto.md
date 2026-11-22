# F5 — `client_key` con otro combo o mueble, y largo máximo

Fix F5 de `odd/tasks/revision-produccion.md`. La clave la genera nuestra app del kiosco una vez por pedido confirmado: si llega repetida con otro combo o mueble, es un bug del kiosco y el servidor debe rechazarla de forma visible (fail fast) en vez de devolver el pedido viejo en silencio.

## Specs

- **S1** — Misma `client_key`, mismo `combo_id` y mismo `kiosk_id` que un pedido existente: se devuelve el pedido existente, como hoy (reintento idempotente). Propuesta aceptada: «Misma clave + mismo combo + mismo mueble → devuelve el pedido existente (reintento normal, sin cambios).»
- **S2** — Misma `client_key` con otro combo u otro mueble: «Misma clave con otro combo u otro mueble → **409 Conflict** con mensaje claro, y un `warn` en el log.» No se crea ningún pedido nuevo y el pedido existente no cambia. Aplica también a la carrera en `CreateOrder` (`ErrAlreadyExists` → relectura por clave, `internal/order/place.go:60-66`).
- **S3** — «`client_key` de más de 128 caracteres → 400.» Se mide después de `strings.TrimSpace`. No se crea ningún pedido.
- **S4** — «Documentar el contrato del kiosco en `README.md` y `docs/arquitectura.md`: "un UUID por pedido confirmado, reutilizado sólo para reintentar ese mismo pedido".» Incluye los nuevos 409 y 400.

## Tasks

- [ ] T1 — S1-S4 — writer `gentle-ai-worker` — commit: pendiente.

## Log

- **L1** — Pregunta del usuario: «F5, pero porque un client_key podria ser reutilizado?»
- **L2** — Contexto del usuario: «No puede haber una clave con otro combo. El que va a crear esa key (el cliente = kiosk) es una app nuestra tambien. Asi que la idea seria que se cree una vez confirmado el epdido. Si el usuario arranca y cancela no deberia crearla.»
- **L3** — Decisión: «Sí, así» sobre la propuesta «409 si cambia combo o mueble + 400 si la clave supera 128 caracteres + contrato del kiosco documentado». Sin migración (se descartó el índice único por `(kiosk_id, client_key)`).
- **L4** — Los 3 tests de F3 se movieron a `archive/tests` (commit de retiro en la rama + merge `-s ours` en `archive/tests`); la suite de `archive/tests` pasa.
