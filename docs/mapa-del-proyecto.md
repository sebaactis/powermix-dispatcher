# Mapa del proyecto

Cómo está armado **compras-remotas**: backend de combos para un mueble con dos máquinas expendedoras (batidos y snacks). App Android en un repo aparte (`../compras-remotas-app`).

Léelo de arriba hacia abajo la primera vez; después usá las tablas como índice.

## Regla de oro antes de leer código

**El código no lleva comentarios, a propósito.** Toda la explicación —qué hace cada símbolo y por qué se decidió así— vive en `docs/comentarios-codigo.md`, indexado por archivo y símbolo. Si buscás el porqué de algo, buscá ahí el nombre del símbolo. Este documento te dice **dónde** mirar; ese otro te dice **por qué**.

## Comandos

```bash
go test ./...                          # en main no hay tests: los 341 (42 archivos) viven en la rama archive/tests
go test ./... -race                    # NO corre en esta máquina (sin compilador de C)
gofmt -l . && go vet ./... && go build ./...

# tests: se traen de archive/tests para trabajar y se borran antes del commit en main
git checkout archive/tests -- $(git ls-tree -r --name-only archive/tests | grep '_test.go$')
git rm -q -f $(git ls-files '*_test.go')   # índice y archivo en un solo paso; -f cubre la copia editada

# local, contra los servidores falsos
go run ./cmd/fakeapi                   # 9401 batidos, 9402 snacks, 9403 administración
go run ./cmd/api -config config.local.json -combos combos.local.json

# esquema: una vez por base, antes de arrancar la API con STORE=postgres
DATABASE_URL="postgres://compras:compras@127.0.0.1:5432/compras?sslmode=disable" go run ./cmd/migrate

# contenedor (requiere Docker Desktop corriendo)
docker compose up -d --build           # db + migrate + api + fakeapi
docker compose down -v                 # una vez si el volumen es anterior al runner: las tablas ya están y falta schema_migrations
```

## Tests: dónde viven y cómo se usan

`main` no tiene ni un archivo de test. Los 42 `*_test.go` —341 funciones— viven en la rama `archive/tests` (commit `8fc5761`), que se sube a `origin` junto con `main`. El flujo desde acá es este:

- **Antes de tocar comportamiento**, traer los tests con el `git checkout` de arriba. Sin ellos `go test ./...` responde `[no test files]` en todos los paquetes: la suite no existe hasta que se traen.
- **El test nuevo va primero**, en el paquete que le corresponde y usando los ayudantes que ya usan los demás archivos de ese paquete (paquete externo `_test` cuando el test mira el borde). Se corre la suite completa, no sólo el test nuevo.
- **Antes de commitear en `main`**, borrar los tests con el `git rm` de arriba: `main` no recibe ninguno.
- **El test nuevo se commitea en `archive/tests`**, en un commit propio, para que el borrado de `main` no se lo lleve: recuperar, escribir, correr, borrar en `main` y archivar.
- **La limpieza es un solo paso.** `git rm -q -f` saca la entrada del índice y el archivo a la vez; el `-f` hace falta cuando el test restaurado se editó y ya no coincide con `HEAD`. El par `git rm --cached` + `rm $(git ls-files --others '*_test.go')` no sirve: después del `checkout` los archivos quedan rastreados, `--others` los devuelve vacío y `rm` falla por falta de argumentos.

**Costo:** mientras los tests vivan sólo en `archive/tests`, `main` no tiene red de regresión y la CI no puede correr la suite.

## Mapa de paquetes

