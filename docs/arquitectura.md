# Arquitectura del proyecto

Backend de combos para un mueble con dos máquinas expendedoras independientes (batidos y snacks) y una tablet central. La aplicación Android de la tablet se desarrolla en un repositorio independiente (`../compras-remotas-app`).

Este documento unifica la visión general del sistema (paquetes, dependencias, invariantes y reglas) con el detalle técnico paso a paso referenciado con `archivo:línea` para navegar directo al código fuente.

Para consultar el estado de las tareas pendientes, deudas técnicas y bloqueos externos, consulta [pendientes.md](pendientes.md). Para explicaciones detalladas símbolo por símbolo, consulta [comentarios-codigo.md](comentarios-codigo.md).

![Flujo del sistema](flujo-del-sistema.svg)

---

# PARTE 1: Visión general

## Regla de oro antes de leer código

**El código no lleva comentarios, a propósito.** Toda la explicación —qué hace cada símbolo y por qué se decidió así— vive en [comentarios-codigo.md](comentarios-codigo.md), indexado por archivo y símbolo. Si buscas el porqué de algo, busca ahí el nombre del símbolo. Este documento te dice **dónde** mirar; ese otro te dice **por qué**.

## Comandos

```bash
go test ./...                          # en main no hay tests: los 479 (55 archivos) viven en la rama archive/tests
go test ./... -race                    # NO corre en esta máquina (sin compilador de C)
gofmt -l . && go vet ./... && go build ./...

# tests: se traen de archive/tests para trabajar y se borran antes del commit en main
git checkout archive/tests -- $(git ls-tree -r --name-only archive/tests | grep '_test.go$')
git rm -q -f $(git ls-files '*_test.go')   # índice y archivo en un solo paso; -f cubre la copia editada

# local, contra los servidores falsos
go run ./cmd/fakeapi                   # 9401 batidos, 9402 snacks, 9403 administración, 9404 Mercado Pago
go run ./cmd/api -config config.local.json -combos combos.local.json

# esquema: una vez por base, antes de arrancar la API con STORE=postgres
DATABASE_URL="postgres://compras:compras@127.0.0.1:5432/compras?sslmode=disable" go run ./cmd/migrate

# contenedor (requiere Docker Desktop corriendo)
docker compose up -d --build           # db + migrate + api + fakeapi
docker compose down -v                 # una vez si el volumen es anterior al runner: las tablas ya están y falta schema_migrations
```

## Tests: dónde viven y cómo se usan

`main` no tiene ni un archivo de test. Los 55 `*_test.go` —479 tests— viven en la rama `archive/tests`, que se sube a `origin` junto con `main`. El flujo de trabajo es el siguiente:

- **Antes de tocar comportamiento**, traer los tests con el comando `git checkout` indicado arriba. Sin ellos `go test ./...` responde `[no test files]` en todos los paquetes: la suite no existe hasta que se traen.
- **El test nuevo va primero**, en el paquete que le corresponde y usando los ayudantes que ya usan los demás archivos de ese paquete (paquete externo `_test` cuando el test mira el borde). Se corre la suite completa, no sólo el test nuevo.
- **Antes de commitear en `main`**, borrar los tests con `git rm` indicado arriba: `main` no recibe ninguno.
- **El test nuevo se commitea en `archive/tests`**, en un commit propio, para que el borrado de `main` no se lo lleve: recuperar, escribir, correr, borrar en `main` y archivar.
- **La limpieza es un solo paso.** `git rm -q -f` saca la entrada del índice y el archivo a la vez; el flag `-f` hace falta cuando el test restaurado se editó y ya no coincide con `HEAD`. El par `git rm --cached` + `rm $(git ls-files --others '*_test.go')` no sirve: después del `checkout` los archivos quedan rastreados, `--others` los devuelve vacío y `rm` falla por falta de argumentos.

**Costo:** mientras los tests vivan sólo en `archive/tests`, `main` no tiene red de regresión y la integración continua (CI) no puede correr la suite de forma automática.

## Mapa de paquetes

