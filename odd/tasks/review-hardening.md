# Endurecimiento tras la revisión

Correcciones surgidas de la revisión de buenas prácticas, en el orden recomendado, un paso por vez.
Rama: `fix/review-hardening`. Cada paso espera la decisión del usuario cuando es estructural.

## Specs

- **S1** (paso 1a, opción A aceptada): «Cambiar a `Success *bool` y rechazar solo si viene presente y es `false`.» Aplica a `Ship` y a `get` del cliente de snacks. Una respuesta sin `success` y con `code` 0 o 200 sigue aceptándose, porque la sección general de la documentación (`ys-vending.txt:351-363`) no trae ese campo.
- **S2** (paso 1b): el callback de snacks no se firma según la documentación (`ys-vending.txt:2023-2075`); corregir `docs/comentarios-codigo.md`, que afirma que nuestros callbacks usan `VerifySignature`, y anotar el riesgo.
- **S3a** (paso 2, problema A): «un rechazo con intentos pendientes deja la línea en `pending` y se guarda una sola vez. Solo el último rechazo la pasa a `failed`. Además se borra `failed → sent` de `CanLineTransition`, y `failed` queda terminal, como dice la invariante 5.»
- **S3b** (paso 2, opción B1): «`store.Order` gana `Version int64` y una migración nueva `0005` agrega `orders.version`. `SaveOrder` escribe `WHERE id = $1 AND version = $2` y devuelve `ErrStale` si alguien escribió antes. `settleOrder` recarga y reintenta una vez ante `ErrStale`.» `ConfirmPayment` escribe con versión: el segundo `/pay` concurrente recibe `ErrStale` y termina sin despachar.
- **S4** (paso 3): recuperar líneas `pending` con intento ya registrado. Pendiente de decisión. Incluye dos casos que dejó a la vista la verificación del paso 2: una línea reintentada que queda `pending` con `Attempts=1` si el proceso cae, y una orden que queda `paid` con líneas `pending` si el guardado a `dispatching` sale `ErrStale`.
- **S5** (paso 4): timeouts del servidor, despacho con `context.WithoutCancel`, chequeo previo en paralelo. Pendiente de decisión.
- **S6** (paso 5): versionado de migraciones y tests de Postgres y `-race`. Pendiente de decisión.

## Tasks

| ID | Specs | Ruta | Commit |
| --- | --- | --- | --- |
| T1 | S1 | inline | `415fdb1` hecho |
| T2 | S2 | inline | ver commit `docs(snacks)` hecho |
| T3a | S3a | inline + verify | `646320a` hecho, verify PASS |
| T3b | S3b | worker + verify | `7a1f963` hecho, verify PASS (Postgres sin ejecutar) |
| T4 | S4 | pendiente | pendiente |
| T5 | S5 | pendiente | pendiente |
| T6 | S6 | pendiente | pendiente |

## Corte de entrega

- **Porción 1** (pasos 1 y 2): `415fdb1`, `eb34cbc`, `646320a`, `7a1f963`. ~830 líneas, por encima de las ~400 recomendadas por revisión, porque 2B cambia el contrato del store en las dos implementaciones a la vez. Candidata a un PR propio antes del paso 3.

## Log

- **L1**: «Vayamos por tu orden recomendado, paso a paso. Ya sabes como comportarte segun mi agent.md»
- **L2**: la documentación del proveedor muestra dos formas de respuesta: con `success` (endpoints concretos) y sin él (sección general). El callback no lleva firma documentada. Dos hallazgos «alta» de la revisión bajan: `success` a media, la firma del callback pasa a corrección de documentación.
- **L3**: «Ok. Implementa la opcion A.»
- **L4**: «Listo. Vamos con ambas opciones recomendadas tuyas.» (A con `pending` y B1)
- **L5** (diseño de T3b, decidido por el orquestador dentro de B1):
  1. Contrato: `SaveOrder(ctx, order) (Order, error)`, igual que `SaveLine`. Devuelve la orden recibida con `Version` incrementada; las líneas no se tocan ni se releen (sigue siendo «solo cabecera»). `ErrStale` si la versión guardada no coincide; `ErrNotFound` si la orden no existe. El mensaje de `ErrStale` pasa a «el registro cambió desde que se leyó».
  2. Postgres: `UPDATE ... SET ..., version = version + 1 WHERE id = $1 AND version = $11 RETURNING version`; sin filas, distinguir con un `orderExists` análogo a `lineExists`. `scanOrder` y `orderColumns` incluyen `version`. Migración `migrations/0005_order_version.sql`: `ALTER TABLE orders ADD COLUMN IF NOT EXISTS version bigint NOT NULL DEFAULT 0;` dentro de `BEGIN/COMMIT`.
  3. Memoria: compara `Version`, incrementa, guarda; `CreateOrder` conserva la versión recibida (0).
  4. Dominio: se extrae `refreshOrderStatus(ctx, orderID) (store.Order, error)` en `internal/order/settle.go`: carga, `AdvanceOrder`, guarda sólo si cambió; ante `ErrStale` recarga y reintenta **una** vez; un segundo `ErrStale` se devuelve como error. `settleOrder` = `refreshOrderStatus` + `notifyOrder`. La cola de `ConfirmPayment` (`payment.go:58-73`) usa `refreshOrderStatus` (sin aviso, como hoy).
  5. `ConfirmPayment`: el guardado a `paid` usa la versión cargada; `ErrStale` ahí devuelve `nil` sin despachar (otro `/pay` ganó). El guardado a `dispatching` parte de la orden devuelta por el primero; un `ErrStale` ahí es error y no se despacha.