| Paquete | Responsabilidad | Símbolos que importan |
| --- | --- | --- |
| `cmd/api` | composition root: es el único que conoce a la vez proveedores y dominio | `main`, `newServer`, `openStore` |
| `cmd/fakeapi` | levanta los dos sustitutos locales + siembra inicial + guardia de loopback | `main`, `requireLoopback` |
| `migrations` | los `.sql` del esquema, **dentro del binario** (`//go:embed`) | `Files` |
| `cmd/migrate` | aplica las migraciones pendientes y termina; es el paso previo del compose | `main`, `run` |
| `internal/migrate` | qué archivos faltan, en una transacción por archivo, bajo un advisory lock | `Apply`, `versions`, `missing`, `applyOne` |
| `internal/machine` | **vocabulario compartido**: la familia de máquina | `Kind`, `Batidos`, `Snacks` |
| `internal/config` | topología del mueble desde JSON + secretos por entorno, validada al arrancar | `Load`, `Config`, `Kiosk`, `Machine`, `validate` |
| `internal/catalog` | el menú: carga, valida y sirve combos | `Combo`, `Item`, `LoadFile`, `Memory` |
| `internal/dispenser` | **el puerto** hacia las máquinas, sin saber de proveedores | `Dispenser`, `MachineRef`, `Request`, `DispatchResult`, `Status`, `Rejection`, `LineState` |
| `internal/dispenser/batidos` | adaptador GS: firma MD5, campaña, canje, `getOrderInfo` | `Client`, `Dispenser`, `CampaignTime` |
| `internal/dispenser/snacks` | adaptador YS: HMAC, nonce, despacho remoto, consulta | `Client`, `Dispenser`, `PageQuery` |
| `internal/store` | **el puerto** de persistencia + memoria | `Store`, `Order`, `Line`, `Attempt`, `Webhook`, `OrderStatus`, `AttemptOutcome`, `ErrStale`, `DueLines`, `StalledLines` |
| `internal/store/postgres` | implementación sobre Postgres | `Open`, `Store`, `Seed` |
| `internal/topology` | upsert de muebles/máquinas/combos al arrancar (Postgres tiene FK) | `Seed` |
| `internal/order` | **el dominio**: crear, cobrar, despachar, reconciliar, cerrar | `Engine`, `Draft`, `Place`, `ConfirmPayment`, `ReconcileDue`, `CloseFromCallback`, `ConfirmFromCallback`, `Gate`, `NotSellableError`, `DeriveOrderStatus` |
| `internal/gate` | adaptador `order.Gate` sobre `preflight` | `New`, `Check` |
| `internal/preflight` | la regla de disponibilidad (consulta las dos máquinas y sus productos) | `Checker`, `Request`, `Result`, `Reason` |
| `internal/httpapi` | el borde: **dos contratos separados** | `Handler` (tablet), `Callbacks` (proveedores), `Deps`, `BatidosCallbackURL` |
| `internal/payments` | puerto de devoluciones + mock que no mueve dinero | `Refunder`, `MockRefunder` |
| `internal/notify` | puerto de avisos + transporte por log | `Notifier`, `Kind`, `Event` |
| `internal/sweep` | barrido periódico que dispara `ReconcileDue` | `Runner`, `New` |
| `internal/logging` | `slog` con mensajes capitalizados en el borde | `New`, `Capitalize` |
| `internal/fakeapi` | implementación de los dos proveedores falsos + administración | `NewBatidos`, `NewSnacks`, `NewAdmin` |

## Dirección de las dependencias

Todas las flechas apuntan **hacia adentro**. Los puertos no conocen adaptadores:

```
cmd/api ──► httpapi ──► order ──► store (puerto) · dispenser (puerto) · machine · notify
                │           │                ▲            ▲
                │           └──► gate ──► preflight      │
                └──► catalog, config          batidos ───┘
                                              snacks ────┘
store/postgres ──► store (tipos) · topology · machine
```

Reglas que no se rompen:

- `order` **no** importa `batidos` ni `snacks` ni `httpapi` ni `config`.
- `catalog` no importa `dispenser`.
- `machine` no importa nada del proyecto.
- `store` todavía importa `dispenser` por `LineState` y `MachineRef` — **es deuda anotada**, no diseño.

## Flujo de una compra

| # | Paso | Endpoint | Dónde vive |
| --- | --- | --- | --- |
| 1 | menú | `GET /combos` | `httpapi/combos.go` → `catalog.Memory` |
| 2 | crear orden | `POST /orders` | `httpapi/orders.go` → `draft.go` (arma el `Draft`) → `order.Place` |
| 3 | chequeo previo | (dentro del paso 2) | `order/place.go` → `gate.Check` → `preflight.Checker` → las dos máquinas en paralelo, con un presupuesto de 8 s y dos llamadas en fila por máquina |
| 4 | pago | `POST /orders/{id}/pay` | `httpapi/orders.go` → `order.ConfirmPayment` → `dispatch.go` en paralelo; el despacho **sigue aunque la tablet corte** (`context.WithoutCancel` + un plazo por línea) |
| 5 | estado | `GET /orders/{id}` | `httpapi/orders.go` → `store.LoadOrder` → `responses.go` |
| 6 | cierre | callbacks y barrido | `httpapi/callbacks.go` y `sweep` → `order/reconcile.go` |

Quién cierra una línea:

- **snacks**: sólo el callback (`CloseFromCallback`) o el barrido, que para snacks siempre concluye duda. `CloseFromCallback` aplica lo que dice el proveedor.
- **batidos**: `ConfirmFromCallback` **no se fía** del aviso (no viene firmado): consulta `getOrderInfo` con el mismo `reconcileLine` que usa el barrido.

Quién mueve una línea `pending`:

