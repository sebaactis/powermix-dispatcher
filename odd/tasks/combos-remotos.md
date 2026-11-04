# Combos remotos Powermix

App de tablet en un mueble con dos máquinas Powermix (batidos y snacks) para comprar combos y disparar la entrega en ambas a la vez.
Estado: especificación y planificación. Sin código todavía.

> Este documento es memoria interna del proyecto y **sí se versiona**: es el único registro de por qué el código es como es. `design/` sigue fuera de git.

## Cómo retomar

Raíz del proyecto (tiene un espacio en el nombre, citar siempre):

```
D:/Descargas/Seba/Proyects/GO/Powermix BI-MAQUINA/compras-remotas
```

Estado: **173 tests en verde, 12 paquetes, árbol limpio**. Sin dependencias externas.

Hecho: esqueleto, firmas de las dos APIs, servidores falsos, catálogo, store en memoria, dominio de estados, motor con despacho en paralelo, reintento del fallido, worker de reconciliación, wiring en `cmd/api` con barrido cada 30 s, e idempotencia de la creación de orden.

En curso: **T11c-2, los handlers HTTP** en un paquete `internal/httpapi` nuevo.
- `GET /combos` desde el catálogo.
- `POST /orders`: la idempotencia ya está resuelta en el store y en `Place`; falta el handler.
- `GET /orders/{id}` para el estado en vivo.
- `POST /orders/{id}/pay`: pago simulado, **no debe registrarse si `env=production`**.

Pendiente después: callbacks de las máquinas (T10), pagos reales (T7, bloqueado por la decisión del proveedor), y la app Android.

**Deuda que no hay que olvidar:** `internal/preflight` existe y está probado, pero **el motor todavía no lo llama**. La puerta de disponibilidad nunca se conectó a `Place`, así que hoy se puede crear una orden sobre una máquina caída. Está diseñada la interfaz `Gate` pero no implementada.

**Dos verificaciones que nunca se corrieron:** `go test -race` (no hay compilador de C en la máquina) y las tres migraciones contra un PostgreSQL real (no hay `psql` ni daemon de Docker).

## Specs

Cada spec cita literalmente lo que pidió el dueño del producto. No se agrega nada que no haya sido pedido.

**S1 — El producto es una app Android en una tablet, dentro de un mueble con las dos máquinas.**
> "Vamos a tener un mueble, con las dos maquinas,y una tablet en el medio, que nos va a permitir comprar en una app dentro de ella."

**S2 — Una compra dispara los productos en las dos máquinas, cada uno en la suya.**
> "Si yo compre un combo de batido + snack, en la Batidos Powermix debe disparar el batido, y en la Snack Powermix, debe disparar el snack."

**S3 — El menú son varios combos fijos.** Un combo es una combinación cerrada, no armable por el cliente.
> "Varios combos fijos en el menú"

**S4 — Las credenciales de cada API las entrega cada proveedor.**
> "Cada proveedor de cad amaquina nos va a brindar las credenciales para pegarle a sus respectivas APIs."

**S5 — El cobro es con QR / billetera digital. El proveedor de pagos todavía no está elegido.**
> "QR / billetera digital"
> "Todavía no está definido"

**S6 — El código de canje del batido se genera en cada compra.**
> "Generar la campaña en cada compra"

**S7 — Si un producto falla, se reintenta automáticamente el que falló; el otro no se toca.**
> "Reintento automático del fallido"

**S8 — Las máquinas se disparan en el instante en que se confirma el pago.**
> "Inmediato al confirmar pago"

**S9 — El sistema sirve a varios muebles / sucursales desde el primer día.**
> "Varios muebles / sucursales"

**S10 — Si la tablet o el backend no tienen conexión, la venta se bloquea.**
> "Bloquear la venta"

**S11 — Backend en Go, tablet en Kotlin.**
> "Go (backend) + Kotlin (tablet)"

**S12 — Por ahora no hay panel de administración de combos; se cargan por configuración.**
> "Por ahora sin panel, despues vemos de armar un CRUD."

**S13 — El hosting del backend todavía no está decidido.**
> "Sin definir aun"

**S14 — Este documento es la guía del proyecto: qué está hecho y qué falta.**
> "Escribamos todo el spec y el plan en un documento para guiarnos desde ahi, y ver que vamos completando y que va faltando."

**S15 — Hace falta una lámina visual simple del flujo, para mostrarle al dueño del sistema Powermix, que no es técnico.**
> "te pido si me mandas un diagrama simple de como queda el flujo (en canvas por ejuemplo) asi le paso una captura a la persona dueña del sistema de powermix. Pensa que no es una persona tecnica, mandamelo tipo presentacion."

**S16 — Se espera un máximo de 2 minutos por una respuesta antes de declarar fallida una línea; si el resultado es incierto, se reconcilia contra la API hasta obtener una respuesta, con timeout.**
> "2 minutos"
> "Reconciliar hasta tener una respuesta con un timeout."