| Paquete | Responsabilidad | Símbolos que importan |
| --- | --- | --- |
| `cmd/api` | Composition root: es el único que conoce a la vez proveedores y dominio | `main`, `newServer`, `openStore` |
| `cmd/fakeapi` | Levanta los dos sustitutos locales + siembra inicial + guardia de loopback | `main`, `requireLoopback` |
| `migrations` | Los `.sql` del esquema, **dentro del binario** (`//go:embed`) | `Files` |
| `cmd/migrate` | Aplica las migraciones pendientes y termina; es el paso previo del compose | `main`, `run` |
| `internal/migrate` | Detecta qué archivos faltan y vigila huellas SHA-256 de las ya aplicadas, en una transacción por archivo, bajo un advisory lock | `Apply`, `versions`, `checksum`, `missing`, `applyOne` |
| `internal/machine` | **Vocabulario compartido**: la familia de máquina y la referencia a una máquina concreta | `Kind`, `Batidos`, `Snacks`, `Ref` |
| `internal/linestate` | **Vocabulario compartido**: el estado de una línea | `State`, `Pending`, `Sent`, `Brewing`, `Delivered`, `Failed`, `InDoubt` |
| `internal/config` | Topología del mueble desde JSON + secretos por entorno, validada al arrancar | `Load`, `Config`, `Kiosk` (con `MPPosID`), `Machine`, `validate`, `MercadoPagoEnabled` |
| `internal/catalog` | El menú: carga, valida y sirve combos | `Combo`, `Item`, `LoadFile`, `Memory` |
| `internal/dispenser` | **El puerto** hacia las máquinas, sin saber de proveedores | `Dispenser`, `Request`, `DispatchResult`, `Status`, `Rejection` |
| `internal/dispenser/batidos` | Adaptador GS: firma MD5, campaña, canje, `getOrderInfo` | `Client`, `Dispenser`, `CampaignTime` |
| `internal/dispenser/snacks` | Adaptador YS: HMAC, nonce, despacho remoto, consulta | `Client`, `Dispenser`, `PageQuery` |
| `internal/store` | **El puerto** de persistencia + memoria | `Store`, `Order`, `Line`, `Attempt`, `Webhook`, `PaymentIntent`, `OrderStatus`, `PaymentStatus`, `AttemptOutcome`, `ErrStale`, `ErrInvalid`, `NotificationSent`, `RecordNotification`, `OverdueLines`, `StalledLines`, `PendingPaymentIntents` |
| `internal/store/postgres` | Implementación sobre Postgres (importa también `catalog` y `config` para `seed.go`) | `Open`, `Store`, `Seed` |
| `internal/topology` | Upsert de muebles/máquinas/combos al arrancar (Postgres tiene FK) | `Seed` |
| `internal/order` | **El dominio**: crear, cobrar, despachar, reconciliar, cerrar | `Engine`, `Draft`, `CreateOrder`, `RequestQR`, `SyncPayment`, `SyncPendingPayments`, `MarkPaid`, `SyncOverdueLines`, `ApplyCallback`, `SyncLineFromCallback`, `AvailabilityChecker`, `NotSellableError`, `DeriveOrderStatus` |
| `internal/gate` | Adaptador `order.AvailabilityChecker` sobre `preflight` | `New`, `Check` |
| `internal/preflight` | La regla de disponibilidad (consulta las dos máquinas y sus productos) | `Checker`, `Request`, `Result`, `Reason` |
| `internal/httpapi` | El borde: **tres contratos separados** (tablet, callbacks de máquinas y webhook de Mercado Pago) | `Handler`, `Callbacks`, `MercadoPagoWebhook`, `Deps`, `BatidosCallbackURL` |
| `internal/payments` | **El puerto** de cobro (`Gateway`: crear QR, consultar, cancelar). También el puerto de devoluciones con su mock, que sigue sin conectar (fuera del MVP) | `Gateway`, `Charge`, `ChargeStatus`, `QRRequest`, `Refunder`, `MockRefunder` |
| `internal/payments/mercadopago` | Adaptador de la Orders API de Mercado Pago (`net/http`, sin SDK) + validación de la firma del webhook | `Client`, `VerifySignature`, `SignatureHeader` |
| `internal/notify` | Puerto de avisos + transportes por log y por Telegram | `Notifier`, `Kind`, `Event`, `LogNotifier`, `TelegramNotifier` |
| `internal/sweep` | Barrido periódico que dispara `SyncOverdueLines` y, con Mercado Pago, `SyncPendingPayments` | `Runner`, `New`, `WithPaymentSync` |
| `internal/logging` | `slog` con mensajes capitalizados en el borde | `New`, `Capitalize` |
| `internal/fakeapi` | Implementación de los proveedores falsos (batidos, snacks, Mercado Pago) + administración | `NewBatidos`, `NewSnacks`, `NewMercadoPago`, `NewAdmin` |

## Dirección de las dependencias

Todas las flechas apuntan **hacia adentro**. Los puertos no conocen adaptadores:

```
cmd/api ──► httpapi ──► order ──► store (puerto) · dispenser (puerto) · payments (puerto) · machine · linestate · notify
                │           │                ▲            ▲                    ▲
                │           └──► gate ──► preflight      │                    │
                └──► catalog, config          batidos ───┘   payments/mercadopago
                                              snacks ────┘
store/postgres ──► store (tipos) · topology · machine · linestate · catalog · config
```

Reglas que no se rompen:

- `order` **no** importa `batidos` ni `snacks` ni `payments/mercadopago` ni `httpapi` ni `config`. Sí importa el puerto `payments`.
- `catalog` no importa `dispenser`.
- `machine` y `linestate` no importan nada del proyecto: son vocabulario, no comportamiento.
- `store` **no** importa `dispenser`: la referencia de la máquina es `machine.Ref` y el estado de la línea, `linestate.State`. El puerto no dicta cómo se guarda una fila.

