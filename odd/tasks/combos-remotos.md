# Combos remotos Powermix

App de tablet en un mueble con dos máquinas Powermix (batidos y snacks) para comprar combos y disparar la entrega en ambas a la vez.
Estado: especificación y planificación. Sin código todavía.

> Este documento es memoria interna del proyecto y **sí se versiona**: es el único registro de por qué el código es como es. `design/` sigue fuera de git.

## Cómo retomar

Raíz del proyecto (tiene un espacio en el nombre, citar siempre):

```
D:/Descargas/Seba/Proyects/GO/Powermix BI-MAQUINA/compras-remotas
```

Estado: **227 funciones de test en verde, 14 paquetes, sin dependencias externas**. Árbol con cambios sin commitear: T11, T10 y T10b armado (falta el valor de la URL pública).

Hecho: esqueleto, firmas de las dos APIs, servidores falsos, catálogo, store en memoria, dominio de estados, motor con despacho en paralelo, reintento del fallido, worker de reconciliación, wiring en `cmd/api` con barrido cada 30 s, idempotencia de la creación de orden, la API de la tablet en `internal/httpapi` con sus cuatro endpoints, y los callbacks de las dos máquinas con deduplicación y cierre por línea.

En curso: nada. **T10b está armado**: la campaña ya se crea con `notifyUrl` armado desde `PUBLIC_BASE_URL` + la ruta secreta. Falta sólo cargar esa variable cuando haya despliegue (S13); sin ella el arranque avisa con un `WARN` y la campaña sale sin aviso, como hoy.

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
- [x] **T8** Motor de orden — `Place`, `ConfirmPayment` con despacho en paralelo por línea, derivación del estado desde las líneas y tabla de transiciones; el aviso sale sólo para duda o fallo
- [x] **T9** Reintentos y reconciliación — reintento sólo ante rechazo explícito, código nuevo por intento, plazo de 2 minutos, ventana de reconciliación de 2 minutos y barrido cada 30 s
- [x] **T10** Callbacks — `POST /callbacks/snacks/{secret}` cierra una línea por suborden; `POST /callbacks/batidos/{secret}/{orden}` no cierra: confirma contra `getOrderInfo` (R6). Deduplicación en el store por `(origen, clave)`. T10b (el `notifyUrl` de la campaña) está armado de punta a punta: sólo falta el valor de `PUBLIC_BASE_URL`, que depende de S13.
- [x] **T11** API para la tablet — `internal/httpapi`: `GET /combos`, `POST /orders` (idempotente por `client_key`), `GET /orders/{id}` y `POST /orders/{id}/pay`. El pago simulado se niega en producción. Sigue sin conectarse el chequeo previo al motor.
- [ ] T12 App Android kiosco
- [ ] T13 Pruebas end-to-end + manual
- [ ] T14 Notificador — el puerto y el transporte por log existen; faltan el canal real (S18) y deduplicar los avisos repetidos

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

**L33 — T11c-2: la API de la tablet.** Paquete nuevo `internal/httpapi` con los cuatro endpoints que faltaban, montados sobre el `ServeMux` que ya servía `/healthz`.

Archivos: `internal/httpapi/handler.go` y `internal/httpapi/handler_test.go` (nuevos), `cmd/api/main.go` (construye el handler y registra las rutas), `docs/comentarios-codigo.md` (sección nueva del paquete).

Contrato que se fijó, porque ninguno estaba escrito:

- **`POST /orders` responde 200** tanto al crear como al reintento con el mismo `client_key`. No se usa 201 porque el endpoint también devuelve una orden existente, y separar los dos casos exigiría una lectura extra que `Place` ya hace adentro. La idempotencia no se reimplementó: se apoya en `OrderByClientKey` y en el `ErrAlreadyExists` del store, que ya estaban.
- **El `kiosk_id` es opcional sólo cuando hay un mueble configurado.** Con varios, se rechaza con 400: elegir por el cliente sería adivinar la sucursal.
- **404** para combo inexistente o inactivo y para mueble no configurado. **409** para un combo cuyos ítems piden una máquina que el mueble no tiene: la petición es válida, el mueble no puede cumplirla.
- **Cuerpo**: `DisallowUnknownFields`, tope de 64 KB y un único objeto JSON. Un cuerpo rechazado no crea nada.
- **Importe por línea en cero.** El catálogo define precio por combo, no por ítem; repartirlo sería inventar un dato. El importe de la orden es el del combo.
- **`POST /orders/{id}/pay` es pago simulado y sólo existe en `sandbox`.** En cualquier otro entorno responde 403 antes de tocar el motor, y el test lo prueba con un espía que falla si lo llaman y con la orden verificada intacta después.
- **Si el motor devuelve error, no se responde 500 a ciegas.** Se relee la orden: si ya no está pendiente de pago, el pago quedó registrado y lo que falló fue el despacho, así que la respuesta es 200 con el estado real y el detalle por línea; si sigue pendiente, es 500 y el cliente puede reintentar. Ese caso no es hipotético: `ConfirmPayment` guarda la orden pagada y recién después despacha.
- **La existencia de la orden la decide el motor**, no un chequeo previo del handler: `ConfirmPayment` ya devuelve `ErrNotFound` y un prechequeo sería TOCTOU.