**S17 — La política de reembolso queda abierta: se implementa como una pieza intercambiable con una implementación mock por ahora. El sistema no mueve dinero real hasta que se defina.**
> "Se puede definir despues, y dejar un mock mientras?"

**S18 — Las excepciones se notifican de forma automática; el canal concreto queda a definir.**
> "Auto del sistema, pero podemos pensar una forma de ser notificados automaticamente."

### Restricciones que imponen las APIs (no son requisitos nuevos, son condiciones del proveedor)

- **R1** Todo pasa por un backend propio: ninguna de las dos documentaciones permite llamar a las máquinas desde la tablet, y ambas prohíben exponer el secreto en el cliente.
- **R2** El batido no se pide por producto: se pide canjeando un **código** (兑换码) que antes hay que emitir creando una **campaña** (兑换活动). Son dos llamadas Batidos por bebida.
- **R3** Las fechas de la campaña de batidos son strings `yyyy-MM-dd HH:mm:ss` en **UTC+8**. Desde Argentina son 11 horas de diferencia: se construyen siempre en UTC+8 (zona fija, porque China no tiene horario de verano y así no se depende de tzdata).
- **R4** La API de snacks se firma con `Base64(HMAC-SHA256(AppSecret, "MÉTODO\nRUTA\ntimestamp\nnonce"))`, admite ±5 minutos de desfase de reloj y rechaza `nonce` repetido durante 10 minutos.
- **R5** "Aceptado" no es "entregado". En Batidos `data: true` es "pedido tomado"; en Snacks el 200 es "despacho encolado". Solo el callback confirma la entrega real.
- **R6** El callback de canje de Batidos **no viene firmado**: hay que autenticarlo por ruta secreta y confirmar el resultado con `getOrderInfo` antes de cerrar la línea.
- **R7** Ninguna de las dos APIs cobra: el pago es responsabilidad nuestra con un proveedor externo de QR.

## Tasks

Ruta: `inline` = la hace el orquestador; `worker` = delegable a un subagente. Commit: el repositorio todavía no es git, así que no hay commits; se revisa por diff.

| ID | Specs | Tarea | Ruta | Commit |
| --- | --- | --- | --- | --- |
| T1 | S9, S11, S13 | Esqueleto del backend en Go: configuración por mueble, entorno sandbox/producción, healthcheck | inline | — |
| T2 | S4, S6, R2, R3 | Driver Batidos: firma MD5, fechas en Asia/Shanghai, crear campaña, `produceCoffee`, `getOrderInfo` | inline | — |
| T3 | S4, R4 | Driver Snacks: firma HMAC-SHA256, nonce/timestamp por intento, `remote/shipment` | inline | — |
| T4 | S4 | Servidor falso de ambas APIs (mocks locales) + vectores de firma como test | inline | — |
| T5 | S3, S9 | Modelo de datos y migraciones: kiosk, machine, combo, order, order_line, dispatch_attempt, webhook_event, catalog_snapshot | inline | — |
| T6 | S8, S10 | Catálogo y chequeo previo: sólo se genera el QR si ambas máquinas están sanas y ambos productos disponibles | inline | — |
| T7 | S5, S17 | Capa de pagos abstracta (interfaz) + webhook idempotente + política de reembolso como estrategia con implementación mock | inline | — |
| T8 | S2, S8 | Motor de orden: pago confirmado → despacho en paralelo de las dos líneas | inline | — |
| T9 | S7, S16, R5, R6 | Reintentos con backoff, timeout de 2 minutos y reconciliación de intentos inciertos | inline | — |
| T10 | S2, R6 | Callbacks de ambas máquinas con deduplicación y cierre por línea | inline | — |
| T11 | S1 | API para la tablet: catálogo, crear orden, estado en vivo | inline | — |
| T12 | S1 | App Android en modo kiosco: elegir combo, QR, progreso de las dos máquinas, resultado | inline | — |
| T13 | S1–S18 | Pruebas end-to-end con mocks + manual de operación del mueble | inline | — |
| T14 | S18 | Notificador de excepciones automático, con canal de transporte intercambiable | inline | — |

## Estado

- [x] **T1** Esqueleto Go — `cmd/api` con `/healthz`, topología validada al arrancar (un kiosco sin una máquina de cada tipo no levanta)
- [x] **T4** Servidores falsos + vectores de firma — `internal/fakeapi`, 4 vectores calculados con `openssl`
- [x] **T5** Modelo de datos — `migrations/0001_init.sql`, aplicada en Postgres 16 real
- [x] **T2** Cliente de Batidos — campaña, canje y consulta; 6 tests
- [x] **T3** Cliente de Snacks — despacho remoto con nonce por intento; 7 tests
- [x] **T6** Catálogo y chequeo previo — lecturas de dispositivo y producto en las dos APIs (GET firmado) más el paquete `internal/preflight` con la regla de decisión; 33 tests nuevos. Queda pendiente el adaptador por máquina del puerto (`Dispenser.Check`), que sigue sin implementación.
- [ ] T7 Pagos + reembolso mock
- [ ] T8 Motor de orden
- [ ] T9 Reintentos y reconciliación
- [ ] T10 Callbacks
- [ ] T11 API para la tablet
- [ ] T12 App Android kiosco
- [ ] T13 Pruebas end-to-end + manual
- [ ] T14 Notificador