## Invariantes

Si un cambio rompe alguna de estas invariantes, el cambio está mal:

1. **El precio y el SKU salen del catálogo**, nunca del cliente. El cuerpo del pedido sólo trae `combo_id`, `client_key` y `kiosk_id`. Cada ítem del catálogo (`combos.example.json`) lleva su `price_cents` (> 0) y la suma de sus ítems debe coincidir con el `price_cents` del combo.
2. **Idempotencia de la creación** por `client_key`: la consulta rápida en memoria o base de datos es la vía rápida, la restricción de clave única del store es la verdad definitiva. Contrato del kiosco: `client_key` es un UUID por pedido confirmado, reutilizado sólo para reintentar ese mismo pedido (`<= 128` caracteres tras `strings.TrimSpace`, si supera responde **400**; misma clave con mismo `combo_id` y `kiosk_id` devuelve **200** con la orden existente; misma clave con otro combo u otro mueble responde **409 Conflict** y registra un `warn` en el log, sin crear ni modificar órdenes).
3. **La línea lleva una sola familia de máquina** (`line.Machine.Kind`). En el struct `Line`, `MachineType` fue reemplazado por `Machine.Kind`; no obstante, el nombre `MachineType` se mantiene legítimamente en `internal/order/callback.go:33`, `internal/catalog/catalog.go:17` e `internal/httpapi/responses.go:27`.
4. **El estado de la orden se deriva de las líneas** (`DeriveOrderStatus`), nunca se inventa; `settleOrder` lo recalcula después de cada cambio en cualquier línea.
5. **Una línea cerrada no se reabre** (`delivered` y `failed` son terminales).
6. **La línea y la orden se escriben con versión** (`SaveLine` y `SaveOrder`, ambos condicionales, retornan `ErrStale` si la versión en memoria es vieja). El worker de barrido, los callbacks y dos peticiones `/pay` concurrentes no se pisan.
7. **El dedupe se registra después del efecto durable.** Un fallo transitorio no quema la clave. Excepción: lo que nunca va a poder aplicarse se registra antes, para evitar reintentos eternos.
8. **Reintento sólo ante rechazo de negocio claro.** Sólo un HTTP 4xx (salvo 408 y 429) con sobre y `code` del proveedor, o un HTTP 200 con código no exitoso, devuelve `*dispenser.Rejection` y se reintenta (salvo en el paso 2 de batidos: tras emitir el código de canje, cualquier fallo de `ProduceCoffee` queda `in_doubt` con ese código para reconciliar y nunca crea una segunda campaña). Cualquier 5xx, 429, 408, timeout, error de red, JSON inválido o respuesta sin `code` va a «en duda» (`in_doubt`) y se reconcilia; nunca se reintenta a ciegas.
9. **El callback de batidos no cierra nada por sí solo**: confirma la entrega consultando directamente al proveedor (R6).
10. **Los secretos de callback viajan en la ruta del path** y se comparan en tiempo constante; sin secreto configurado se responde 404 igual que una ruta inexistente. El path se redacta en los logs.
11. **El pago simulado sólo existe en `sandbox` y sin Mercado Pago.** O se cobra simulado o con Mercado Pago completo; cualquier otro caso lo rechaza antes de tocar el motor de órdenes.
12. **Si el pago se registró y el despacho falló, la respuesta es 200 con el estado real**, no un 5xx que invite al cliente a pagar dos veces.
13. **Manda Mercado Pago, no nuestro reloj.** Nunca se cancela una orden por cuenta propia: al vencer el QR se le pide a Mercado Pago que lo cancele y se obedece su respuesta. Si dice «pagada», se despacha.
14. **El webhook de Mercado Pago no se cree por su cuerpo**: la firma prueba de quién viene, y lo que pasó se le pregunta a Mercado Pago (`Lookup`).
15. **Un monto pagado distinto al del catálogo no se despacha**: la orden queda en `pending_payment`, el pago en `amount_mismatch` y se avisa a soporte.

## Vocabularios: dónde vive cada uno

| Tipo | Vive en | Valores |
| --- | --- | --- |
| `machine.Kind` | `internal/machine` | `batidos`, `snacks` |
| `linestate.State` | `internal/linestate` | `pending`, `sent`, `brewing`, `delivered`, `failed`, `in_doubt` |
| `store.OrderStatus` | `internal/store` | `pending_payment`, `paid`, `dispatching`, `delivered`, `partial`, `failed`, `in_doubt`, `refunded`, `cancelled` |
| `store.AttemptOutcome` | `internal/store` | `ambiguous`, `accepted`, `rejected`, `error` |
| `store.PaymentStatus` | `internal/store` | `pending`, `approved`, `expired`, `cancelled`, `amount_mismatch` |
| `payments.ChargeStatus` | `internal/payments` | `pending`, `approved`, `expired`, `cancelled`, `unknown` |
| `notify.Kind` | `internal/notify` | `order_in_doubt`, `order_failed`, `order_partial`, `payment_mismatch`, `payment_unknown` |
| `preflight.Reason` | `internal/preflight` | `batidos_device_offline`, `snacks_sku_out_of_stock`, … |