Orden de trabajo observado: primero el esqueleto del paquete con los cuatro handlers devolviendo 501 y la suite completa escrita; después el rojo (19 casos fallando, más el esperado); después la implementación. El rojo se vio antes de escribir la lógica, no después.

Evidencia:

- `gofmt -l .` sin salida, `go vet ./...` sin salida, `go build ./...` ok.
- `go test ./...` con **14 paquetes en verde y 192 funciones de test** (antes 173). `internal/httpapi` aporta 19, incluidas las que prueban que un rechazo no deja orden creada, que el reintento no consume identificadores, que las líneas guardan el `device_id` del batido y el `mid` del snack, y que el pago simulado en producción no registra nada.
- Arranque real con `config.example.json` y `combos.example.json` en `127.0.0.1:18099`: `GET /combos` devolvió los dos combos activos; `POST /orders` creó `pending_payment` con dos líneas `pending`; el mismo `client_key` devolvió el mismo id; `GET /orders/{id}` repitió el estado; `POST /orders/{id}/pay` respondió 200 con `in_doubt` y `last_error` en cada línea, porque `api.batidos.invalid` no existe. Que el pago no mienta con un 500 y que el fallo quede en la línea es exactamente lo que se buscaba.
- `GET /orders/nope` devolvió 404.

Desviación declarada: el primer intento de la suite usó un motor sin máquinas registradas y `Place` lo rechazó con 500; el arreglo fue del test (mapa con un `Dispenser` falso por tipo), no del handler. Queda como evidencia de que el motor valida que exista driver antes de aceptar un borrador.

Riesgo: el pago es simulado y no verifica ningún cobro real, y el chequeo previo de disponibilidad sigue sin conectarse al motor, así que hoy se puede crear una orden sobre una máquina caída. `go test -race` sigue sin correr: no hay compilador de C en la máquina.

Sin commits: el dueño del producto pidió revisar el diff y decidir él. La unidad para revisar es `internal/httpapi/`, el bloque de `httpapi` en `cmd/api/main.go` y la sección nueva de `docs/comentarios-codigo.md`. Mensaje sugerido: `feat(api): agrega los endpoints de la tablet`.

**L34 — T10: los callbacks de las máquinas.** Lo que convierte «aceptado» en «entregado», que es la única verdad según R5.

Lo primero fue mapear qué comparten los dos mundos, porque el callback llega con identificadores de ellos. La respuesta ya estaba en el código: `store.Line.Ref` guarda el código de canje en batidos y el `SubOrderNo` en snacks, y ambos callbacks traen exactamente ese valor. No hizo falta ningún índice nuevo.

Lo que se agregó:

- **`internal/order/callback.go`** — dos entradas, y la diferencia entre ellas es el corazón del asunto:
  - `CloseFromCallback`: se usa en **snacks**, donde el callback es la única fuente. Su API no tiene consulta de suborden y `Reconcile` siempre devuelve duda, así que sin esta entrada una línea de snacks no se cerraba nunca.
  - `ConfirmFromCallback`: se usa en **batidos**, donde el callback no viene firmado. Por R6 no cierra nada por su cuenta: consulta `getOrderInfo` con el código de canje y cierra con lo que el proveedor responde. El estado del callback sólo se usa para subir la línea a «en elaboración», información cierta que no cierra nada.
  - Deduplicación por `(origen, clave)` con el `RegisterWebhook` que ya existía. El webhook se registra **antes** de buscar la línea: un callback con una referencia que no vamos a reconocer nunca se marca como visto igual, porque si no el proveedor lo reintentaría para siempre.
- **`internal/httpapi/callbacks.go`** — `POST /callbacks/batidos/{secret}/{orden}` y `POST /callbacks/snacks/{secret}`, con comparación en tiempo constante del secreto y **404 cuando el secreto no está configurado**. La ruta de batidos lleva la orden porque su callback no trae ningún identificador nuestro: el `notifyUrl` se entrega por campaña y la campaña se crea por línea.
- **Configuración**: `batidos.callback_secret`, `snacks.callback_secret`, y las variables `BATIDOS_CALLBACK_SECRET` / `SNACKS_CALLBACK_SECRET`. Opcionales, y opcionales quiere decir que sin ellas los endpoints rechazan todo.

Decisiones de contrato que se fijaron:

- **La respuesta al proveedor es parte del contrato, no cortesía.** Batidos documenta `{"code":200,"success":true,"data":true,"msg":"操作成功"}` y se devuelve tal cual, con el literal incluido. Snacks: `{"success":true,"code":200,...}`.
- **Nada que cerrar se acusa recibo con 200**, no con error: un 4xx o 5xx haría que el proveedor reintentara un evento que nunca va a poder aplicarse. Un cuerpo incompleto sí es 400 (error nuestro) y un fallo real es 500 (el único caso donde el reintento sirve).
- **Un estado fuera de la tabla se acusa recibo sin tocarlo.** No se cierra una línea por un código que no entendemos: es la misma regla conservadora de `preflight`.
- **Las claves de deduplicación** son `orderNo|code|status` en batidos y `orderNo|subOrderNo|shipmentStatus` por suborden en snacks. Así un reintento del mismo evento se descarta y el paso de «en curso» a «completado» del mismo código sí se procesa.
- **No se tocó `reconcileLine`**: el worker sigue igual. La alternativa —que una línea en curso pase a «elaborando» también al sondear— era un cambio de comportamiento compartido que nadie pidió.