## Huecos abiertos

Lo que todavía no se puede programar sin una respuesta. Nada de esto se asume.

### Bloqueantes con Batidos
1. Dominio real de la API (la documentación sólo muestra el del panel de gestión).
2. ¿El `key-md5` va en minúsculas? ¿Es MD5 hex estándar?
3. ¿`deviceNo` es obligatorio en `produceCoffee`? La tabla dice sí, el ejemplo lo omite.
4. ¿Se pueden crear códigos en tiempo real, uno por venta, sin intervención del panel? ¿Hay límite?
5. ¿Un código es de un solo uso? ¿Caduca? ¿Es canjeable en el instante en que se crea?
6. ¿`data: true` significa aceptado o preparado? ¿La llamada es idempotente por `code`?
7. ¿El callback reintenta? ¿Cuántas veces? ¿Cómo se autentica?
8. ¿Existe entorno de prueba o sandbox? No está documentado.

### Bloqueantes con Snacks
1. JSON exacto de `/open/v7/device/remote/shipment`: la tabla del PDF está desalineada.
2. ¿`accessKey` va en el body además del header?
3. ¿`PaymentOrderId` es clave de idempotencia? ¿Se puede reintentar con el mismo valor?
4. ¿`retailPrice` liquida algo o es informativo? ¿Hace falta saldo o contrato previo para despachar?
5. ¿`v6` o `v7` para Argentina? La documentación dice v7 fuera de China continental.
6. ¿El callback de resultado se autentica de alguna forma?

### Política de errores y de dinero (a decidir)

Esto es lo que hay que responder antes de escribir el motor de órdenes. No es un detalle de implementación: define qué se le promete al cliente y qué se le cobra al negocio.

**Las cinco formas en que una compra se rompe:**

| # | Situación | ¿Sabemos si el producto salió? | Decisión necesaria |
| --- | --- | --- | --- |
| E1 | Antes de cobrar: máquina fuera de línea, producto dado de baja, sin stock, o proveedor caído | Sí: no salió | Ya decidido (S10): no se genera el QR, costo cero |
| E2 | Pago aprobado y el despacho es **rechazado** con error explícito | Sí: no salió | Cuántos reintentos y con qué (en batidos exige código nuevo) |
| E3 | La máquina **acepta** y después informa fallo por callback (`status 0` / `ShipmentStatus 0`) | Sí: falló, con motivo | Qué se devuelve y en cuánto tiempo |
| E4 | La llamada se acepta y **nunca llega respuesta** | **No** | Prohibido reintentar a ciegas: reconciliar y, si no hay dato, no tocar el dinero solo |
| E5 | El reintento ya se declaró fallido y la máquina entrega tarde | Sí, pero tarde | Se registra y se asume la pérdida; no se corrige sola |

**Regla transversal:** un mismo pago, un webhook repetido o dos reintentos solapados nunca pueden producir dos entregas. Todo intento lleva clave de idempotencia.

**Decisiones tomadas:**
1. **E3 — qué se devuelve:** queda abierto (S17). Se construye la pieza intercambiable con un mock; el sistema registra el hecho pero no mueve dinero hasta que se defina.
2. **Tiempo de espera:** 2 minutos antes de declarar fallida una línea (S16).
3. **E4 — caso incierto:** reconciliar contra la API hasta obtener respuesta, con timeout (S16). Nunca se reintenta a ciegas.
4. **Avisos:** automáticos (S18), canal a definir.

**Sigue abierto:** qué se devuelve y con qué criterio (S17), y por qué canal se avisa (S18). Ninguno de los dos bloquea el motor de órdenes.

### Decisiones de negocio pendientes
1. Proveedor de pagos QR (S5).
2. Hosting del backend (S13) — obligatorio que sea alcanzable por HTTPS público para recibir webhooks.
3. Qué pasa con un combo cuando la tablet se corta en medio del despacho: hoy la venta se bloquea (S10), pero el despacho ya iniciado sigue su curso.

## Log

**L1 — Pedido original del dueño del producto:**
> Hola rey.
>
> En este repositorio, tenemos dos documentos:
>
> 1) @Batidos-Remoto-API.md. Esta es la documentacion de la API de Batidos Powermix = maquina de batidos de nuestra marca Powermix.
> 2) @"YS Vending Management Platform API Document V1.0.1.pdf". Esta es la documentacion de la API de la nueva maquina de Snacks Powermix.
>
> La idea de hoy, es que,  basados ewn esta docuemntacion, queremos armar una app android, que lo que haga, sea poder comprar los combos que incluyan productos de estas dos maquinas a la vez.
>
> Por ejemplo:
>
> Vamos a tener un mueble, con las dos maquinas,y una tablet en el medio, que nos va a permitir comprar en una app dentro de ella. Con la compra, debemos disparar en ambas maquinas, los productos pedidos. Si yo compre un combo de batido + snack, en la Batidos Powermix debe disparar el batido, y en la Snack Powermix, debe disparar el snack.
>
> Por favor, analizá en profundidad la documentacion, y debatamos como podemos armarla. No asumas nada, y tambien, yo necesito que me expliques la documentacion asi entiendo el proyecto que vamos a programar.
>
> Y por favor, vamos de a poco, no me explotes de informacion, sino quei vamos entendiendo las cosas juntos en bloques.