- **`ConfirmPayment`**: es el camino normal, una vuelta de `dispatchLine` por intento, con el contexto del pedido ya separado y un plazo por línea igual al plazo de reclamo.
- **el barrido**, con dos reglas distintas: una `pending` **con plazo vencido** quedó reclamada por un despacho que no dejó resultado, y se **reconcilia** (nunca se vuelve a despachar); una `pending` **sin plazo** de una orden `paid`/`dispatching` pagada hace más de 2 minutos nunca se reclamó, y se **despacha** con el intento siguiente.

Endpoints del backend:

```
GET  /healthz
GET  /combos
POST /orders                      {combo_id, client_key?, kiosk_id?}
GET  /orders/{id}
POST /orders/{id}/pay             simulado; 403 si env != sandbox
POST /callbacks/batidos/{secret}/{orderId}
POST /callbacks/snacks/{secret}
```

## Invariantes

Si un cambio rompe alguna de estas, el cambio está mal.

1. **El precio y el SKU salen del catálogo**, nunca del cliente. El body sólo trae `combo_id`, `client_key` y `kiosk_id`.
2. **Idempotencia de la creación** por `client_key`: el lookup es la vía rápida, la clave única del store es la verdad.
3. **La línea lleva una sola familia de máquina** (`line.Machine.Kind`). `Line.MachineType` ya no existe.
4. **El estado de la orden se deriva de las líneas** (`DeriveOrderStatus`), nunca se inventa; `settleOrder` lo recalcula después de cada cambio de línea.
5. **Una línea cerrada no se reabre** (`delivered`/`failed` son terminales).
6. **La línea y la orden se escriben con versión** (`SaveLine` y `SaveOrder`, ambos condicionales, `ErrStale` si la copia es vieja). El worker, los callbacks y los dos `/pay` no se pisan.
7. **El dedupe se registra después del efecto durable.** Un fallo transitorio no quema la clave. Excepción: lo que nunca va a poder aplicarse se registra antes, para no generar reintentos eternos.
8. **Reintento sólo ante rechazo explícito.** Un timeout o una respuesta ilegible van a «en duda» y se reconcilian, nunca se reintentan a ciegas.
9. **El callback de batidos no cierra nada por sí solo**: confirma con el proveedor (R6).
10. **Los secretos de callback viajan en la ruta del path** y se comparan en tiempo constante; sin secreto configurado, 404 igual que una ruta inexistente. El path se redacta en los logs.
11. **El pago simulado sólo existe en `sandbox`.** Cualquier otro entorno lo rechaza antes de tocar el motor.
12. **Si el pago se registró y el despacho falló, la respuesta es 200 con el estado real**, no un 5xx que invite a pagar dos veces.

## Vocabularios: dónde vive cada uno

| Tipo | Vive en | Valores |
| --- | --- | --- |
| `machine.Kind` | `internal/machine` | `batidos`, `snacks` |
| `dispenser.LineState` | `internal/dispenser` | `pending`, `sent`, `brewing`, `delivered`, `failed`, `in_doubt` |
| `store.OrderStatus` | `internal/store` | `pending_payment`, `paid`, `dispatching`, `delivered`, `partial`, `failed`, `in_doubt`, `refunded`, `cancelled` |
| `store.AttemptOutcome` | `internal/store` | `ambiguous`, `accepted`, `rejected`, `error` |
| `notify.Kind` | `internal/notify` | `order_in_doubt`, `order_failed` |
| `preflight.Reason` | `internal/preflight` | `batidos_device_offline`, `snacks_sku_out_of_stock`, … |

## Si buscás X, mirá Y

| Busco | Voy a |
| --- | --- |
| por qué una línea no se reintenta | `internal/order/dispatch.go`, `classifyDispatch` |
| por qué una línea `pending` se recuperó | `internal/order/reconcile.go` (`ReconcileDue`, `resumeStalledLine`) y `StalledLines` en `internal/store` |
| cuándo se declara una duda | `internal/order/reconcile.go`, `extendReconcile` |
| cómo se elige el driver de una máquina | `internal/order/engine.go` (`machines`) y `dispatch.go` |
| por qué un callback no encontró su línea | `internal/order/callback.go`, `callbackLine` (tres reglas) |
| por qué una venta se bloqueó | `internal/preflight/checker.go` + `internal/gate` |
| la forma de la URL de aviso | `internal/httpapi/callback_url.go`, `BatidosCallbackURL` |
| el mapeo de una columna SQL | `internal/store/postgres/postgres.go` y `migrations/` |
| qué versión del esquema está aplicada | `internal/migrate` (`versions`, `missing`) y `migrations/embed.go` |
| qué hace un proveedor paso a paso | `internal/dispenser/{batidos,snacks}/dispenser.go` |
| cómo se arma el binario | `cmd/api/main.go` (orden de construcción) |

