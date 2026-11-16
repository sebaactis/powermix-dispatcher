# Pendientes

Estado al 2026-11-15; se actualiza al cerrar cada paso.

Para la visión de arquitectura y detalle técnico del sistema, consulta [arquitectura.md](arquitectura.md).

---

## A. Funcionalidades pendientes

| # | Qué falta | Qué hay hoy | Quién decide |
| --- | --- | --- | --- |
| 1 | **Cobro real**: generar el QR (Mercado Pago, MODO u otro), recibir el webhook de cobro y validarlo | `POST /orders/{id}/pay` simulado; responde 403 fuera del entorno `sandbox` (`internal/httpapi/orders.go:54-58`) | Dueño del producto (elegir pasarela de pago) |
| 2 | **Devoluciones**: política (qué se devuelve si falla una de las dos máquinas, plazos, quién absorbe la pérdida) y su ejecución | `MockRefunder` sólo escribe un log (`internal/payments/refund.go:25-46`) y nadie lo llama: ningún paquete importa `internal/payments` | Política: dueño del producto; conectarlo al motor de órdenes: técnico |
| 3 | **Avisos reales a soporte** cuando una orden queda en `failed` o `in_doubt` | `LogNotifier` sólo escribe un WARN en el log (`internal/notify/notify.go:26-42`); la deduplicación con la tabla `notification_sent` ya funciona | Canal (Telegram, Slack, email): dueño del producto; adaptador: técnico |
| 4 | **App de la tablet** (Android, modo kiosco): combos, QR, progreso del despacho, resultado | No existe en este repositorio; proyecto planeado en un repo independiente `../compras-remotas-app` | Técnico |
| 5 | **Prueba de punta a punta automatizada** | Probado a mano contra el servidor local `cmd/fakeapi` | Técnico |

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

---

## C. Bloqueos externos

- **Hosting y `PUBLIC_BASE_URL`**: Sin una URL pública con HTTPS, el proveedor de batidos nunca puede enviar su webhook de callback (`cmd/api/main.go:85-88`).
- **Credenciales y sandbox de los dos proveedores**: Actualmente no se dispone de credenciales reales ni acceso a entornos de prueba oficiales de Batidos ni Snacks.
- **Dudas de la documentación de proveedores** (detalle en `odd/tasks/combos-remotos.md:140-157`):
  - *Batidos*: dominio real de la API en producción, uso de mayúsculas en `key-md5`, obligatoriedad de `deviceNo`.
  - *Snacks*: estructura JSON real de la respuesta de `shipment`, si `PaymentOrderId` actúa efectivamente como clave de idempotencia, y si el campo `retailPrice` descuenta saldo de la cuenta comercial.

---

## D. No verificado

- **Proveedores reales**: Ninguna llamada se ha ejecutado contra los servidores de producción de los proveedores (todo el desarrollo se validó contra los servidores falsos de `cmd/fakeapi`).
- **`go test -race`**: No corre en entornos Windows sin compilador de C configurado para cgo. La mayor parte de la concurrencia está cubierta de forma determinista mediante inyección de estados desactualizados, con la excepción del chequeo previo paralelo (`preflight/checker_parallel_test.go`).
- **Flujo completo automatizado**: El circuito completo `POST /orders` → `pay` → callbacks → `delivered` se ha probado únicamente de forma manual contra las APIs simuladas.
- **Tests en `main`**: La rama `main` no contiene archivos `*_test.go`. Los 42 archivos (341 tests) residen en `archive/tests`.
- **Estado de Postgres y migraciones (lo que SÍ está verificado)**:
  - El store de Postgres (`internal/store/postgres`) cuenta con 27 tests que pasan exitosamente contra una instancia real de PostgreSQL cuando se define `TEST_DATABASE_URL`.
  - El ejecutor de migraciones (`internal/migrate`) ha sido probado contra Postgres real aplicando las 6 versiones sobre un esquema vacío y validando el checksum SHA-256 en `schema_migrations`.

---

## Orden recomendado

1. **Decisiones del dueño del producto**: Pasarela de pago definitiva, política de devoluciones comerciales, canal oficial de alertas a soporte y contratación de hosting con dominio HTTPS.
2. **Antes de producción**: Integrar los tests en `main` y configurar el pipeline de integración continua (CI).
3. **Deudas técnicas menores**: Incluir `Ref` en la respuesta de `GET /orders/{id}`; conectar el adaptador de devoluciones (`Refunder`) una vez definida la política comercial.
4. **Implementación de lease**: Sustituir el plazo fijo de 2 minutos por un mecanismo de lease/latido cuando se opere con proveedores reales y se conozcan sus latencias efectivas.

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