**L2 — Lectura de las dos documentaciones (evidencia).** `Batidos-Remoto-API.md`: Merchant Open API v2.0, 2877 líneas. Endpoints reales: `/coffee/newThird/active/v2/submit`, `/active/updateState`, `/order/getOrderInfo`, `/order/produceCoffee`, `/order/getOrderList`, `/device/page`, `/device/detail/{deviceId}`, `/device/deviceTypeList`, `/goods/*`, `/material/*`, `/replenish/*`, `/resource/*`. No existe endpoint de creación de orden ni de cobro. `YS Vending Management Platform API Document V1.0.1.pdf`: 49 páginas, extraído a `ys-vending.txt`. v1.0.1 (2026-09-04) agrega el despacho remoto: `/open/v7/device/remote/shipment` y su callback por sub-orden.

**L3 — Decisión: credenciales propias por proveedor.** Respuesta del dueño: "Cada proveedor de cad amaquina nos va a brindar las credenciales para pegarle a sus respectivas APIs." → S4.

**L4 — Decisión: cobro con QR / billetera digital, proveedor aún no definido.** → S5. Consecuencia: la compra nace en `PAGO_PENDIENTE` y el webhook de pago es el único disparador.

**L5 — Decisión: varios combos fijos en el menú.** → S3. Habilita `exchangeType: 1` (producto único) en la campaña de canje.

**L6 — Decisión: generar la campaña de canje en cada compra.** → S6. Riesgo aceptado: son dos llamadas Batidos por bebida, y si la emisión del código falla después del cobro, la única salida honesta es el reembolso de esa línea.

**L7 — Decisión: reintento automático del producto que falló.** → S7. Consecuencia técnica: en Batidos el reintento implica **un código nuevo**, porque el código anterior probablemente quedó consumido.

**L8 — Decisiones operativas: disparo inmediato al confirmar el pago, multi-sede, y bloquear la venta sin conexión.** → S8, S9, S10. Consecuencia combinada: hace falta un chequeo previo de salud de ambas máquinas antes de generar el QR.

**L9 — Decisión de stack: Go + Kotlin.** → S11.

**L10 — Decisión: sin panel de administración por ahora.** → S12.

**L11 — Hosting sin definir.** → S13. No bloquea el desarrollo: las URLs base son configuración.

**L12 — Pedido de este documento guía.** → S14.

**L13 — Pedido de la lámina visual para el dueño de Powermix.** → S15. Se implementa en `design/flujo-combos/`.

**L14 — Lámina entregada.** `design/flujo-combos/01-como-funciona.html` (1600×900, una diapositiva, «Un combo, dos máquinas») más su captura `01-como-funciona.png`. Sin alternativas: es una única pieza explicativa, no una exploración de diseño. Contenido verificado contra S1–S10: el flujo describe elección → pago QR → disparo simultáneo en ambas máquinas → retiro, y las tres garantías del pie cubren reintento automático (S7), bloqueo de venta sin conexión (S10) y trazabilidad (R5).

**L15 — Pedido de la lámina interna y de la política de errores.**
> "El canvas: agregale el flujo como hace por detras."
> "Y preguntas que hay que responder: como manejamos errores contra los sistemas de la maquina, como vamos ad evol.ver la palta o resolver esas situaciones."

Segunda lámina creada: `design/flujo-combos/02-por-detras.html` (1600×900), con las tres etapas (se cobra / se dispara / se resuelve), los endpoints reales de cada máquina y su tipo de firma. La política de errores quedó abierta en la sección «Huecos abiertos», con las cinco formas de fallo E1–E5 y las cuatro decisiones pendientes.

**L16 — Decisiones sobre errores y dinero.** → S16, S17, S18.
> "Se puede definir despues, y dejar un mock mientras?"
> "2 minutos"
> "Reconciliar hasta tener una respuesta con un timeout."
> "Auto del sistema, pero podemos pensar una forma de ser notificados automaticamente."

Consecuencia: el reembolso y las notificaciones quedan detrás de dos interfaces (`RefundPolicy`, `Notifier`) con implementación mock, de modo que el motor de órdenes no queda bloqueado por decisiones de negocio todavía abiertas. Las tareas T7, T9 se ajustan y se agrega T14.

**L17 — T1, T4 y T5 implementadas.** Autorizado con "Dale vamos con eso". No hay repositorio git, así que no hay commits: la evidencia es el diff y las comprobaciones de abajo.