Orden de trabajo observado, en las dos mitades: tests primero con la implementación devolviendo no-op/501, rojo (10 casos en `order`, 10 en `httpapi`, más los 3 de configuración sin compilar), después la lógica. El tramo de configuración y wiring lo hizo un worker con superficies acotadas y su propio rojo observado.

Evidencia: `gofmt -l .` sin salida, `go vet ./...` sin salida, `go build ./...` ok, `go test ./...` con **14 paquetes en verde y 215 funciones de test** (antes 192). Los tests que más importan: el de batidos que pasa `status: 2` y `status: 0` y comprueba que **`ConfirmFromCallback` se llama, no `CloseFromCallback`**; el que fuerza al motor a decir «fallo» en el callback y «entregado» en `getOrderInfo` y verifica que gana el proveedor; el de snacks con dos subórdenes, una entregada y otra fallida con su motivo; los tres de secreto (equivocado, vacío, y ruta ausente sin receptor); y el que verifica que un callback repetido no vuelve a consultar al proveedor.

Riesgo: **T10b sigue abierto** y no es cosmético: el `notifyUrl` de la campaña de batidos viaja vacío (`batidos/dispenser.go` no lo setea), así que en producción el callback de batidos no llega. El cable es chico —una opción `WithNotifyURL` y un campo de configuración con la URL pública— pero depende de S13 (hosting). Para snacks no hace falta: la URL se configura del lado del proveedor. Los otros dos riesgos son heredados: `go test -race` no corre en esta máquina, y el chequeo previo (`preflight`) sigue sin conectarse al motor.

Sin commits: la unidad para revisar son `internal/order/callback.go`, `internal/httpapi/callbacks.go`, los dos archivos de test nuevos, los dos secretos en `internal/config/config.go`, el bloque `WithCallbacks` en `cmd/api/main.go`, `config.example.json`, `README.md` y las secciones nuevas de `docs/comentarios-codigo.md`. Mensaje sugerido: `feat(api): recibe los callbacks de las dos máquinas`.

**L35 — La verificación independiente encontró cuatro defectos en T10, y se corrigieron.** Los cuatro tenían la misma raíz: el evento se marcaba como visto **antes** de que su efecto fuera durable.

1. **Deduplicación antes del efecto.** `claimCallback` registraba el webhook y después aplicaba. Un `SaveLine` o un `settleOrder` que fallara dejaba la clave quemada: el reintento del proveedor se descartaba y el evento se perdía. Ahora el orden es validar → resolver la línea → aplicar → recalcular la orden → registrar. Un fallo transitorio devuelve error **sin** registrar, así que el reintento sirve. La excepción sigue siendo la que nunca se va a poder aplicar (orden o referencia desconocidas): esa se registra antes de devolver el error, para no generar un reintento eterno.
2. **Una línea cerrada se degradaba.** `ConfirmFromCallback` forzaba `LineBrewing` en memoria aunque la transición fuera ilegal, y el reconcile posterior podía escribir `in_doubt` o `failed` encima de una línea ya entregada. Ahora `markBrewing` devuelve si aplicó, el llamador sólo asigna cuando aplicó, y el camino corta antes: **una línea cerrada no se toca ni se reabre**. Se acepta como decisión: una corrección del proveedor después del cierre se ignora; con dinero de por medio preferimos una línea que no se mueve sola.
3. **400 en un evento que nunca va a poder aplicarse.** Cuerpo roto y evento incompleto devolvían 400, y el proveedor reintenta ante no-2xx, con lo cual el reintento era eterno. Ahora se acusa recibo con 200 y queda en el log con nivel de error. El 500 quedó reservado para lo único que mejora al reintentarse.
4. **Secretos de callback opcionales en silencio.** `cmd/api` ahora avisa con un `WARN` cuando falta cada ruta secreta, porque sin ellas los endpoints rechazan todo y eso no se notaba en el arranque.

Evidencia: rojo observado antes de cada corrección (`transición de estado inválida`, `quedó "in_doubt"`, `no debe quedar registrado como visto`, y los dos 400 en lugar de 200), y después `gofmt -l .` sin salida, `go vet ./...` sin salida, `go build ./...` ok y `go test ./...` con **14 paquetes en verde y 219 funciones de test** (antes 215). Los tres tests nuevos — línea cerrada que no se degrada (los dos), y clave que **no** se quema cuando la aplicación falla — son la red que impide que esto vuelva.

Riesgo residual aceptado: no hay control de concurrencia entre el worker de reconciliación y los callbacks sobre la misma línea, porque el store en memoria es last-write-wins y de una sola instancia. Y la corrección del proveedor posterior al cierre no se aplica (decisión del punto 2).

**L36 — Se revisó `vps-powermix` y no aplica: es otra superficie de GS.** Pedido del dueño del producto:

> "Fijate en batidos, en el proyecto powermix-vps, que ahi vas a poder ver como funciona el tema del callbackp para los batidos"