## Glosario de verbos

Los nombres del motor y de sus puertos siguen un vocabulario fijo (revisión de terminología del 2026-11-18):

| Verbo | Significa | Ejemplos |
| --- | --- | --- |
| **Create** | Crea algo nuevo | `CreateOrder` (valida, chequea disponibilidad, idempotente por `client_key`) |
| **Mark** | Cambia un estado propio sin preguntarle a nadie | `MarkPaid` (marca pagada y despacha; hoy lo usa el pago simulado) |
| **Apply** | Aplica un aviso tal cual, porque el aviso es la verdad | `ApplyCallback` (callback de snacks) |
| **Sync** | Le pregunta al proveedor cómo está algo y aplica lo que responde | `SyncPayment` (webhook de MP), `SyncLineFromCallback` (callback de batidos), `SyncOverdueLines`, `SyncPendingPayments` |
| **Barrido** | El worker de fondo (`sweep.Runner`) que cada 30 s llama a los `Sync…` | `sweep.WithPaymentSync` |

«Overdue» es **vencida** (`deadline_at` ya pasó), no «en duda» (`in_doubt`). Las interfaces de los bordes se nombran por lo que piden: `OrderCreator`, `PaidMarker`, `QRRequester`, `PaymentSyncer`, `CallbackHandler`, `AvailabilityChecker`.

## Valores por defecto que no están en configuración

| Constante | Valor | Dónde |
| --- | --- | --- |
| Plazo por línea | 2 min | `order/reconcile.go` |
| Ventana de reconciliación | 2 min | `order/reconcile.go` |
| Intervalo de reconciliación | 30 s | `order/reconcile.go` |
| Intentos máximos por línea | 2 | `order/dispatch.go` |
| Barrido periódico | Cada 30 s, 10 líneas y 10 pagos | `cmd/api/main.go` |
| Vencimiento del QR | 180 s | `order/qr.go` (`defaultQRExpiry`) |
| Antigüedad mínima para revisar un QR en el barrido | 10 s | `order/reconcile_payments.go` |
| Margen tras el vencimiento antes de pedir la cancelación | 30 s | `order/reconcile_payments.go` |
| Ventana de la firma del webhook de Mercado Pago | 5 min | `payments/mercadopago/signature.go` (`SignatureTolerance`) |
| Tiempo de lectura de encabezados HTTP (`ReadHeaderTimeout`) | 5 s | `cmd/api/server.go:9` |
| Tiempo de lectura del servidor HTTP (`ReadTimeout`) | 10 s | `cmd/api/server.go:10` |
| Tiempo de escritura del servidor HTTP (`WriteTimeout`) | 60 s | `cmd/api/server.go:11` |
| Conexión ociosa (`IdleTimeout`) | 60 s | `cmd/api/server.go:12` |
| Encabezados máximos (`MaxHeaderBytes`) | 64 KB | `cmd/api/server.go:13` |
| Cierre HTTP en apagado (`httpShutdownTimeout`) | 10 s | `cmd/api/shutdown.go` |
| Drenaje de barrido y despachos (`drainTimeout`) | 60 s | `cmd/api/shutdown.go` |
| Cuerpo máximo aceptado (`maxBodyBytes`) | 64 KB | `httpapi/respond.go` |
| Presupuesto del chequeo previo | 8 s | `preflight/checker.go` |

### Apagado ordenado (producción)

En cualquier orquestador o supervisor debe configurarse un margen mínimo de **75 s** entre `SIGTERM` y `SIGKILL` (en `docker-compose.yml` se fija `stop_grace_period: 75s`; en Kubernetes equivale a `terminationGracePeriodSeconds: 75` y en systemd a `TimeoutStopSec=75`): **10 s** para `server.Shutdown` más **60 s** de drenaje (primero el barrido, luego `engine.Wait`) y **5 s** de margen antes de cerrar la base. Si vence el drenaje, se registra el trabajo pendiente y se cierra el store igual. F7b no está cubierto: un corte abrupto (`SIGKILL`) durante batidos deja la línea en `in_doubt` porque el código de campaña no se guarda antes de `ProduceCoffee`.

## Convenciones

- **Identificadores, claves de log y JSON en inglés**; **comentarios, mensajes de error y logs en español**. No hay comentarios directos en el código fuente.
- Mensajes de commit en **español**, Conventional Commits, asunto en imperativo y sin punto final.
- El registro histórico de decisiones es `odd/tasks/combos-remotos.md` (entradas `L#`), con las especificaciones `S#` y las restricciones de proveedor `R#`. Es memoria interna y **sí se versiona**.
- Los documentos originales de los proveedores residen exclusivamente en local bajo `docs/proveedores/` y están en `.gitignore`: un clon nuevo no los tiene (consultar [pendientes.md](pendientes.md) para más detalle).