Archivos creados: `go.mod`, `cmd/api/main.go`, `cmd/fakeapi/main.go`, `internal/config/config.go`, `internal/dispenser/dispenser.go`, `internal/dispenser/batidos/sign.go`, `internal/dispenser/snacks/sign.go`, `internal/fakeapi/batidos.go`, `internal/fakeapi/snacks.go`, `internal/payments/refund.go`, `internal/notify/notify.go`, `migrations/0001_init.sql`, `config.example.json`, `.gitignore`, `README.md`, más 6 archivos de test.

Comprobaciones observadas:
- `gofmt -l .` → sin salida. `go vet ./...` → sin salida. `go build ./...` → ok.
- `go test ./...` → 6 paquetes en verde, incluidos los 4 vectores de firma calculados con `openssl` (no con Go), el cruce de medianoche en `CampaignTime`, el rechazo de código de canje reusado, el rechazo de `nonce` repetido y el rechazo de firma inválida.
- Arranque real: `go run ./cmd/api -config config.example.json` → `GET /healthz` devolvió `{"env":"sandbox","kiosks":1,"status":"ok"}`.
- Migración aplicada en `postgres:16` real: 10 tablas creadas, seed de kiosco/máquinas/combo/orden/líneas correcto, y rechazo efectivo de máquina duplicada por kiosco, estado de línea desconocido y máquina de batidos sin los identificadores de su API.

Desviaciones declaradas: la delegación a un worker no fue posible (no hay repositorio git, la preparación del destino falla), así que el tramo se hizo inline; y sin repositorio no hay commits por unidad de trabajo.

Riesgo residual: las firmas están validadas contra vectores propios, no contra el proveedor real. El primer llamado con credenciales verdaderas es la única prueba definitiva, y depende de los huecos bloqueantes.

**L18 — Convención de idioma del código.** Pedido del dueño del producto:
> "Comentarios pasalos a español."
> "Respuestas pasalas a español, para que en los logs sea claro para mi buscar."

Regla adoptada: comentarios, mensajes de log, errores y textos propios se escriben en español. Se mantienen en inglés los identificadores de código, los nombres de campo JSON de los proveedores (son contratos ajenos) y las claves del endpoint `/healthz` (contrato legible por máquinas). El literal `操作成功` de los servidores falsos se conserva porque es exactamente lo que responde el proveedor.

Evidencia: `gofmt -l .` sin salida, `go vet ./...` sin salida, `go test ./...` con los 6 paquetes en verde, y una línea de log real: `msg="API escuchando" direccion=127.0.0.1:18081 entorno=sandbox muebles=1`. La migración escrita de nuevo con comentarios en español no se pudo reaplicar en Postgres porque Docker Desktop dejó de estar disponible; la estructura de sentencias se verificó por inspección (10 tablas y 4 índices presentes).

**L19 — Corrección del alcance de L18.** Precisión del dueño del producto:
> "Esperá. Solo comentarios y mensajes de logs. Variables y campos ingles."

En L18 se habían traducido también las claves de los campos de log (`direccion`, `entorno`, `muebles`, `orden`, `linea`, `importe_centavos`, `motivo`, `tipo`, `detalle`), que era justamente lo que no correspondía. Regla definitiva:

- **En español:** comentarios del código y el texto de los mensajes de log (`msg="API escuchando"`) y de los errores.
- **En inglés:** todo identificador y toda clave de campo, incluidas las claves estructuradas de log (`addr`, `env`, `kiosks`, `order_id`, `line_id`, `amount_cents`, `reason`, `kind`, `detail`), los nombres de campo JSON de los proveedores y las claves de `/healthz`.

Verificado: `grep` de las claves en español devuelve vacío, `gofmt` y `vet` sin salida, `go test ./...` en verde, y una línea de log real con el mensaje en español y las claves en inglés.

**L20 — Renombrado al vocabulario del dominio.** Pedido del dueño del producto:
> "ourvend vamos a cambiarlo por snacks, para que nosotros lo identifiquemos mas rapido. GS por batidos"

Se renombró el código al nombre de la máquina, no al del proveedor: los valores `"batidos"` y `"snacks"` ya eran el vocabulario de `MachineType` y de las columnas SQL, así que ahora el código coincide con el dominio y no con el apellido comercial del vendedor.

Renombrado: paquetes y directorios (`dispenser/batidos`, `dispenser/snacks`), archivos (`fakeapi/batidos.go`, `fakeapi/snacks.go`), tipos (`fakeapi.Batidos`, `fakeapi.Snacks`), campos de configuración (`BatidosBaseURL`, `SnacksAccessKey`, ...), claves del archivo de configuración (`batidos`, `snacks`), variables de entorno (`BATIDOS_KEY`, `BATIDOS_SECRET`, `SNACKS_ACCESS_KEY`, `SNACKS_APP_SECRET`), columnas SQL (`batidos_device_id`, `snacks_mid`) y nombres de test.

