# compras-remotas

Backend en Go para un mueble expendedor con una tablet y dos máquinas independientes (batidos y snacks). Cada compra crea un pedido, valida la disponibilidad de ambas máquinas, gestiona el cobro y despacha cada ítem a su máquina por separado.

```text
tablet (app Android) ──► compras-remotas ──► API Batidos ──► máquina de batidos
                                         ──► API Snacks  ──► máquina de snacks
```

La tablet nunca se comunica directo con las máquinas ni con los proveedores: las credenciales viven sólo en este backend.

## Documentación

- [docs/flujo-del-sistema.svg](docs/flujo-del-sistema.svg): diagrama visual completo de la arquitectura, rutas HTTP, cobro, despacho y reconciliación.
- [docs/arquitectura.md](docs/arquitectura.md): cómo funciona por dentro (paquetes, dependencias, invariantes y flujo paso a paso con referencias `archivo:línea`).
- [docs/diseno-mercadopago.md](docs/diseno-mercadopago.md): diseño e implementación del cobro con QR de Mercado Pago.
- [docs/pendientes.md](docs/pendientes.md): qué queda abierto, bloqueos externos, qué falta verificar y puntero a `odd/tasks/revision-produccion.md`.
- [docs/comentarios-codigo.md](docs/comentarios-codigo.md): explicación detallada símbolo por símbolo (el código fuente no lleva comentarios a propósito).
- [odd/tasks/combos-remotos.md](odd/tasks/combos-remotos.md) y [odd/tasks/revision-produccion.md](odd/tasks/revision-produccion.md): especificación histórica, decisiones y plan de revisión para producción.

## Requisitos y estructura rápida

- Go `1.23` (`go.mod`).
- PostgreSQL 16 (opcional en desarrollo local con `STORE=memory`; obligatorio con `STORE=postgres`).
- Docker y Docker Compose (para levantar el stack local completo).

Binarios principales:

- `cmd/api`: servidor HTTP de la tablet, callbacks de proveedores y barrido periódico de reconciliación.
- `cmd/migrate`: ejecutor puntual de migraciones SQL embebidas (`migrations/*.sql`).
- `cmd/fakeapi`: servidores simulados locales de Batidos, Snacks y Mercado Pago, más una API de administración sólo para pruebas.

## Ejecución local

### 1. Compilar y verificar el código

```bash
go test ./...   # en main no hay tests: los 64 archivos *_test.go viven en la rama archive/tests
go vet ./...
go build ./...
```

### 2. Preparar configuración y catálogo locales

```bash
cp config.example.json config.local.json   # topología de muebles/máquinas (sin secretos reales)
cp combos.example.json combos.local.json   # catálogo: cada ítem lleva price_cents > 0 y su suma debe igualar price_cents del combo
```

### 3. Migrar base de datos (sólo si usas `STORE=postgres`)

```bash
DATABASE_URL="postgres://compras:compras@127.0.0.1:5432/compras?sslmode=disable" go run ./cmd/migrate
```

### 4. Levantar los proveedores simulados y la API

En una terminal:

```bash
go run ./cmd/fakeapi
# Batidos falso     -> http://127.0.0.1:9401
# Snacks falso      -> http://127.0.0.1:9402
# Administración    -> http://127.0.0.1:9403 (sólo POST, sólo loopback)
# Mercado Pago falso -> http://127.0.0.1:9404 (Orders API: crear, consultar, cancelar)
```

Por defecto `cmd/fakeapi` siembra el producto `goods-1` (`-batidos-goods`) en el dispositivo `dev-e2e-1` (`-batidos-device`), y snacks arranca con el dispositivo `snacks-dev-1` y stock en `prod_snack_1` / `prod_snack_2`. Pasa `-batidos-goods ""` si prefieres empezar con el catálogo de batidos vacío y cargarlo por la API de administración.

En otra terminal:

```bash
go run ./cmd/api -config config.local.json -combos combos.local.json
```

### Probar el flujo completo con Mercado Pago local

Apunta el webhook de `fakeapi` al endpoint de la API y configura las credenciales del falso:

```bash
go run ./cmd/fakeapi -mp-webhook-url http://127.0.0.1:8080/callbacks/mercadopago/local-mp
```

En `config.local.json` (o mediante variables `MP_*`):