---

# PARTE 2: Detalle con archivo:línea

## 0. Capas: quién llama a quién

```
                ┌──────────────┐        ┌───────────────────────────┐
  Tablet ──────►│ internal/    │──────► │ internal/order  (Engine)  │  ← el cerebro
  Proveedores ─►│ httpapi      │        │ place · payment · dispatch│
  (callbacks)   └──────────────┘        │ callback · reconcile ·    │
                                        │ settle · status           │
  internal/sweep (cada 30 s) ─────────► └───┬────────┬─────────┬────┘
                                            │        │         │
                            ┌───────────────▼┐  ┌────▼─────┐ ┌─▼──────────┐
                            │ internal/store │  │dispenser │ │ gate →     │
                            │ (puerto)       │  │(puerto)  │ │ preflight  │
                            └──┬──────────┬──┘  └──┬────┬──┘ └────────────┘
                         memory.go   postgres/  batidos/ snacks/
                                         │         └─ HTTP a las máquinas ─┘
                                 migrations/ + internal/migrate
  Vocabulario sin dependencias: internal/machine (Ref, Kind) · internal/linestate (State)
```

Un **puerto** es una interfaz que `order` usa sin saber quién la implementa (`store.Store`, `dispenser.Dispenser`); por eso `order` no importa ni `postgres` ni `batidos`. Patrón: *ports and adapters* (arquitectura hexagonal).

## 1. Arranque: `cmd/api/main.go`

```
main (:41)
 ├─ flags -config / -combos ............................... :42
 ├─ config.Load ........................ internal/config/config.go:115
 ├─ catalog.LoadFile → catalog.NewMemory  catalog/catalog.go:37, memory.go:14
 ├─ batidos.NewClient / snacks.NewClient  dispenser/batidos/client.go:59
 │                                        dispenser/snacks/client.go:227
 ├─ batidos.NewDispenser / snacks.NewDispenser ............ main.go:94
 ├─ openStore (:257) ─ "postgres"? ─ sí → postgres.Open (postgres/postgres.go:41)
 │                                │        + Seed (postgres/seed.go:12)
 │                                └ no → store.NewMemory (store/memory.go:45)
 ├─ gate.New(preflight.New) ............ gate/gate.go:23, preflight/checker.go:68
 ├─ ¿Mercado Pago? → mercadopago.NewClient + order.WithQRPayments ... main.go:134
 ├─ order.New(store, machines, notifier, gate, pagos) .. order/engine.go:61
 ├─ sweep.New(engine) (30 s, lote 10; + pagos con MP) .. sweep/runner.go:57
 ├─ httpapi.New (+ QRRequester/Payments con MP) ...... main.go:168
 ├─ rutas en el mux (+ webhook de MP) ............ main.go:208, :215
 ├─ newServer (timeouts) .......................... cmd/api/server.go:16
 ├─ go sweep.Run ........ :228        go ListenAndServe ........ :236
 └─ SIGINT/SIGTERM → shutdown (cmd/api/shutdown.go): 10 s HTTP,
    60 s drenaje (barrido y luego engine.Wait, que cierra el motor
    para que spawn no arranque trabajo nuevo), y closeStore después .. :244-254
```

Aparte: `cmd/migrate/main.go:35 run` → `migrate.Apply` (`internal/migrate/migrate.go:36`); `cmd/fakeapi/main.go:22` levanta máquinas falsas (puertos 9401 batidos, 9402 snacks, 9403 administración local, 9404 Mercado Pago).

## 2. Rutas HTTP

| Ruta | Handler | Llama a |
| --- | --- | --- |
| `GET /healthz` | `cmd/api/main.go:137` | Retorna 200 OK |
| `GET /combos` | `httpapi/combos.go:7 listCombos` | `catalog/memory.go:40 Combos` |
| `POST /orders` | `httpapi/orders.go:10 createOrder` | `order/place.go:12 CreateOrder` + (con MP) `order/qr.go:21 RequestQR` |
| `GET /orders/{id}` | `httpapi/orders.go:56 readOrder` | `store.LoadOrder` + (con MP) `store.PaymentIntentByOrder` |
| `POST /orders/{id}/pay` | `httpapi/orders.go:79 payOrder` | `order/payment.go:12 MarkPaid` (simulado; 403 fuera de sandbox o con MP) |
| `POST /callbacks/mercadopago/{secret}` | `httpapi/mercadopago.go:77 receive` | `order/charge.go:15 SyncPayment` |
| `POST /callbacks/batidos/{secret}/{orderId}` | `httpapi/callbacks.go:116` | `order/callback.go:55 SyncLineFromCallback` |
| `POST /callbacks/snacks/{secret}` | `httpapi/callbacks.go:155` | `order/callback.go:38 ApplyCallback` |

