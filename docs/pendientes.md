# Pendientes

Estado al 2026-11-18; se actualiza al cerrar cada paso.

Para la visión de arquitectura y detalle técnico del sistema, consulta [arquitectura.md](arquitectura.md).

---

## A. Funcionalidades pendientes

| # | Qué falta | Qué hay hoy | Estado |
| --- | --- | --- | --- |
| 1 | **Cobro real con QR de Mercado Pago** | Implementado el 2026-11-18 ([diseno-mercadopago.md](diseno-mercadopago.md)): QR en `POST /orders`, webhook firmado, barrido de pagos. Probado sólo contra el Mercado Pago falso | Falta validarlo contra el sandbox real (bloqueo externo: credenciales) |
| 2 | **Avisos reales a soporte** cuando una orden queda en `failed` o `in_doubt`, o un pago queda con `payment_mismatch` / `payment_unknown` | Implementado el 2026-11-18 por Telegram (`internal/notify/telegram.go`): siempre deja el WARN en el log y después manda el mensaje al chat de soporte; obligatorio en producción. Probado sólo contra un servidor de prueba | Falta crear el bot y el grupo de soporte y probarlo contra Telegram real |
| 3 | **Prueba de punta a punta automatizada** | Probado a mano contra el servidor local `cmd/fakeapi` | Técnico |

### Fuera del MVP

- **Devoluciones**: no entran en el primer MVP. `MockRefunder` (`internal/payments/refund.go:25-46`) queda sin conectar a propósito: ningún paquete importa `internal/payments`.
- **App de la tablet**: ya existe en el repo `../compras-remotas-app` y no se sigue desde este documento.

---

## B. Deudas técnicas

| # | Deuda | Impacto | Tamaño |
| --- | --- | --- | --- |
| 1 | **El reclamo es un plazo, no un lease** | Si un despacho tarda más de 2 minutos, el barrido lo reconcilia mientras sigue en vuelo; sin latido no se distingue «trabado» de «lento» | Grande |
| 2 | **Tests fuera de `main`** (viven en la rama `archive/tests`) | Un CI sobre `main` no encuentra tests (`[no test files]`); ninguna regresión se detectaría automáticamente. Decisión deliberada del usuario, revisar antes de pasar a producción | Media |
| 3 | **Callback de snacks sin firma** | Sólo lo protege el secreto configurado en el path de la URL; quien conozca la URL puede cerrar líneas como entregadas. Mitigación: lista de IPs permitidas del proveedor cuando se disponga de la infraestructura | Media |
| 4 | **Migraciones sin `down`** | Sólo avanzan hacia adelante; volver atrás requiere crear una migración nueva (la verificación de integridad por SHA-256 en `schema_migrations` ya existe) | Media |
| 5 | **`GET /orders/{id}` no devuelve el `Ref` de cada línea** | Ante un reclamo de cliente, soporte no ve en la respuesta el código de canje (batidos) ni el `subOrderId` (snacks) | Chica |
| 6 | **Riesgo L8: líneas `pending` agotadas** | Con `maxAttempts` reducido por configuración, líneas `pending` agotadas vuelven en cada barrido ocupando cupo; ningún camino normal de uso lo provoca | Chica |
| 7 | **`fakeapi` no puede volver una máquina de batidos a `online`** | Devuelve 501 Not Implemented; sólo afecta a pruebas locales con la superficie de administración del falso | Chica |
| 8 | **Revisar la terminología de todo el proyecto** (archivos, tipos, funciones, opciones) | Hay nombres que no dicen qué hacen. Ejemplos: `Place` (crea la orden y chequea la disponibilidad), `WithPayments` (en `order` prende el cobro con QR; en `sweep` sólo suma el barrido de pagos; y sin él igual se cobra, con el pago simulado), `Confirmer` / `ConfirmFromCallback` / `ConfirmFromGateway` / `ConfirmPayment` (cuatro «confirmar» distintos). Cuesta leer el código sin el glosario de la cabeza | Media |
| 9 | **Orden sin QR que queda en `pending_payment` para siempre** | Si `RequestQR` falla (502) y la tablet nunca reintenta, la orden no tiene cobro y ningún barrido la cancela. No despacha nada, pero ensucia los listados | Chica |
| 10 | **Un aviso que Telegram rechaza 3 veces se pierde** (decisión del 2026-11-18) | Queda sólo como ERROR en el log y se registra como enviado en `notification_sent`: no hay reintento posterior. La alternativa robusta es una tabla de avisos pendientes que reintente el barrido | Media |

---

## C. Bloqueos externos