- `"mercadopago": {"base_url": "http://127.0.0.1:9404", "access_token": "TEST-fake-token", "webhook_secret": "fake-webhook-secret", "callback_secret": "local-mp"}`
- `"mp_pos_id": "POS-1"` en cada mueble.

Luego puedes crear una orden, simular el pago y consultar el estado:

```bash
curl -X POST http://127.0.0.1:8080/orders -d '{"combo_id":"...","client_key":"k1","kiosk_id":"kiosk-demo"}'
curl -X POST http://127.0.0.1:9403/admin/mercadopago/pay -d '{"order":"<order_id>"}'
curl http://127.0.0.1:8080/orders/<order_id>
```

> Solo para pruebas locales: la API de administración de `cmd/fakeapi` no tiene autenticación. `-admin-addr` usa `127.0.0.1:9403` por defecto y el binario rechaza arrancar si se configura fuera de loopback.

### Superficie de administración de `cmd/fakeapi`

Todas las rutas aceptan únicamente `POST` con JSON sin campos desconocidos y responden `{"ok":true}` o `{"ok":false,"error":"…"}` (salvo las de Mercado Pago, que también incluyen `order_id` y `webhook`). El estado vive sólo en memoria.

| Ruta | Cuerpo | Efecto |
| --- | --- | --- |
| `/admin/batidos/goods` | `{"device_id":"dev-e2e-1","goods":[{"id":"goods-1","listing":true,"sold_out":0}]}` | Reemplaza el catálogo del dispositivo (`[]` lo vacía) |
| `/admin/batidos/offline` | `{"device_id":"dev-e2e-1","offline":true}` | Marca el dispositivo offline (`false` responde 501) |
| `/admin/batidos/abnormal` | `{"device_id":"dev-e2e-1","abnormal":true}` | Marca avería (`false` responde 501) |
| `/admin/batidos/out_of_stock` | `{"device_id":"dev-e2e-1","out_of_stock":true}` | Marca sin insumos (`false` responde 501) |
| `/admin/batidos/advance` | `{"code":"000001","status":2}` | Avanza un código (`0` fallido, `1` en preparación, `2` entregado) |
| `/admin/snacks/device` | `{"online":true,"sell_status":1,"fault_state":0,"lock_type":0,"stockout_state":0}` | Actualiza sólo los campos presentes |
| `/admin/snacks/slot` | `{"slot_no":"1","stock":5,"slot_status":1}` | Ajusta stock y/o estado de una ranura |
| `/admin/snacks/order` | `{"status":1,"name":"…"}` o `{"empty":true}` | Cambia lo que informa la página de órdenes o la vacía |
| `/admin/mercadopago/pay` | `{"order":"<ORD… o order_id>","amount_cents":150050}` | Marca pagada y envía webhook firmado (`amount_cents` opcional; un valor distinto simula monto incorrecto) |
| `/admin/mercadopago/expire` | `{"order":"…"}` | Expira una orden `created` y envía webhook |
| `/admin/mercadopago/notify` | `{"order":"…"}` | Reenvía el webhook con el estado actual y un nuevo `x-request-id` |

## Endpoints HTTP

### API de la tablet

- `GET /healthz`: responde `200` con `{"status","env","kiosks","combos","store"}`.
- `GET /combos`: lista los combos activos del catálogo.
- `POST /orders`: cuerpo `{"combo_id":"...","client_key":"...","kiosk_id":"..."}` (`kiosk_id` es opcional si sólo hay un mueble configurado; `combo_id` y `client_key` se recortan con `TrimSpace`). Responde `201` con la orden.
  - `client_key`: un UUID por pedido confirmado en el kiosco, reutilizado únicamente para reintentar ese mismo pedido (`<= 128` caracteres tras `trim`, si supera responde `400`).
  - Mismo `client_key`, mismo `combo_id` y mismo `kiosk_id`: devuelve `201` con la orden existente (idempotencia: el reintento recibe la misma respuesta que el original).
  - Mismo `client_key` con otro combo o mueble, combo no vendible o mueble sin la máquina requerida: responde `409`.
  - JSON inválido, `combo_id` vacío o `kiosk_id` faltante con múltiples muebles: responde `400`.
  - Combo o mueble inexistente: responde `404`.
  - Falla o timeout de un proveedor en el chequeo previo o al crear el QR en Mercado Pago: responde `502`.
  - Error interno: responde `500`.
  - Con Mercado Pago activo, suma `"payment": {"status","qr_data","expires_at"}` (`qr_data` sólo mientras el pago está `pending`).
