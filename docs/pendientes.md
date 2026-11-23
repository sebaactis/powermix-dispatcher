# Pendientes

Estado al 2026-11-23 (con **F1–F3**, **F5–F9**, **F7b**, **F11–F16**, **D3–D6** y **P3** ya publicados en `main`).

Para cómo funciona el sistema consulta [arquitectura.md](arquitectura.md); para el detalle completo, las referencias `archivo:línea` y el historial de la revisión para producción, consulta [odd/tasks/revision-produccion.md](../odd/tasks/revision-produccion.md).

---

## A. Revisión para producción abierta (`odd/tasks/revision-produccion.md`)

| ID | Resumen | Estado |
| --- | --- | --- |
| **F4** | `Reconcile` de snacks siempre devuelve `InDoubt` y consulta sólo la página 1 sin filtro | Bloqueado: esperando confirmación del proveedor |
| **F10** | `RetailPrice` en snacks se envía en `0` fijo | Bloqueado por duda al proveedor (sección C) |

### Fuera del MVP

- **Devoluciones**: `payments.Refunder` y `MockRefunder` (`internal/payments/refund.go:21-46`) siguen sin conectarse al motor a propósito.
- **App de la tablet**: se desarrolla en el repositorio aparte `../compras-remotas-app`.

---

## B. Otras deudas técnicas

| Deuda | Impacto | Tamaño |
| --- | --- | --- |
| **El reclamo es un plazo fijo, no un lease** | Si un despacho tarda más de 2 min, el barrido lo reconcilia mientras sigue en vuelo | Grande |
| **Tests fuera de `main` (`archive/tests`)** | `main` no tiene archivos `*_test.go`; un CI directo sobre `main` no corre la suite | Media |
| **Callback de snacks sin firma** | Sólo lo protege el secreto en la ruta; mitigable con lista de IPs permitidas en infraestructura | Media |
| **Migraciones sin `down`** | Sólo avanzan hacia adelante; revertir requiere una migración nueva | Media |
| **`GET /orders/{id}` no devuelve `Ref` de cada línea** | Soporte no ve en la respuesta el código de canje (batidos) ni el `SubOrderNo` (snacks) | Chica |
| **Líneas `pending` agotadas con `WithMaxAttempts`** | Si se configura un límite menor en código, pueden ocupar cupo en el barrido (riesgo L8) | Chica |
| **`fakeapi` no vuelve a poner batidos `online`** | Los endpoints de administración de batidos responden 501 si se pasa `false` | Chica |
| **Orden sin QR queda `pending_payment`** | Si `RequestQR` falla (502) y la tablet no reintenta, no tiene cobro que el barrido cancele | Chica |
| **Cabecera de orden desfasada de sus líneas** | Si `refreshOrderStatus` agota sus 3 intentos y el ganador derivó sin ver la última línea, la orden puede quedar en `dispatching` con todas las líneas cerradas; el barrido no la toma y no sale el aviso. Arreglo: que el barrido repare cabeceras que no coinciden con sus líneas | Media |
| **Aviso perdido tras 3 fallos de Telegram** | Queda en `ERROR` en el log y se registra en `notification_sent` sin reintento posterior | Media |

---

## C. Bloqueos externos

- **Mercado Pago**: crear la aplicación propia, obtener access token (prueba y producción), webhook secret y user id; dar de alta sucursal y caja por mueble (`mp_pos_id`); configurar el webhook (tópico «Order») hacia `PUBLIC_BASE_URL/callbacks/mercadopago/{MP_CALLBACK_SECRET}`.
- **Hosting y `PUBLIC_BASE_URL`**: falta crear el servicio en Render con PostgreSQL; sin URL HTTPS pública no llegan webhooks de Mercado Pago ni callbacks de batidos.
- **Credenciales y sandbox de Batidos y Snacks**: sin acceso oficial todavía (confirmar con Batidos si la Merchant Open API comparte `key`/`secret` con `vps-powermix`).
- **Dudas de contratos de proveedores** (ver `odd/tasks/combos-remotos.md`, «Huecos abiertos»):
  - *Batidos*: dominio real en producción y obligatoriedad de `deviceNo`.
  - *Snacks*: forma real de la respuesta de `shipment`, idempotencia real de `PaymentOrderId`, impacto de `retailPrice` (**F10**) y si la API informa resultado del despacho (**F4**).

---

## D. Qué falta verificar (y qué sí está verificado)

- **No verificado en entornos reales**: llamadas a servidores reales de Batidos y Snacks; mapeo de estados/webhooks contra sandbox real de Mercado Pago; recuperación de webhook perdido en circuito E2E completo; prueba E2E automatizada (hoy sólo manual contra `cmd/fakeapi`).
- **`go test ./... -race`**: no corre en Windows sin compilador C para cgo.
- **Tests de Postgres en F1–F16**: en los fixes recientes se saltaron por no estar definida `TEST_DATABASE_URL`.
- **Sí verificado anteriormente**: `internal/store/postgres` (suite de tests contra Postgres real con `TEST_DATABASE_URL`) e `internal/migrate` (aplicación de esquema y verificación de checksums SHA-256 en `schema_migrations`).

---

## Orden recomendado

1. Desbloquear **F4** y **F10** con el proveedor de snacks.
2. Desplegar en Render con PostgreSQL, fijar `PUBLIC_BASE_URL` y validar Mercado Pago contra su sandbox real.
3. Integrar la suite de `archive/tests` en `main` con CI antes de salir a producción, y evaluar las deudas restantes (`Ref` en `GET /orders/{id}` y mecanismo de lease).

---

## Documentos de proveedores

Los contratos originales contienen avisos de copyright y viven **sólo en local** bajo `docs/proveedores/` (en `.gitignore`): `batidos-api.md`, `snacks-api.pdf` y `snacks-api.txt`. En una máquina nueva debes copiarlos manualmente desde una estación autorizada sin forzarlos en Git.