Se leyó `D:/Descargas/Seba/Proyects/GO/vps-powermix` (sólo lectura, otro repositorio). Conclusión: ese proyecto integra GS por el **Payment Open API Document v2.0** (`docs/Nueva API GS.md`), que es la API de cobro, no la de la máquina. Sus callbacks son `POST /order/complete` (preparación de bebida, con `thirdOrderNo`, `success` y `outStockStatus`) y `POST /order/cancel`, sobre rutas fijas de nuestro dominio y con el envelope `{code,msg,data{returnCode,returnMsg}}`. No existe ahí el callback de canje: `兑换`, `redeem` y `campaign` no aparecen en ninguna parte, y tampoco hay creación de campañas.

Respuesta del dueño del producto, cerrando el tema:

> "Olvidate. Si era otra cosa, guiate por la de GS que venias usando."

Decisión: **T10 queda como está**, contra la Merchant Open API v2.0 del documento de este repositorio (campaña + código de canje + `getOrderInfo`), que es la que gobierna el despacho de la máquina.

Dos cosas que sí se aprovecharon de esa lectura, como validación y no como cambio:

1. **El emisor reintenta hasta recibir el acuse.** En `vps-powermix` el aviso saliente se reintenta desde el reconciler hasta marcar `gs_notified_at` (`internal/reconciler/reconciler.go:239-243`). Confirma que el acuse rápido con 200 y la idempotencia de la recepción son la decisión correcta: el que llama no se rinde.
2. **El patrón «no cierres con el aviso, verificá contra la API» es el que ya usa ese proyecto para su otro proveedor** (`verificarContraPVS`), y es exactamente lo que R6 pide para batidos. No fue una precaución inventada.

Nota para el futuro: si algún día GS unifica las dos superficies, el contrato de recepción cambia de forma (ruta fija, correlación por `thirdOrderNo` en el cuerpo, acuse con envelope). Queda anotado acá para no confundirlo con una regresión.

**L37 — T10b armado, y una fuga de secreto en los logs.** Pedido del dueño del producto:

> "Esta bien pero eso es cuando ya tengamos la api expuesta. Dejemos armado eso para tener todo el camino listo para solo aplicar el cambio en el path real cuando esté desplegado. Y si, arreglá el log."

**El camino del `notifyUrl`, completo salvo el valor.** El `notifyUrl` de batidos viaja **dentro del request de creación de campaña**, no se configura en ningún panel: por eso la campaña se crea por línea y la URL puede llevar la orden. Lo que quedó armado:

- `httpapi.BatidosCallbackURL(baseURL, secret, orderID)` es la **única** fuente de la forma de esa URL. Si algún día cambia la ruta, este constructor es lo único que hay que acompañar; hay un test que ata el constructor a la ruta registrada, pasando la URL generada por `httptest` y exigiendo 200. Sin ese test, alguien cambia la ruta y el `notifyUrl` empieza a apuntar a la nada sin que falle nada.
- `batidos.WithNotifyURL(func(orderID string) string)` en el dispenser. Sin la opción, la campaña se crea exactamente como antes (`notifyUrl` vacío): el cambio es aditivo.
- `PublicBaseURL` en la configuración (`public_base_url` / `PUBLIC_BASE_URL`), opcional y normalizado sin barra final.
- `cmd/api` arma la función con el constructor y avisa con un `WARN` cuando falta la base pública o el secreto, porque en ese caso el callback de batidos no va a llegar y el modo degradado sería silencioso.

**Cuándo el callback empieza a llegar:** el día del despliegue, con `PUBLIC_BASE_URL` apuntando al host real. No hay ninguna URL por defecto en el código, y es a propósito: una URL inventada mandaría los avisos a un host que no es nuestro.

**La fuga que se tapó.** El secreto de callback viaja dentro del path (`/callbacks/batidos/<secreto>/<orden>`), y cuatro líneas de log escribían `r.URL.Path` crudo: cada callback fallido dejaba el secreto escrito en el log, y con eso cualquiera podía forjar cierres de línea. Ahora `redactCallbackPath` deja `/callbacks/batidos/***` en los cuatro sitios (`fail` y los tres de `callbacks.go`). Hay un test que captura el log real y exige que el secreto no aparezca: la tabla del helper, sola, no probaba el camino verdadero.

Consecuencia asumida: el secreto sigue viajando en la URL, así que queda en los logs del proveedor y de cualquier proxy intermedio. Es inherente a la autenticación por ruta secreta que pide R6; la mitigación es que sea rotable. Lo que no podía pasar es que además lo escribiéramos nosotros.

Evidencia: rojo observado en los tres paquetes antes de implementar (`undefined: BatidosCallbackURL`, `cfg.PublicBaseURL undefined`, `undefined: batidos.WithNotifyURL`), y después `gofmt -l .` sin salida, `go vet ./...` sin salida, `go build ./...` ok y `go test ./...` con **14 paquetes en verde y 227 funciones de test** (antes 219). Y una decisión declarada: `PUBLIC_BASE_URL` no es obligatoria en la validación de la configuración; un arranque sin ella funciona en modo degradado, que es exactamente lo que hoy queremos.