## 3. Flujo de una compra, de punta a punta

```
TABLET
  │ POST /orders {combo_id, client_key, kiosk_id}
  ▼
httpapi/orders.go:10 createOrder
  ├─ decodeCreateOrder ...... httpapi/draft.go:26   (64 KB, sin campos extra, trim de combo_id y client_key <= 128 runas → 400)
  ├─ resolveKiosk ........... httpapi/draft.go:52
  └─ buildDraft ............. httpapi/draft.go:74   (precio del combo y por ítem SIEMPRE del catálogo)
  ▼
order/place.go:14 CreateOrder
  ├─ validateDraft :95 ──► machine.Ref.Validate (identificadores según Kind)
  ├─ ¿ya existe client_key? (:20, o tras ErrAlreadyExists en :62) ──► matchClientKeyOrder :76
  │     ├─ mismo combo y mueble → devuelve la misma orden (200, idempotencia)
  │     └─ otro combo u otro mueble → warn en log + ErrClientKeyConflict → 409 (httpapi/errors.go:48)
  ├─ gate.Check (gate/gate.go:27) → preflight.Check (preflight/checker.go:113)
  │     plazo total 8 s, una goroutine por máquina:
  │       checkBatidos :161  (Device + DeviceGoods)
  │       checkSnacks  :193  (Devices + Slots)
  │     no vendible → NotSellableError → 409 (httpapi/errors.go:75)
  │     falla o timeout del proveedor → ErrAvailabilityUnavailable → 502 (httpapi/errors.go:53), sin crear orden
  │     panic en preflight (ErrPanic), sin verificador o combo no evaluable → 500, sin crear orden
  └─ CreateOrder: orden pending_payment, líneas pending (:62)
  ▼
  └─ (con MP) RequestQR (order/qr.go:21): crea el QR en MP (idempotencia = id de orden; si falla → ErrQRUnavailable → 502),
     guarda el payment_intent y la respuesta suma payment {status, qr_data, expires_at}
  ▼
COBRO, por uno de dos caminos:

  a) Con Mercado Pago: el cliente paga el QR
     MP ──► POST /callbacks/mercadopago/{secret} (httpapi/mercadopago.go:77)
            secreto de ruta → firma → type == order
       ▼
     order/charge.go:15 SyncPayment
       ├─ Lookup en MP (no se usa el cuerpo del aviso)
       ├─ external_reference → payment_intent de la orden, mismo cobro
       └─ applyCharge (:45)
            approved + monto correcto → claimPaid y despacho en segundo plano (spawn)
            approved + monto distinto → no despacha, aviso payment_mismatch
            expired / cancelled       → orden cancelled
            unknown                   → aviso payment_unknown, no toca nada
     El webhook responde 200 sin esperar el despacho. Si el aviso se pierde, lo
     recupera SyncPendingPayments (order/reconcile_payments.go:17) en el barrido.

  b) Sin Mercado Pago (sólo sandbox): TABLET POST /orders/{id}/pay
  ▼
order/payment.go:12 MarkPaid = claimPaid (:21) + dispatchLines (:58)
  ├─ si no está pending_payment → no hace nada (:18)
  ├─ SaveOrder → paid       (ErrStale: otro /pay ganó → termina sin despachar)
  ├─ AdvanceOrder → dispatching (order/status.go:92)
  ├─ detached := context.WithoutCancel(ctx) (:47)  ← sobrevive al corte del cliente
  ├─ una goroutine por línea → dispatchWithinDeadline (order/dispatch.go:23)
  │                            con context.WithTimeout de 2 min
  └─ wg.Wait → settleOrder (order/settle.go:16)
```

## 4. El corazón: `dispatchLine` (`internal/order/dispatch.go:30`)

```
for line.Attempts < maxAttempts:
  │
  ├─ 1. RECLAMO  DeadlineAt = now+2m, ReconcileUntil = now+2m+ventana
  │      SaveLine (con versión) ── ErrStale → return (otro la tomó)
  │
  ├─ 2. AppendAttempt  clave "orden:línea:número", outcome ambiguous
  │      ErrAlreadyExists → return
  │
  ├─ 3. machine.Dispatch(...)  ──►  batidos/dispenser.go:95  o  snacks/dispenser.go:87
  │
  ├─ 4. classifyDispatch (:133)
  │       ┌────────────────────────────────────┬───────────┬────────────────┐
  │       │ qué pasó                           │ outcome   │ estado destino │
  │       ├────────────────────────────────────┼───────────┼────────────────┤
  │       │ rechazo de negocio (*Rejection)    │ rejected  │ failed         │
  │       │ red / timeout / 5xx / 429 / 408 /  │           │                │
  │       │ JSON inválido / sin code / paso 2  │ error     │ in_doubt       │
  │       │ Accepted = true                    │ accepted  │ sent           │
  │       │ no confirmó                        │ error     │ in_doubt       │
  │       └────────────────────────────────────┴───────────┴────────────────┘
  │
  ├─ 5. SaveAttempt (cierra el intento)
  │
  ├─ 6. ¿rechazo y quedan intentos?  (:87)
  │       sí → la línea SIGUE pending, sin plazos → vuelve al for
  │       no → transitionLine al estado destino (reconcile.go:234)
  │
  ├─ 7. plazos según resultado:
  │       accepted → Ref = ProviderRef, nuevo DeadlineAt de 2 min
  │       error    → DeadlineAt = now + reconcileInterval (vuelve pronto el barrido)
  │       rejected → plazos en cero
  │       (Ref se guarda siempre que venga, aunque no se haya aceptado: S4r)
  │
  └─ 8. SaveLine ── ErrStale → return
```