- **Mercado Pago**: crear la aplicación propia y obtener el access token (prueba y producción), el secreto del webhook y el user id; dar de alta una sucursal y una caja por mueble (`mp_pos_id`); configurar el webhook de la aplicación (tópico «Order») apuntando a `PUBLIC_BASE_URL/callbacks/mercadopago/{MP_CALLBACK_SECRET}`.
- **Hosting y `PUBLIC_BASE_URL`**: El destino es Render (donde ya corren `backend-powermix` y `powermix-pvs`), pero el servicio todavía no existe. Sin una URL pública con HTTPS, ni el proveedor de batidos (`cmd/api/main.go:85-88`) ni Mercado Pago pueden enviar sus webhooks.
- **Credenciales y sandbox de los dos proveedores**: Actualmente no se dispone de credenciales reales ni acceso a entornos de prueba oficiales de Batidos ni Snacks. Al pedirlas a Batidos, confirmar si la Merchant Open API usa el mismo par `key`/`secret` que la integración de pago de `vps-powermix`.
- **Dudas de la documentación de proveedores** (detalle en `odd/tasks/combos-remotos.md`, sección «Huecos abiertos»):
  - *Batidos*: dominio real de la API en producción y obligatoriedad de `deviceNo`. El formato de `key-md5` quedó resuelto (L46): MD5 hex en minúsculas, igual que en `vps-powermix`.
  - *Snacks*: estructura JSON real de la respuesta de `shipment`, si `PaymentOrderId` actúa efectivamente como clave de idempotencia, y si el campo `retailPrice` descuenta saldo de la cuenta comercial.

---

## D. No verificado

- **Proveedores reales**: Ninguna llamada se ha ejecutado contra los servidores de producción de los proveedores (todo el desarrollo se validó contra los servidores falsos de `cmd/fakeapi`).
- **Mercado Pago real**: el mapeo de `status` / `status_detail` a nuestros estados y la forma exacta del webhook están tomados de la documentación, no de un sandbox.
- **Webhook perdido en el circuito completo**: el Mercado Pago falso siempre envía el aviso, así que «se pierde el webhook y lo recupera el barrido» sólo está cubierto por tests (`ReconcilePayments` y el `Runner`), no por una prueba de punta a punta.
- **`go test -race`**: No corre en entornos Windows sin compilador de C configurado para cgo. La mayor parte de la concurrencia está cubierta de forma determinista mediante inyección de estados desactualizados, con la excepción del chequeo previo paralelo (`preflight/checker_parallel_test.go`).
- **Flujo completo automatizado**: El circuito completo `POST /orders` → `pay` → callbacks → `delivered` se ha probado únicamente de forma manual contra las APIs simuladas.
- **Tests en `main`**: La rama `main` no contiene archivos `*_test.go`. Los 55 archivos (474 tests, contados con `go test -list` el 2026-11-18) residen en `archive/tests`. Para traer `main` a esa rama se mergea y se restauran los `*_test.go` antes de commitear: `main` los borra y un merge directo los eliminaría.
- **Estado de Postgres y migraciones (lo que SÍ está verificado)**:
  - El store de Postgres (`internal/store/postgres`) cuenta con 27 tests que pasan exitosamente contra una instancia real de PostgreSQL cuando se define `TEST_DATABASE_URL`.
  - El ejecutor de migraciones (`internal/migrate`) ha sido probado contra Postgres real aplicando las 6 versiones sobre un esquema vacío y validando el checksum SHA-256 en `schema_migrations`.

---

## Orden recomendado

1. **Alertas a soporte**: crear el bot de Telegram y el grupo de soporte, y probar un aviso real.
2. **Despliegue en Render**: crear el servicio con Postgres y fijar `PUBLIC_BASE_URL`; con eso, validar Mercado Pago contra su sandbox.
3. **Revisión de terminología** (deuda B8), antes de que el código crezca más.
4. **Antes de producción**: Integrar los tests en `main` y configurar el pipeline de integración continua (CI).
5. **Deudas técnicas menores**: Incluir `Ref` en la respuesta de `GET /orders/{id}`.
6. **Implementación de lease**: Sustituir el plazo fijo de 2 minutos por un mecanismo de lease/latido cuando se opere con proveedores reales y se conozcan sus latencias efectivas.

---

## Documentos de proveedores

Los documentos originales de los proveedores contienen avisos de copyright de terceros y residen **únicamente en local** bajo el directorio `docs/proveedores/`, excluidos de Git mediante `.gitignore`:

1. `docs/proveedores/batidos-api.md`: Especificación de la Merchant Open API v2.0 de Batidos Powermix (GS).
2. `docs/proveedores/snacks-api.pdf`: "YS Vending Management Platform API Document V1.0.1" de Snacks Powermix (OurVend).
3. `docs/proveedores/snacks-api.txt`: Extracción en texto plano del documento de Snacks para búsquedas y referencias de líneas en código y comentarios (`docs/proveedores/snacks-api.txt:línea`).

### Cómo obtenerlos en otra PC

Al clonar el repositorio en una máquina nueva, estos archivos no estarán presentes. Para trabajar con ellos:
- Debes copiarlos manualmente desde una estación de trabajo autorizada o desde el almacenamiento seguro del equipo al directorio `docs/proveedores/`.
- No intentes forzar su inclusión en Git (`git add -f`), respetando las restricciones de propiedad intelectual de los proveedores.