Sin commits: la unidad es `internal/httpapi/callback_url_test.go` y los tests nuevos de config y batidos, más el cable en `internal/httpapi/callbacks.go`, `internal/httpapi/handler.go`, `internal/dispenser/batidos/dispenser.go`, `internal/config/config.go`, `cmd/api/main.go`, `config.example.json`, `README.md` y las secciones nuevas de `docs/comentarios-codigo.md`. Mensaje sugerido: `feat(batidos): manda el notifyUrl de la campaña y redacta el secreto en los logs`.

**L38 — La verificación de T10b encontró tres trampas de despliegue; dos se cerraron.** Con la verificación independiente del mismo cambio (riesgo de seguridad: el secreto de callback), el veredicto fue **no bloquea esta unidad**, pero señaló tres cosas que morderían el día que exista la URL real:

1. **`public_base_url` sin validar** (`config.go`), así que `http://` o un host sin TLS pasaban sin ruido y el `notifyUrl` —que lleva la ruta secreta adentro— habría viajado en claro hacia el proveedor y sus proxies. **Corregido**: cuando está, tiene que ser una URL absoluta con host, de esquema `http` o `https`, y en producción `https`. En sandbox se permite `http` para poder probar con un túnel.
2. **Secreto de callback sin validar**: uno con `/` parte el path en más segmentos y uno con `.` o `..` se colapsa en la limpieza del path. Los dos dejaban el callback **inalcanzable en silencio**, con un 404 indistinguible de un secreto equivocado. **Corregido**: sólo letras, números, punto, guion y guion bajo, y nunca `.` ni `..`.
3. **El atributo `error` y `line.LastError` no se redactan.** Si el proveedor devolviera en su mensaje de error la URL que le mandamos, ese texto viajaría al `last_error` de la línea y al log. **Aceptado como riesgo residual**: depende de un eco del proveedor que no está demostrado, y el destinatario del `last_error` es nuestra propia tablet. Queda anotado para cuando haya un log externo.

Evidencia: rojo observado con cinco tests nuevos de configuración antes de implementar (`debía rechazarse`), y después `gofmt -l .` sin salida, `go vet ./...` sin salida, `go build ./...` ok y `go test ./...` con **14 paquetes en verde y 232 funciones de test** (antes 227). Arranque real con `config.example.json`: el `WARN` «La campaña de batidos se va a crear sin notifyUrl» sale en la primera línea, antes de que el servidor escuche.

Regla que queda para el despliegue (S13): cargar `PUBLIC_BASE_URL` con `https` público y un `BATIDOS_CALLBACK_SECRET` de sólo caracteres de ruta. Las dos cosas ahora se validan al arrancar, así que un error de tipeo no se convierte en callbacks muertos.

**L39 — El chequeo previo, conectado. Y la prueba end-to-end que encontró dónde falta.**

**Lo conectado.** `internal/preflight` estaba escrito y probado desde T6, pero nadie lo llamaba: se podía crear una orden sobre una máquina caída. Ahora el motor lo consulta, sin romper la regla de capas:

- `order.Gate` es el puerto (`Check(ctx, Draft) error`) y `WithGate` la opción. **El motor no importa `preflight` ni los drivers**: el adaptador vive en `internal/gate`, que traduce el `Draft` a la consulta (`DeviceID` de batidos, `MID` de snacks) y falla cerrado si el combo no tiene exactamente un ítem por máquina, porque la regla actual no sabe evaluar otra forma.
- La consulta va **después** de la idempotencia y **antes** de generar ids: un reintento con la misma `client_key` sobre una orden que ya existe no se vuelve a chequear (la máquina pudo caerse después; bloquear ahí sería mentir sobre un pedido hecho) y un bloqueo no consume identificadores.
- Un proveedor inalcanzable **no** es un «no vendible»: devuelve error y la respuesta es 500, porque reintentar sirve. Lo que no se puede vender devuelve `NotSellableError` con sus razones y el HTTP responde **409** con `reasons` en el cuerpo, que es lo que la tablet necesita para decir «máquina fuera de línea».

**La prueba end-to-end, corrida de verdad.** Se levantaron los dos procesos y se caminó una compra. Lo que pasó:

| Paso | Resultado |
| --- | --- |
| `GET /combos` | 200 |
| `POST /orders` | **200** `pending_payment` con dos líneas |
| `POST /orders/{id}/pay` | 200 `dispatching`; las dos líneas `sent`, intento 1 |
| Callback de batidos (con `getOrderInfo` completado) | 200, línea `delivered` |
| Chequeo previo con la máquina fuera de línea | **409** `{"reasons":["batidos_device_offline"]}` |
| Secreto en el log del backend | 0 apariciones; sólo `/callbacks/batidos/***` |

Antes de eso hubo que arreglar el **arnés**, no la aplicación: `cmd/fakeapi` arrancaba el falso de batidos con el catálogo de productos vacío y sin forma de sembrarlo, así que el chequeo previo recién conectado bloqueaba *toda* venta con `batidos_sku_not_listed`. El 409 era correcto; el falso era el incompleto. Ahora `cmd/fakeapi` tiene un tercer servidor de administración (`-admin-addr`, loopback obligatorio) que llama a los setters existentes, y siembra un producto por defecto con `-batidos-goods`.

**Lo que el end-to-end destapó y todavía no está resuelto:**