No se renombró, porque son contratos del proveedor: las rutas HTTP (`/coffee/newThird/...`, `/open/v7/...`), los nombres de campo de sus JSON (`deviceNo`, `PaymentOrderId`, `key-md5`) y **el host de producción `ysopenapi.ourvend.com`**.

**Incidente durante el renombrado.** El reemplazo masivo también cambió el host de producción dentro de la constante y del literal del test, dejándolo como `ysopenapi.snacks.com`. El test siguió pasando porque comparaba contra el mismo valor equivocado: se había convertido en una tautología. Se detectó por revisión del diff, no por la suite. Corregido: el host volvió al valor documentado, con un comentario que prohíbe renombrarlo y un test cuyo mensaje de fallo nombra "el host de producción documentado".

Verificación: `gofmt -l .` sin salida, `go vet ./...` sin salida, `go build ./...` ok, `go test ./...` con los 6 paquetes en verde, `grep` de `ourvend|gs|GS` en código/configuración sin resultados salvo el host legítimo, y arranque real con `config.example.json` usando las claves nuevas.

Nota de mantenimiento: el vocabulario de este documento también se normalizó en las secciones vivas y en las entradas anteriores del Log, para que buscar "batidos" o "snacks" encuentre todo. Ninguna cita textual del dueño del producto fue alterada.

**L21 — Mayúscula inicial en los logs.** Pedido del dueño del producto:
> "me gustaria abordar que los logs empiezen con letra mayuscula en su primer letra, se que los logs en go /el fmt.error, no lo permite, como podemos hacerlo?"

Corrección del supuesto: Go no prohíbe mayúsculas en `fmt.Errorf`. Lo que existe es la convención ST1005, que pide minúscula inicial porque los errores se envuelven dentro de otras frases: `"no se pudo abrir: archivo ausente"` se lee bien, `"no se pudo abrir: Archivo ausente"` no.

Solución adoptada: **capitalizar en el borde de salida, no en el origen.** El paquete `internal/logging` envuelve el `slog.TextHandler` con un `ReplaceAttr` que pone en mayúscula el mensaje y el valor del atributo `error`. Las cadenas de error del código quedan en minúscula y siguen siendo componibles; los logs salen capitalizados. `Capitalize` respeta UTF-8 ("ñandú" → "Ñandú") y no altera lo que empieza con dígito, signo o mayúscula.

Consecuencia a tener en cuenta: buscar en el código la forma capitalizada de un mensaje no lo encuentra; buscar en los logs sí.

Evidencia: 4 tests nuevos (tabla de `Capitalize`, capitalización de mensaje y error, composición con un `ReplaceAttr` del llamador, y no mutación de las opciones recibidas). Salida real de un arranque fallido:

`time=... level=ERROR msg="No se pudo cargar la configuración" path=archivo-que-no-existe.json error="No se pudo leer la configuración archivo-que-no-existe.json: open ..."`

Mensaje y error capitalizados, el nombre del campo (`path`) y su valor intactos. `gofmt` y `go vet` sin salida, `go test ./...` con 7 paquetes en verde.

**L22 — Los documentos del proveedor quedan fuera del repositorio.** Decisión del dueño del producto:
> "Los documentos de las APIs van al gitignore, que solo se suba mi codiggo."

Agregados a `.gitignore`: `Batidos-Remoto-API.md`, `YS Vending Management Platform API Document V1.0.1.pdf` y `ys-vending.txt` (el texto extraído del PDF). Motivo: son material de terceros con aviso de copyright explícito, mientras que el repositorio contiene solo trabajo propio.

Consecuencia aceptada: un clon nuevo no puede leer los contratos originales. Las reglas que esos documentos definen quedan igualmente fijadas en código y en tests (firmas, fechas en UTC+8, código de un solo uso, replay de nonce), y los huecos que dejaron siguen listados en «Huecos abiertos».

Lo que sí se versiona, por ser trabajo propio: código, migraciones, tests, `odd/tasks/combos-remotos.md`, `README.md`, `config.example.json`, `.gitignore` y las dos láminas de `design/`.

**L23 — Corrección de L22: tampoco las notas internas.** Precisión del dueño del producto:
> "odd y design tampoco. Es mas bien para nosotros interno. En el codigo voy a trabajar yo nada mas."

`odd/` y `design/` pasan a `.gitignore`. El repositorio queda con **23 archivos, solo código**: `cmd/`, `internal/`, `migrations/`, `go.mod`, `config.example.json`, `.gitignore` y `README.md`. Este último se mantiene porque explica cómo correr y probar el proyecto, y no contiene material de terceros ni decisiones internas.

Corrección de lo afirmado en L22: ese párrafo decía que `odd/` y `design/` se versionaban. Queda sin efecto.

**L25 — Sin comentarios en el código; todo a un documento.** Pedido del dueño del producto:
> "no me gustan los comentarios en el codigo. Eliminá todos los comentarios del codigo, PERO, agregalos en un .md para si yo necesito consutlar algo, tenerlo ahi. Nosé si me explico. En el .md por ejemplo que diga: Refund --> \"comentario.....\""