## Valores por defecto que no están en configuración

| Constante | Valor | Dónde |
| --- | --- | --- |
| plazo por línea | 2 min | `order/reconcile.go` |
| ventana de reconciliación | 2 min | `order/reconcile.go` |
| intervalo de reconciliación | 30 s | `order/reconcile.go` |
| intentos por línea | 2 | `order/dispatch.go` |
| barrido | cada 30 s, 10 líneas | `cmd/api/main.go` |
| lectura del servidor HTTP | 10 s | `cmd/api/server.go` |
| escritura del servidor HTTP | 60 s | `cmd/api/server.go` |
| conexión ociosa | 60 s | `cmd/api/server.go` |
| encabezados máximos | 64 KB | `cmd/api/server.go` |
| cuerpo máximo aceptado | 64 KB | `httpapi/respond.go` |
| presupuesto del chequeo previo | 8 s | `preflight/checker.go` |

## Qué NO está verificado

- **`main` no tiene ni un test.** Los 42 archivos viven en `archive/tests`, así que `go test ./...` responde `[no test files]` en todos los paquetes y nada frena una regresión hasta que vuelvan.
- **Nada del store de Postgres se ejecutó contra una base real.** Las migraciones `0004` y `0005` y los 14 tests de ese paquete se saltean sin `TEST_DATABASE_URL`. El contenedor está escrito y `docker compose config` lo resuelve, pero nunca se levantó.
- **El runner de migraciones tampoco se ejecutó contra un Postgres real**: `cmd/migrate` compila y los `.sql` se aplican con la misma transacción por archivo, pero el advisory lock, el `CREATE TABLE IF NOT EXISTS` y el rollback de un archivo roto sólo se probaron hasta el borde (los tests se saltean sin `TEST_DATABASE_URL`). Un volumen anterior a este cambio necesita `docker compose down -v` una vez.
- **`go test -race` no corre** en esta máquina. Casi toda la concurrencia se demuestra de forma determinista (copias viejas inyectadas); la excepción es el chequeo previo, que sí corre sus dos goroutines contra una barrera (`preflight/checker_parallel_test.go`), pero sin detector de carreras.
- **El end-to-end es manual**: `POST /orders` → `pay` → callbacks → `delivered` se corrió a mano contra los falsos, no está automatizado.
- Los webhooks de callback **no** se verificaron contra los proveedores reales: no hay credenciales ni sandbox.

## Deudas y bloqueos abiertos

| Tema | Estado |
| --- | --- |
| Decisiones de dinero (proveedor de pago, política de devolución, quién asume la pérdida) | **abiertas**, son del dueño del producto; el código ya tiene los ganchos |
| Hosting + `PUBLIC_BASE_URL` | sin definir; sin eso el callback de batidos nunca llega |
| `store` importa `dispenser` por `LineState`/`MachineRef` | deuda anotada, próximo cambio de la revisión de código |
| `machineIdentified` acepta cualquier identificador | una línea con `Kind: snacks` y `device_id` de batidos sigue siendo expresable |
| Migraciones sin volver atrás | el runner sólo aplica; no hay `down` ni checksum del archivo ya aplicado, así que editar un `.sql` que ya corrió no cambia nada y no avisa |
| Avisos repetidos | `settleOrder` avisa cada vez que la orden está en duda; no hay dedupe por `(orden, tipo)` |
| El reclamo es un plazo, no un lease | si un despacho tarda más que el plazo (2 min), el barrido reconcilia una llamada que sigue en vuelo; sin latido no hay forma de distinguir «trabado» de «lento» |
| Store en memoria más permisivo que Postgres | acepta una línea sin familia; el `CHECK` de SQL es la última red |
| Callback de snacks sin firma | el proveedor no documenta firma en el aviso; sólo lo protege el secreto de la ruta. Quien conozca esa URL puede cerrar líneas como entregadas. Mitigación posible: lista de IPs del proveedor, a pedir cuando haya credenciales reales |

## Convenciones

- **Identificadores, claves de log y JSON en inglés**; **comentarios, mensajes de error y logs en español**. No hay comentarios en el código.
- Mensajes de commit en **español**, Conventional Commits, asunto en imperativo y sin punto final.
- El registro de decisiones es `odd/tasks/combos-remotos.md` (entradas `L#`), con las specs `S#` y las restricciones de proveedor `R#`. Es memoria interna y **sí se versiona**.
- Los documentos de los proveedores y `design/` están en `.gitignore`: un clon nuevo no los tiene.