1. **La correlación del callback de snacks depende de un dato que el proveedor quizá no manda.** El falso responde el despacho con `result: {}` y sin `subOrderId` — igual que el documento del proveedor, que también muestra `"result": {}`. Sin ese id, `line.Ref` queda vacío y **ningún callback de snacks puede encontrar su línea**: se acusa recibo y no se cierra nada. Si el proveedor real tampoco lo devuelve, hay que correlacionar por otra vía (orden + tipo de máquina) o el cierre de snacks nunca ocurre. Es el hueco de bloqueo «JSON exacto de `/open/v7/device/remote/shipment`» mordiendo por primera vez.
2. **El falso de batidos no sabe volver a poner la máquina en línea**: `SetDeviceOffline` y compañía sólo van en una dirección, así que `offline:false` responde 501 con el motivo en vez de fingir. Es una limitación del arnés, no del backend.
3. **`GET /orders/{id}` no expone el `Ref` de cada línea.** Para la tablet probablemente esté bien, pero deja al operario sin el código de canje y obligó a deducirlo del contador del falso durante la prueba.

Evidencia: `gofmt -l .` sin salida, `go vet ./...` sin salida, `go build ./...` ok, `go test ./...` con **14 paquetes en verde y 249 funciones de test** (antes 232; 17 nuevas del chequeo previo). Sin procesos escuchando al terminar la corrida y sin tocar código de la aplicación para que la prueba pasara.

**L40 — El callback de snacks ya encuentra su línea, y el end-to-end llega a `delivered`.**

El documento del proveedor muestra la respuesta del despacho con `"result": {}`: **no trae id de suborden**. Nuestro cliente lo asumía (`result.subOrderId | subOrderNo`) y el falso lo imitaba, así que `Line.Ref` quedaba vacío y el callback de snacks no podía cerrar nada. La suposición estaba en nuestro código, no en el proveedor.

La solución, decidida con el dueño del producto, es resolver la línea con tres reglas en orden:
1. `line.Ref == cb.Ref` (lo de siempre, exacto).
2. `line.ID == cb.ThirdOrderNo`. El callback documenta ese campo como «thirdparty suborder number»: es nuestro propio `OrderProductId`, que mandamos como `LineID`.
3. La **única** línea de esa máquina cuyo `Ref` esté vacío. Más de una candidata → `ErrCallbackAmbiguous` y no se cierra nada; ninguna → `ErrCallbackUnknownLine`.

Cuando la línea resuelta tenía `Ref` vacío, se **adopta** el `SubOrderNo` del callback, así el próximo evento entra por la regla 1. No hay escritura extra: lo persiste el mismo `SaveLine` del cierre.

Decisión de transporte: `ErrCallbackAmbiguous` se acusa con **200** y log de error, igual que una referencia desconocida. Reintentar no lo arregla y adivinar sería peor.

Evidencia: rojo observado en las reglas 2 y 3, en la ambigüedad y en el caso de `ThirdOrderNo` del lado HTTP; después `gofmt` y `vet` sin salida, `go build` ok, `go test ./...` con **14 paquetes en verde y 258 funciones de test** (antes 249).

Y la prueba end-to-end completa, contra los falsos, terminó en **`delivered`**:

```
POST /orders              → 200 pending_payment, dos líneas
POST /orders/{id}/pay     → 200 dispatching; batidos y snacks "sent", intento 1
GET  /orders/{id}         → Ref de snacks vacío (el falso responde "result": {})
POST /callbacks/snacks    {"SubOrderNo":"", "ThirdOrderNo":"18dca11a…", "ShipmentStatus":1} → 200
POST /admin/batidos/advance + POST /callbacks/batidos → 200
GET  /orders/{id}         → 200 status="delivered", las dos líneas delivered
```

Riesgo residual: `lineWithoutRef` no excluye líneas ya cerradas, así que una línea cerrada sin `Ref` sigue siendo candidata; su cierre se re-aplica como no-op y nunca como degradación, porque una línea cerrada no se reabre. Y sigue sin haber control de concurrencia entre el worker y los callbacks (heredado del store en memoria).

**L41 — La verificación de L40 encontró dos formas de cerrar la línea equivocada; se cerraron.** El mismo cambio que arregló la correlación había abierto dos puertas:

1. **La regla 3 podía elegir la suborden hermana.** Con dos líneas de snacks, una con `Ref` y otra sin él, un callback cuyo `Ref` no coincidía (id rotado por el proveedor, o evento de un intento anterior) cerraba la línea que no era. Cerrar es irreversible. Ahora las candidatas son **todas** las líneas de esa máquina: exactamente una y sin `Ref` → esa; más de una → `ErrCallbackAmbiguous` sin importar sus `Ref`.
2. **La regla 2 no filtraba la máquina.** Un `ThirdOrderNo` que coincidía con el `ID` de la línea de batidos cerraba la línea de batidos con el resultado de snacks. Ahora las reglas 2 y 3 exigen `MachineType` y comparan también la máquina de la línea; sin ese dato sólo aplica la regla 1.

Lo que empeoró a propósito, y por qué está bien: hay casos que antes se aceptaban y ahora se acusan recibo sin cerrar nada. Un acuse es reversible; un cierre no.