Regla clave: sólo se reintenta un **rechazo de negocio claro** (`*dispenser.Rejection`: HTTP 4xx salvo 408 y 429 con sobre y `code` del proveedor, o HTTP 200 con código no exitoso, y nunca tras emitir el código de canje en batidos). Un error de red, timeout, 5xx, 429, 408, respuesta ilegible/sin `code` o fallo en el paso 2 de batidos nunca se reintenta directamente, porque la petición pudo haber llegado a la máquina o el código ya haberse emitido; despachar dos veces causaría duplicaciones físicas. Esa línea queda `in_doubt` (con `Ref` si ya hubo código) y la resuelve la reconciliación.

### Quién mueve una línea `pending`

- **`MarkPaid`**: Es el camino habitual tras el pago. Ejecuta una vuelta de `dispatchLine` por intento, con el contexto separado de la cancelación HTTP del cliente y un plazo por línea igual al plazo de reclamo (2 minutos).
- **El barrido periódico (`sweep`)**: Aplica dos reglas independientes:
  - Una línea `pending` **con plazo vencido** quedó reclamada por un despacho que no dejó resultado comprobado: se **reconcilia** consultando al proveedor (nunca se vuelve a despachar a ciegas).
  - Una línea `pending` **sin plazo** que pertenezca a una orden en estado `paid` o `dispatching` pagada hace más de 2 minutos nunca llegó a reclamarse: se **despacha** ejecutando el intento siguiente (`resumeStalledLine`).

## 5. Cómo se cierra una línea (dos caminos)

```
 A) CALLBACK DEL PROVEEDOR                      B) BARRIDO (cada 30 s)
 httpapi/callbacks.go:116 / :155                sweep/runner.go:65 Run → tick :79
   └ secretMatches :270 (tiempo constante;        ▼
     si falla → 404)                            order/reconcile.go:20 SyncOverdueLines
   ▼                                              ├─ OverdueLines (plazo vencido, también pending)
 order/callback.go                                │    └ reconcileLine :96 → machine.Reconcile
   ├ recordCallback :187 (webhook_event:          │        ├ resuelto → closeLine :131
   │   duplicado → 200 sin procesar)              │        └ indeterminado → extendReconcile :187
   ├ callbackLine :103                            │             (pospone o pasa a in_doubt)
   │   lineByRef :124 → lineByThirdOrderNo :138   ├─ StalledLines (pending sin plazo, pagada
   │   → lineByMachineType :152                   │    hace más de 2 min)
   ├ SNACKS  ApplyCallback :38                │    └ resumeStalledLine :76 → dispatchWithinDeadline
   │   → applyCallback :205 (delivered/failed)    │
   └ BATIDOS SyncLineFromCallback :55              └─ settleOrder de cada orden tocada (:67)
       → markBrewing :222 + reconcileLine
         (no se confía en el aviso: se pregunta a la API)
   ▼
 settleOrder (order/settle.go:16)
```

Quién cierra una línea y cómo:
- **Snacks**: Sólo el callback (`ApplyCallback`) o el barrido. El barrido para snacks siempre concluye duda si no hay respuesta (`in_doubt`). `ApplyCallback` aplica directamente el resultado reportado por el proveedor (`delivered` o `failed`).
- **Batidos**: `SyncLineFromCallback` **no se fía** del webhook por sí solo (no viene firmado por el proveedor): marca la línea en preparación (`markBrewing`) y consulta inmediatamente `getOrderInfo` usando la misma función `reconcileLine` que utiliza el barrido periódico.

## 6. Asentar la orden y avisar (`internal/order/settle.go`)

```
settleOrder :16
 ├─ refreshOrderStatus :25  → AdvanceOrder (status.go:92) → SaveOrder
 │                            ErrStale → recarga y reintenta UNA vez
 └─ notifyOrder :54   ¿quedó in_doubt, failed o partial?
       └ NotificationSent? (:78) ── ya salió → nada
                                └─ no → Notify (notify/notify.go:34) → RecordNotification
                                        (primero el aviso, después el registro)
                                        Con Telegram (notify/telegram.go): log + hasta 3
                                        envíos; si fallan, ERROR en el log y devuelve nil
```

