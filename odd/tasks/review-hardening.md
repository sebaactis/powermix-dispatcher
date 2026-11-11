# Endurecimiento tras la revisión

Correcciones surgidas de la revisión de buenas prácticas, en el orden recomendado, un paso por vez.
Ramas: `fix/review-hardening` (pasos 1 y 2) y `fix/recover-pending-lines` (paso 3), ya en `main`; `fix/timeouts-and-isolation` (paso 4). Cada paso espera la decisión del usuario cuando es estructural.

## Specs

- **S1** (paso 1a, opción A aceptada): «Cambiar a `Success *bool` y rechazar solo si viene presente y es `false`.» Aplica a `Ship` y a `get` del cliente de snacks. Una respuesta sin `success` y con `code` 0 o 200 sigue aceptándose, porque la sección general de la documentación (`ys-vending.txt:351-363`) no trae ese campo.
- **S2** (paso 1b): el callback de snacks no se firma según la documentación (`ys-vending.txt:2023-2075`); corregir `docs/comentarios-codigo.md`, que afirma que nuestros callbacks usan `VerifySignature`, y anotar el riesgo.
- **S3a** (paso 2, problema A): «un rechazo con intentos pendientes deja la línea en `pending` y se guarda una sola vez. Solo el último rechazo la pasa a `failed`. Además se borra `failed → sent` de `CanLineTransition`, y `failed` queda terminal, como dice la invariante 5.»
- **S3b** (paso 2, opción B1): «`store.Order` gana `Version int64` y una migración nueva `0005` agrega `orders.version`. `SaveOrder` escribe `WHERE id = $1 AND version = $2` y devuelve `ErrStale` si alguien escribió antes. `settleOrder` recarga y reintenta una vez ante `ErrStale`.» `ConfirmPayment` escribe con versión: el segundo `/pay` concurrente recibe `ErrStale` y termina sin despachar.
- **S4** (paso 3, opción R1 aceptada): recuperar las líneas `pending` trabadas. Casos: **C1** el proceso cayó entre registrar el intento y guardar su resultado («no: el pedido pudo llegar a la máquina (invariante 8)»); **C2** rechazo con intentos pendientes y caída antes del siguiente («sí: el último intento fue rechazado de forma explícita»); **C3** el guardado a `dispatching` chocó con otro escritor («sí: nadie llamó a la máquina»). Regla del usuario aceptada: «Antes de `AppendAttempt`, `dispatchLine` guarda la línea con `DeadlineAt` (plazo de 2 min). Una línea `pending` con plazo significa «alguien la llamó y no sabemos cómo terminó».» Con plazo vencido «el barrido no la despacha de nuevo. La **reconcilia**»; sin plazo, de una orden pagada hace más de 2 minutos, «el barrido llama a `dispatchLine` con el número de intento siguiente»; «una orden que quedó `paid` pasa a `dispatching` cuando el barrido la toma (C3)».
- **S4r** (paso 3, agregado aceptado): «Si lo guardamos siempre que venga, C1 en batidos puede llegar a resolverse como entregado o fallido»: `dispatchLine` guarda `result.ProviderRef` en `line.Ref` aunque el intento no se haya aceptado, cuando viene no vacío.
- **S5a** (paso 4, aceptado): timeouts del `http.Server`: «`ReadTimeout` 10 s, `WriteTimeout` 60 s, `IdleTimeout` 60 s, `MaxHeaderBytes` 64 KB», con la configuración extraída a `newServer` para poder probarla.
- **S5b** (paso 4, aceptado): «Usar `context.WithoutCancel(ctx)`» en el despacho de `ConfirmPayment` y «darle a cada línea su propio plazo con `context.WithTimeout`, igual al plazo de reclamo (2 min)».
- **S5c** (paso 4, aceptado): «`sync.WaitGroup` de la biblioteca estándar, una goroutine por máquina, con un plazo total de 8 s». «Las dos llamadas de cada máquina siguen siendo secuenciales».
- **S6** (paso 5): versionado de migraciones y tests de Postgres y `-race`. Pendiente de decisión.

## Tasks