- `GET /orders/{id}`: devuelve la orden y sus líneas (con Mercado Pago activo incluye el bloque `"payment"`). La tablet lo consulta cada 2 s.
- `POST /orders/{id}/pay`: pago simulado; disponible **sólo** en `sandbox` sin Mercado Pago configurado (responde `403` en `production` o con Mercado Pago activo).

### Webhooks y callbacks de proveedores

- `POST /callbacks/mercadopago/{secret}`: webhook de Mercado Pago (secreto en ruta + firma `X-Signature`).
- `POST /callbacks/batidos/{secret}/{orderId}`: callback de Batidos (secreto en ruta; consulta a la API antes de cerrar).
- `POST /callbacks/snacks/{secret}`: callback de Snacks (secreto en ruta).

Si el secreto configurado está vacío o no coincide, los endpoints de callback responden `404`.

## Ejecución con Docker Compose

```bash
docker compose up -d --build
docker compose down          # conserva el volumen pgdata
docker compose down -v       # borra también la base de datos
```

La API responde en `http://127.0.0.1:18080`. Todo el stack de `docker-compose.yml` es local (`sandbox`, secretos de ejemplo y proveedores falsos).

| Servicio | Función |
| --- | --- |
| `db` | PostgreSQL 16 con volumen `pgdata` y healthcheck `pg_isready` |
| `migrate` | Ejecuta `cmd/migrate` con las migraciones embebidas en el binario bajo advisory lock y checksum SHA-256; termina al finalizar |
| `api` | Ejecuta `cmd/api` con `STORE=postgres`, sembrando muebles y combos desde `config.docker.json` y `combos.example.json` (upsert seguro) |
| `fakeapi` | Ejecuta `cmd/fakeapi` exponiendo `9401` (batidos) y `9402` (snacks) sólo en `127.0.0.1`, con `DEMO-BATIDO-VAINILLA` sembrado en batidos |

Las migraciones aplicadas son inmutables: si un archivo ya registrado en `schema_migrations` cambia su checksum SHA-256 (normalizando CRLF a LF), `migrate` aborta antes de aplicar pendientes. Si existe un volumen `pgdata` creado antes de existir `schema_migrations`, ejecuta `docker compose down -v` una vez.

### Apagado ordenado (producción)

`docker-compose.yml` fija `stop_grace_period: 75s` en el servicio `api`. Cualquier orquestador o supervisor en producción debe dar al menos **75 s** entre `SIGTERM` y `SIGKILL` (por ejemplo `terminationGracePeriodSeconds: 75` en Kubernetes o `TimeoutStopSec=75` en systemd):

- **10 s** para `server.Shutdown` (cerrar conexiones HTTP entrantes).
- **60 s** de drenaje para esperar a que terminen el barrido y los despachos en curso (`engine.Wait`), antes de cerrar la base de datos.
- **5 s** de margen para el cierre del store.

Alcance (pendiente **F7b**): si el proceso recibe un corte abrupto (`SIGKILL` o caída eléctrica) durante el despacho de batidos, la línea queda en `in_doubt` porque el código de campaña no se guarda antes de `ProduceCoffee`.

## Variables de entorno y secretos

Las credenciales nunca se versionan. Las variables de entorno pisan los valores del archivo JSON de configuración (`config.local.json`):