Regla nueva del proyecto: **el código no lleva comentarios**; la explicación vive en `docs/comentarios-codigo.md`, indexada por símbolo. Alcanza a `.go`, tests y `migrations/0001_init.sql`.

Cómo se hizo: no con `sed`. Un reemplazo de texto habría destruido `https://` y los literales de los que dependen las firmas. Se usó el AST de Go (`go/parser` + `go/printer`): se extrajeron los grupos de comentarios reales, se volcaron al documento y se reimprimió cada archivo sin comentarios. Después se etiquetó cada entrada con el símbolo que la precede, usando la convención de Go de que el comentario empieza con el nombre.

Números: 22 archivos Go y el SQL procesados, 946 líneas de documentación, 165 símbolos (29 quedaron como `(archivo)`: son comentarios de paquete y notas dentro de funciones, sin un símbolo propio).

Evidencia: `grep` de `//` en `.go` devuelve vacío salvo URLs dentro de strings; el SQL conserva las 10 tablas y no le queda ningún `--`; `git diff --numstat` muestra casi solo borrados (las pocas líneas agregadas son el realineado de campos que gofmt hace al desaparecer los comentarios); `gofmt`, `go vet` y `go test ./...` con 7 paquetes en verde, que es la prueba de que el código no se dañó al quitarle los comentarios.

Consecuencia asumida: `godoc` ya no muestra descripciones y un linter de estilo documental (golint/revive) se quejaría de identificadores exportados sin comentario. Es el costo de la decisión, no un olvido.

**L26 — Los mensajes de commit van en español.** Pedido del dueño del producto:
> "Sugerencia de commits en español por favor"

Convención completa de idioma del proyecto, ya sin ambigüedad: identificadores, nombres de campo y claves en inglés; comentarios, mensajes de log, errores, mensajes de commit y documentos en español. Formato de commit: Conventional Commits con el asunto en imperativo español ("agrega", "quita", "mueve") y sin punto final.

**L27 — `wire.go` separa el esquema del proveedor.** Decisión tomada junto al dueño del producto: los structs que existen para copiar el JSON del proveedor salen de `client.go` y van a `wire.go`, uno por paquete.

Criterio: si el struct existe para copiar su JSON, va a `wire.go`; si existe para que el motor hable con el cliente, se queda en `client.go`. Los tipos públicos (`CampaignRequest`, `OrderInfo`, `ShipmentRequest`) son interfaz nuestra y se quedan junto a sus métodos. No se usa un `types.go`: agrupar por categoría degenera en un cajón.

Resultado: `batidos/client.go` 303 → 250 líneas + `wire.go` 56; `snacks/client.go` 202 → 180 + `wire.go` 25. Sin cambios de comportamiento: la suite quedó en verde sin tocar un test. Las secciones de `docs/comentarios-codigo.md` se mudaron con los structs para que el índice siga diciendo la verdad sobre dónde vive cada cosa.

**L28 — T6: catálogo y chequeo previo.** Lo que el sistema necesita para no cobrar algo que no puede entregar.

Se agregó, en las dos APIs, las lecturas que faltaban: en Batidos `Device` (estado de la máquina) y `DeviceGoods` (catálogo por máquina, con `listing` y `soldOut`); en Snacks `Devices` (página de máquinas con sus cinco estados) y `Slots` (canales con stock y estado). Las cuatro son GET y van firmadas igual que los POST. Los servidores falsos las sirven con la misma verificación de firma y anti-replay, y ganaron setters para forzar cada estado.

Y el paquete nuevo `internal/preflight`, que es la regla: consulta las dos máquinas y los dos productos, acumula **todas** las razones de bloqueo en lugar de cortar en la primera, y declara vendible solo cuando no hay ninguna. No cobra, no dispara y no convierte un error en razón: un fallo del proveedor vuelve como error y el llamador decide no vender.

Decisiones que se fijaron, todas conservadoras:
- `soldOut` bloquea con cualquier valor distinto de 0, así que el `-1` ambiguo del proveedor bloquea. Se bloquea antes que prometer.
- Los estados se comparan contra «normal», no contra una lista de valores malos: un código nuevo del proveedor bloquea en vez de pasar como sano.
- Un SKU de snacks con stock pero solo en canales deshabilitados reporta «canal no vendible», no «sin stock», porque lo segundo sería falso. Las tres causas de canal son excluyentes: evaluarlas por separado daría vendible cuando un canal tiene el estado y otro el stock.

Verificación: `gofmt` y `go vet` sin salida, `go test ./...` con **8 paquetes en verde y 75 tests** (antes 42). El test que más importa del fake nuevo es el que firma la ruta **con** la query y exige 40004: si el falso empezara a aceptarla, el cliente podría firmar mal sin que nada lo note.

Desviación declarada por el worker de `preflight`: escribió la implementación antes que los tests y observó el rojo con dos mutaciones temporales (una devolvía siempre vendible, otra cambiaba `!= 0` por `== 1`). El rojo se observó, pero no en el orden que pide el flujo.