| ID | Specs | Ruta | Commit |
| --- | --- | --- | --- |
| T1 | S1 | inline | `415fdb1` hecho |
| T2 | S2 | inline | ver commit `docs(snacks)` hecho |
| T3a | S3a | inline + verify | `646320a` hecho, verify PASS |
| T3b | S3b | worker + verify | `7a1f963` hecho, verify PASS (Postgres sin ejecutar) |
| T4a | S4r | worker + verify | `9833a3d` hecho, verify PASS |
| T4b | S4 | worker + verify | `333e55a` hecho, verify PASS (Postgres sin ejecutar) |
| T5a | S5a | worker | ver commit `fix(api): agrega timeouts` |
| T5b | S5b | worker | ver commit `fix(order): el despacho sobrevive` |
| T5c | S5c | worker | ver commit `fix(preflight)` |
| T5v | S5a-S5c | verify | pendiente |
| T6 | S6 | pendiente | pendiente |

## Corte de entrega

- **Porción 1** (pasos 1 y 2): `415fdb1`, `eb34cbc`, `646320a`, `7a1f963`. ~830 líneas, por encima de las ~400 recomendadas por revisión, porque 2B cambia el contrato del store en las dos implementaciones a la vez. Candidata a un PR propio antes del paso 3.

- **Porción 2** (paso 3): `9833a3d`, `333e55a` en `fix/recover-pending-lines`. ~970 líneas, la mayor parte tests (casos C1, C2, C3 y guardias). Candidata a su propio PR.

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
- **L6**: «Si Dale.» (paso 3: R1 + agregado de batidos). R2 (recuperar sólo al arrancar) y R3 (outbox) descartadas.
- **L7** (diseño de T4a/T4b, decidido por el orquestador dentro de R1):
  1. T4a: en `dispatchLine`, `line.Ref = result.ProviderRef` cuando `result.ProviderRef != ""`, cualquiera sea el resultado; un intento aceptado sigue limpiando `LastError`.
  2. Reclamo: en cada vuelta de `dispatchLine`, **antes** de `AppendAttempt`, se guarda la línea (`SaveLine`, con versión) con `DeadlineAt = now + deadline` y `ReconcileUntil = now + deadline + reconcileWindow`; estado sigue `pending`. `ErrStale` ahí → `return nil` (otro escritor la tomó). Si después `AppendAttempt` da `ErrAlreadyExists` → `return nil` como hoy (la línea ya quedó reclamada, así que si ese otro escritor muere, el barrido la reconcilia).
  3. Después del despacho: aceptado y en duda fijan sus plazos como hoy; un rechazo con intentos pendientes guarda la línea `pending`, `Attempts=number`, con `DeadlineAt` y `ReconcileUntil` en cero (sin reclamo); el rechazo final (`failed`) deja los dos plazos en cero.
  4. Store: `DueLines` incluye `pending` además de `sent|brewing|in_doubt` (misma condición de plazo vencido). Método nuevo en el puerto `StalledLines(ctx, paidBefore time.Time, limit int) ([]Line, error)`: líneas `pending` sin `DeadlineAt` cuya orden está `paid` o `dispatching` y tiene `paid_at <= paidBefore`, ordenadas por `paid_at`, id. Postgres con `JOIN orders`. Memoria con la misma regla.
  5. Barrido (`ReconcileDue`): las líneas vencidas se reconcilian como hoy, también las `pending` (nunca se vuelven a despachar). Después pide `StalledLines(now - deadline, limit)` y por cada una: `refreshOrderStatus` de su orden (saca a `paid` hacia `dispatching`), luego `dispatchLine` con la línea recargada. Las órdenes tocadas se asientan con `settleOrder` al final, como hoy.
- **L8** (verificación de T4a/T4b): PASS en todo salvo Postgres en ejecución (sin base). Riesgo bajo anotado: una línea `pending` sin reclamo con `Attempts >= maxAttempts` volvería en cada barrido ocupando cupo; ningún camino normal la produce (sólo bajar `maxAttempts` por configuración).
- **L9**: «Si. Lo mismo antes de seguir.» (paso 3 fusionado y subido: `f036d82`).
- **L10**: «Dale.» (paso 4 con los valores propuestos y `sync.WaitGroup`).