| Variable | Uso |
| --- | --- |
| `CONFIG_PATH` | Ruta al JSON de topología (por defecto `config.local.json`, o flag `-config`) |
| `COMBOS_PATH` | Ruta al JSON de catálogo (por defecto `combos.local.json`, o flag `-combos`) |
| `HTTP_ADDR` | Dirección de escucha HTTP de `cmd/api` (por defecto `127.0.0.1:8080`) |
| `LOG_LEVEL` | Nivel de log para los tres binarios (`debug`, `info`, `warn`, `error`, insensible a mayúsculas; por defecto `info`). Un valor inválido aborta el arranque |
| `STORE` | `memory` (por defecto) o `postgres`; pisa `store` del JSON |
| `DATABASE_URL` | DSN de PostgreSQL, obligatorio si `STORE=postgres` y para `cmd/migrate`; pisa `database_url` del JSON |
| `PUBLIC_BASE_URL` | URL pública base de este backend (opcional; en `production` exige `https`). Sin ella la campaña de batidos se crea sin `notifyUrl` y el callback no llega |
| `BATIDOS_KEY` | `key` del proveedor de Batidos (GS merchant API) |
| `BATIDOS_SECRET` | `secret` del proveedor de Batidos |
| `BATIDOS_CALLBACK_SECRET` | Segmento secreto en la ruta del callback de Batidos (opcional) |
| `SNACKS_ACCESS_KEY` | `access_key` del proveedor de Snacks (OurVend open API) |
| `SNACKS_APP_SECRET` | `app_secret` del proveedor de Snacks |
| `SNACKS_CALLBACK_SECRET` | Segmento secreto en la ruta del callback de Snacks (opcional) |
| `MP_ACCESS_TOKEN` | Access token de Mercado Pago. Con valor: cobro por QR activo y pago simulado apagado. Vacío: pago simulado (sólo `sandbox`; `production` rechaza arrancar) |
| `MP_WEBHOOK_SECRET` | Secreto de firma `X-Signature` del webhook de Mercado Pago; obligatorio con el token |
| `MP_CALLBACK_SECRET` | Segmento secreto en la ruta del webhook de Mercado Pago; obligatorio con el token |
| `MP_BASE_URL` | Base URL de Mercado Pago (por defecto `https://api.mercadopago.com`; el falso local usa `http://127.0.0.1:9404`) |
| `TELEGRAM_BOT_TOKEN` | Token del bot de Telegram para avisos a soporte. Vacío: avisos sólo al log (sólo `sandbox`; `production` rechaza arrancar) |
| `TELEGRAM_CHAT_ID` | ID del chat o grupo de soporte; obligatorio con el token |
| `TELEGRAM_BASE_URL` | Base URL de Telegram Bot API (por defecto `https://api.telegram.org`) |

Notas operativas:

- **Mercado Pago es todo o nada**: una configuración a medias (secretos sin token, token sin secretos o algún mueble sin `mp_pos_id`) impide arrancar la API.
- **Telegram**: los avisos (`order_in_doubt`, `order_failed`, `order_partial`, `dispatch_panic`, `payment_mismatch`, `payment_unknown`) siempre se escriben primero en el log. Si Telegram falla tras 3 intentos, el error queda en el log (`ERROR`) y la orden continúa.
- **Secretos locales**: guárdalos en `.env` (ignorado por Git) y cárgalos en tu shell antes de ejecutar (`set -a; source .env; set +a` en bash). Nunca pongas valores reales en `config.example.json` ni fuerces el agregado de `.env` o `*.local.json`. Los valores de prueba (`mysecret`, `testsecret`, `ak_test`, `TEST-fake-token`) son sintéticos para `fakeapi`.

## Logueo

Los tres binarios (`cmd/api`, `cmd/migrate`, `cmd/fakeapi`) emiten logs en formato JSON estructurado (`slog.JSONHandler`) por salida estándar, filtrados por `LOG_LEVEL`.

En `cmd/api`, cada petición HTTP pasa por `httpapi.Wrap`:

- Genera un `X-Request-Id` único en el servidor (ignorando el header entrante), lo devuelve en la respuesta y lo propaga en el contexto para que los logs con `*Context` incluyan `request_id`.
- Recupera panics en handlers devolviendo `500` en JSON (o aborta con `http.ErrAbortHandler` si ya se enviaron cabeceras).
- Registra un access log por petición (`info` para 2xx/3xx, `warn` para 4xx, `error` para 5xx o abortadas) usando el patrón de ruta (`route`/`path`) o la ruta limpia y redactada (`/callbacks/***`), sin query string ni headers.
- `http.Server.ErrorLog` también apunta al logger JSON en nivel `error`.

## Qué no está en el repositorio

| Excluido (`.gitignore`) | Motivo |
| --- | --- |
| `docs/proveedores/` (`batidos-api.md`, `snacks-api.pdf`, `snacks-api.txt`) | Documentación original de terceros con avisos de copyright (copiar manualmente en local) |
| `design/` | Tableros y capturas internas de diseño |
| `.env`, `.env.*`, `*.local.json` | Credenciales y topologías reales |

## Tests

`main` no contiene archivos `*_test.go`. Los 64 archivos de tests viven en la rama `archive/tests` (incluida la verificación de firmas HMAC/MD5 contra vectores de `openssl`, fechas `UTC+8` de campaña y rechazos de replay en los falsos). Consulta [docs/arquitectura.md](docs/arquitectura.md) para el flujo de trabajo con `archive/tests`.