**Lo que quedó sin resolver y bloquea el despliegue (no esta unidad):** el worker de reconciliación y los callbacks escriben la misma línea sin control de concurrencia. `ReconcileDue` toma una copia de las líneas y la guarda entera después, así que puede revertir un `delivered` que un callback acaba de escribir (y borrar el `Ref` adoptado, reabriendo la regla 3). No es un defecto nuevo —el store en memoria es last-write-wins desde el principio— pero ahora es visible y medible. La corrección es una versión por línea con guardado condicional en el puerto del store, y va en su propia unidad, junto con la implementación sobre Postgres.

Evidencia: rojo observado en los tres escenarios de las guardas (incluido uno de punta a punta donde un aviso de snacks entregaba la línea de batidos), después `gofmt`/`vet` sin salida, `go build` ok y `go test ./...` con **14 paquetes en verde y 263 funciones de test** (antes 258).

**L42 — Versión por línea y guardados condicionales: la carrera del worker contra los callbacks, cerrada.**

El defecto: `store.Memory` guardaba la línea entera y el último en escribir ganaba. `ReconcileDue` toma una copia de las líneas al empezar y la reescribe completa después, así que un callback que cerraba la línea en el medio era **revertido**: `delivered` volvía a `sent` y el `Ref` adoptado se borraba.

Lo implementado:

- **`store.Line.Version`** y `SaveLine` condicional: si la versión no coincide con la del store devuelve `ErrStale` y **no escribe nada**; si coincide, persiste con `Version+1` y devuelve la línea guardada (por eso la firma ahora devuelve la línea: el llamador encadena la versión nueva en vez de inventarla). `CreateOrder` normaliza las líneas a versión 0, así una línea no puede nacer inmune a los conflictos.
- **Reacción distinta en cada llamador**, porque `ErrStale` significa cosas distintas: `closeLine` relee la orden y, si la línea ya está cerrada, es un no-op exitoso; si sigue abierta, reintenta **una sola vez** con la versión fresca. `extendReconcile` y `markBrewing` se retiran sin error: la línea ya tiene dueño. `dispatchLine` no revierte nada y deja que la reconciliación resuelva.
- **El hueco hermano**: `SaveOrder` reemplazaba la orden entera, líneas incluidas, así que `settleOrder`/`ConfirmPayment` podían pisar un cierre aunque la línea tuviera versión. Ahora guarda **sólo la cabecera**. Ese contrato estaba fijado por un test (`TestMemorySaveOrderReplacesTheWholeOrder`) que hubo que invertir a propósito: el reemplazo total era intención vieja y hoy es la carrera.

Cómo quedó demostrada la carrera: `TestReconcileDueDoesNotOverwriteTheCloseThatACallbackAlreadyMade` captura la copia que `DueLines` le devolvió al worker, cierra la línea con un `CloseFromCallback` real (que adopta su `Ref`), y recién entonces corre el barrido con esa copia vieja. Tres subtests según lo que la máquina le conteste al worker; en los tres se exige que no haya error, que la máquina se consulte una sola vez, que la línea siga `delivered`, que el `Ref` siga adoptado y que los plazos sigan en cero. El camino simétrico está en `TestEngineDispatchDoesNotRevertTheLineThatACallbackClosedWhileItWasInFlight`.

Sobre el método: el cambio de `SaveOrder` se hizo sin rojo previo (el test viejo fijaba lo contrario, así que el rojo era una inversión semántica, no una función que faltaba). Para no quedarme en la creencia, **muté el store a propósito**: con el comportamiento viejo el test nuevo falla (`FAIL: TestMemorySaveOrderOnlyPersistsTheHeader`) y con el nuevo pasa. El rojo se observó, aunque después de escribir el código.

Evidencia: `gofmt -l .` sin salida, `go vet ./...` sin salida, `go build ./...` ok, `go test ./...` con **14 paquetes en verde y 270 funciones de test** (antes 263).

Sigue abierto, y es lo que viene: la versión vive sólo en `store.Memory`; el store sobre Postgres, su columna `version` y la ejecución del contenedor quedan para la unidad siguiente. Y `go test -race` sigue sin correr: la carrera se demuestra de forma determinista por la copia vieja inyectada, no por concurrencia real.

**L43 — Store sobre Postgres, migración 0004 y contenedor.** Lo que faltaba para dejar de perder el estado al reiniciar.

**La migración 0004** cierra tres huecos que el esquema tenía respecto del código, y no son cosméticos:

- `order_line.version`, porque el guardado condicional de L42 necesitaba su columna.
- `orders.client_key` con índice único parcial, porque la idempotencia de la creación vivía en el mapa del store en memoria y no tenía dónde persistir.
- Las seis columnas `machine_*` en `order_line`, porque la identidad de la máquina vive en la línea (L31) y el esquema no la guardaba: con Postgres, un reinicio habría dejado líneas sin saber a qué máquina despachar.
- Dos índices únicos que creí faltantes y **no** faltaban: 0001 ya declaraba `dispatch_attempt_idempotency_idx` y `webhook_event_dedupe_idx`. La verificación lo detectó; los saqué de 0004 antes de aplicarla en ninguna base, así que el esquema no queda con índices duplicados. La corrección de este párrafo es parte de L43, no un apéndice.