## 7. Máquinas de estado (`internal/order/status.go`)

```
LÍNEA (linestate)  CanLineTransition :72
  pending ──► sent ──► brewing ──┬──► delivered  (terminal)
                │                ├──► failed     (terminal)
                └────────────────┴──► in_doubt ──► delivered | failed

ORDEN (store.OrderStatus)  CanOrderTransition :49   (se deriva con DeriveOrderStatus :12)
  pending_payment ──► paid ──► dispatching ──► delivered | partial | failed | in_doubt
        └──► cancelled                     in_doubt ──► delivered | partial | failed | refunded
                                           failed | partial | delivered ──► refunded
```

## 8. Persistencia

- **Puerto** `internal/store/store.go:91-104`, 12 métodos: `CreateOrder`, `LoadOrder`, `OrderByClientKey`, `SaveOrder`, `SaveLine`, `AppendAttempt`, `SaveAttempt`, `RegisterWebhook`, `NotificationSent`, `RecordNotification`, `OverdueLines`, `StalledLines`.
- **Errores** `store.go:85-88`: `ErrNotFound`, `ErrAlreadyExists`, `ErrStale` (escritura concurrente detectada), `ErrInvalid` (violación de invariante o `CHECK`).
- **Implementaciones**: `store/memory.go:32` (mutex y copias profundas) y `store/postgres/postgres.go:31` (`WHERE version = $n`, *optimistic locking*: no bloquea al leer y detecta choques al escribir).
- **Migraciones**: `0001` esquema inicial · `0002` estado `failed` de orden · `0003` `reconcile_until` · `0004` `client_key` y versión de línea · `0005` versión de orden · `0006` `notification_sent` · `0007` `payment_intent` para el cobro con Mercado Pago (FK a la orden, un cobro por orden, `expires_at`, versión). `internal/migrate` las aplica de forma atómica y vigila su huella SHA-256.

## 9. Índice rápido: «¿dónde busco…?»

| Si buscas | Ve a |
| --- | --- |
| Por qué no se vende un combo o venta bloqueada | `internal/preflight/checker.go:125` / `:157` + `internal/gate` |
| Qué pasa al pagar | `internal/order/payment.go:12` (simulado) y `internal/order/charge.go:15` (Mercado Pago) |
| Cómo se crea el QR | `internal/order/qr.go:21` (`RequestQR`) y `internal/payments/mercadopago/client.go` |
| Por qué se rechazó un webhook de Mercado Pago | `internal/httpapi/mercadopago.go:77` y `internal/payments/mercadopago/signature.go` |
| Por qué se canceló una orden sin pagar | `internal/order/reconcile_payments.go:40` (`reconcilePayment`) |
| Por qué una línea no se reintenta | `internal/order/dispatch.go:133` (`classifyDispatch`) |
| Cuándo se reintenta o queda en duda | `internal/order/dispatch.go:87`, `:133` |
| Por qué una línea `pending` se recuperó | `internal/order/reconcile.go:20` (`SyncOverdueLines`, `resumeStalledLine` `:76`) y `StalledLines` en `internal/store` |
| Cuándo se declara una duda | `internal/order/reconcile.go:187` (`extendReconcile`) |
| Cómo se elige el driver de una máquina | `internal/order/engine.go` (`machines`) y `dispatch.go` |
| Por qué un callback no encontró su línea | `internal/order/callback.go:103` (`callbackLine`, tres reglas de resolución) |
| La forma de la URL de aviso / callback | `internal/httpapi/callback_url.go` (`BatidosCallbackURL`) |
| Cómo se habla con batidos o snacks | `internal/dispenser/batidos/dispenser.go:95`, `snacks/dispenser.go:87` |
| Firmas de los proveedores | `batidos/sign.go:31` (MD5), `snacks/sign.go:47` (HMAC-SHA256) |
| Qué hace el barrido periódico | `internal/order/reconcile.go:20` |
| Qué estados son válidos | `internal/order/status.go:49`, `:72` |
| Por qué un aviso de excepción no llegó o llegó una sola vez | `internal/order/settle.go:54` (`notifyOrder`) y la tabla `notification_sent` (`migrations/0006`) |
| El mapeo de una columna SQL | `internal/store/postgres/postgres.go` y `migrations/` |
| Qué rechaza el store antes de llegar a la base de datos | `internal/store/memory.go` (`validateOrder`, `validateLine`) y el check 23514 de `internal/store/postgres/postgres.go` |
| Qué versión del esquema está aplicada | `internal/migrate` (`versions`, `missing`) y `migrations/embed.go` |
| Timeouts y cotas del servidor HTTP | `cmd/api/server.go:16` |
| Cómo se arma el binario (orden de construcción) | `cmd/api/main.go` |