Sigue pendiente: `Dispenser.Check` por máquina, la reconciliación (T9) y todo el motor (T8).

Pendiente operativo: hay un `/tmp/checker.go.bak` fuera del repositorio que quedó de esas mutaciones; no afecta al proyecto.

Consecuencia que el dueño del producto aceptó al decidirlo: **este documento y las láminas no tienen historial de versiones.** Son la memoria del proyecto —decisiones, specs, huecos abiertos— y ahora existen solo en el disco. Se lo advirtió explícitamente; la decisión de respaldarlo o moverlo a un repositorio privado separado es suya.

**L24 — `odd/` vuelve al repositorio.** Pedido del dueño del producto:
> "Ok agrega odd."

Se quita `odd/` de `.gitignore`; `design/` sigue fuera. Queda sin efecto lo afirmado en L23 sobre este documento: ahora sí tiene historial, y el aviso de «sin versiones» se retiró del encabezado. El repo pasa de 23 a 24 archivos.

**L29 — Los commits los hace el dueño del producto.** Pedido:
> "Te pido para el futuro, no hagas los commits, tenelos listos para mi orden, pero asi puedo ver el git diff yo directamente."

Regla operativa desde acá: el orquestador **no ejecuta `git commit`**. Deja los cambios en el árbol, sin stagear, agrupados por unidad, y entrega por cada unidad el mensaje en español y los paths exactos. El diff queda visible con `git diff` y el dueño decide cuándo y cómo commitear.

Los commits anteriores a esta regla ya están en el historial: se revisan con `git show` o `git diff <rango>`.

Efecto secundario a tener en cuenta: como el árbol acumula lo que no se commitea, varias unidades pendientes a la vez se mezclan en un mismo `git diff`. Para evitarlo, cada reporte nombra los paths de la unidad, de modo que el diff se pueda acotar por ruta.

**L30 — Excepción a L29: por orden explícita, sí.** Pedido:
> "Entendido. Commitea, y pushea lo pendiente a main"

L29 decía que el orquestador no commitea. Por instrucción directa, en esta oportunidad sí: tres unidades en orden y push a `main`. La regla de L29 sigue vigente como default; cada vez que el dueño pida commitear explícitamente, se hace.

Las unidades: (1) `SaveAttempt` en el puerto del store, (2) el motor en `internal/order`, (3) la documentación. El orden importa: el motor no compila sin el método nuevo.

**L31 — Las tres deudas del motor, y el estado de cada una.**

1. **`MachineRef` en RAM** — *pagada*. La referencia vive en `store.Line.Machine`. Sin eso, tras un reinicio una línea `pending` no se podía despachar y `Place` + `ConfirmPayment` en instancias distintas no funcionaba.
2. **Rechazo acoplado a los proveedores** — *pagada*. `dispenser.Rejection` es el error del puerto; los adaptadores traducen su `APIError` en el borde. `internal/order` ya no importa `batidos` ni `snacks`.
3. **El tipo de máquina está duplicado** — *abierta*. `order.Item` lleva `MachineType` y además `MachineRef.Type`, y `validateDraft` no verifica que coincidan. Un `Item` incoherente (`MachineType: "snacks"` con `MachineRef{Type: "batidos", DeviceID: ...}`) se despacha con el driver de un tipo y los identificadores del otro. `machineIdentified` sólo exige que **algún** identificador exista, no que sea el del tipo correcto.

   Dos arreglos posibles:
   - **Barato:** una validación en `validateDraft` que exija `item.Machine.Type == "" || item.Machine.Type == item.MachineType`. No cambia contratos.
   - **Limpio:** sacar `MachineType` de `Item` y tomarlo de `Machine.Type`, dejando una sola fuente. Cambia el `Draft` y a sus llamadores.

   Riesgo real: el motor elegiría el driver correcto y le mandaría la identidad equivocada — un despacho que falla en el proveedor con un error que no explica nada. Hoy nadie construye `Draft` todavía (no hay capa HTTP), así que el costo de arreglarlo es mínimo.

**L32 — El proyecto se movió de lugar y esta sesión perdió su writer.** La carpeta pasó de `.../GO/compras-remotas` a `.../GO/Powermix BI-MAQUINA/compras-remotas`. El repo no se dañó: mismo remoto, mismo `HEAD`, árbol limpio, suite verde.

Consecuencia técnica: el subagente writer quedó **inutilizable** en esa sesión. `gentle-agents.ts` ancla el writer al `originalCwd` de la sesión y exige `safeBootstrapDirectory(originalCwd)`; con la ruta vieja borrada, falla cerrado con "Writer bootstrap requires the original safe project root". No es un bug del repo ni del path nuevo: el ancla es inmutable y apunta a algo que ya no existe. La sesión tampoco puede reubicarse: su storage está nombrado por la ruta vieja (`sessions/--D--Descargas-Seba-Proyects-GO-compras-remotas--`).

Se continuó inline con la idempotencia de la orden y se abrió una sesión nueva desde la raíz correcta para el resto.