**El store**: paquete `internal/store/postgres`, implementando el mismo puerto que la memoria, con `SaveLine` como `UPDATE … WHERE id = $1 AND version = $2 RETURNING …` (cero filas → `ErrStale` si la línea existe, `ErrNotFound` si no) y `SaveOrder` que toca **sólo** la cabecera. La primera dependencia externa del proyecto: `jackc/pgx/v5`, fijada en la última versión que no obliga a subir el `go` del módulo.

**El sembrado**: `orders` tiene FK a `kiosk` y a `combo`, así que con Postgres no se puede insertar una orden si esas filas no existen. `internal/topology` hace upsert idempotente de muebles, máquinas, combos e ítems al arrancar, siguiendo las columnas exactas de 0001.

**El contenedor**: `Dockerfile` multi-stage (builder `golang:1.23`, final distroless nonroot) y `docker-compose.yml` con cuatro servicios —`db` (postgres:16 con healthcheck), `migrate` (aplica los `.sql` con `psql`), `api` (store por Postgres, puerto 18080) y `fakeapi`—, más `config.docker.json` con las URLs internas y secretos de prueba.

Estado de la verificación, y esto es lo importante: **nada de esto se ejecutó todavía contra un PostgreSQL real.** El daemon de Docker no está corriendo en esta máquina y no hay `psql`, así que 13 de los 14 tests del paquete Postgres se saltean y la migración 0004 nunca se aplicó. `docker compose config` sí parsea y resuelve los cuatro servicios. La primera corrida contra la base es la única prueba definitiva, y es exactamente el siguiente paso.

Evidencia de lo que sí se puede afirmar: `gofmt -l .` sin salida, `go vet ./...` sin salida, `go build ./...` ok, `go test ./...` con **16 paquetes en verde y 298 funciones de test**.

Decisiones a tener presentes cuando levantemos: el puerto de administración del falso (9403) **no** se publica en el compose, porque el falso se niega a escuchar fuera de loopback; y `migrate` no lleva tabla de versiones, así que repetir el esquema exige `docker compose down -v`.

**L44 — Reorganización por responsabilidad: ni cajones de varias cosas ni carpetas de un solo archivo suelto.** Pedido del dueño del producto:

> "Antes de eso, revisemos la arquitectura del proyecto para que no queden mezcladas cosas ni carpetas llenas de varios archivos"

Criterio con el que se decidió cada corte, y que vale para lo que venga: **un paquete se separa por audiencia y contrato, no por cantidad de líneas; un archivo se separa por una sola razón para cambiar.** Con eso, el borde HTTP quedó en un paquete (misma audiencia interna, mismo glue) pero con dos tipos distintos, y el motor quedó en un paquete con seis archivos.

Lo que se movió:

- **`internal/order`**: `engine.go` pasó de 708 líneas a 139 (tipo, `New`, opciones, generación de ids) y el resto se repartió en `place.go`, `payment.go`, `dispatch.go`, `reconcile.go` y `settle.go`. Los tests siguieron el mismo corte y **todos los fixtures quedaron en `helpers_test.go`**, uno solo, para que no se dupliquen.
- **`internal/httpapi`**: se separaron los dos contratos que compartían el tipo `Handler`. La tablet quedó en `Handler` (`orders.go`, `combos.go`, `draft.go`, `responses.go`) y los proveedores en `Callbacks` (`callbacks.go`, `callback_url.go`), cada uno con su constructor y su `Register`; el glue común en `respond.go` y `errors.go`. `cmd/api` registra los dos. No se partió en dos paquetes a propósito: obligaría a duplicar `writeJSON`/`fail` o a inventar un paquete para tres funciones, y eso es la otra forma de romper el orden, la de fragmentar de más.
- **`internal/fakeapi`**: la superficie de administración salió de `cmd/fakeapi/main.go` (465 → 133 líneas) y se mudó a `internal/fakeapi/admin.go`, junto a los setters que llama. **Ganó 13 tests que no tenía**: era código de arnés viviendo en un `main` sin cobertura.
- **`internal/config`**: `config.go` 325 → 174 (tipos, `Load`, overrides) y `validate.go` con todas las reglas; los tres tests de validación se consolidaron en uno.

Lo que se dejó como estaba, y por qué: el puerto `dispenser` con sus dos adaptadores, `store` con `postgres` como subpaquete, y `gate`/`topology` como adaptadores en la costura. También se dejaron los `Dockerfile`/`docker-compose.yml` en la raíz, porque `docker compose up` los busca ahí por convención.

Evidencia: `gofmt -l .` sin salida, `go vet ./...` sin salida, `go build ./...` ok, `go test ./...` con **16 paquetes en verde y 311 funciones de test** (298 conservadas, ninguna con la expectativa cambiada, más 13 nuevas de administración). Ningún comportamiento cambió: la reorganización se valida comparando el contenido de `HEAD` contra los archivos nuevos, y lo único distinto son las adaptaciones mecánicas (receptor, `fail` como función libre con logger, campos que se mudaron de `Handler` a `Callbacks`).

Costo declarado: el diff es grande y ensucia `git blame` en archivos jóvenes, y hubo tres borrados por consolidación (`callback_safety_test.go`, `publicbaseurl_test.go`, `engine_test.go`), con su contenido íntegro en los archivos nuevos. Se hizo ahora, antes de seguir agregando piezas, justamente porque después es más caro.
