# Comentarios del código

Este documento es el único lugar donde vive la explicación del código.
El código fuente no lleva comentarios a propósito: acá se consulta, se busca y se versiona.

Formato: `Símbolo --> "texto del comentario"`.

## cmd/api/main.go

### (archivo)

> Command api es el backend con el que habla la tablet.
> 
> En esta etapa solo expone un endpoint de salud: el sentido de este esqueleto es
> que la topología venga de la configuración y se valide al arrancar, para que un
> mueble mal configurado falle acá y no delante de un cliente.

### main

> Los nombres de los campos JSON se mantienen estables: son un contrato
> legible por máquinas, no texto para una persona.

## cmd/fakeapi/main.go

### (archivo)

> Command fakeapi levanta los dos sustitutos locales de los backends de máquinas.
> 
> Permite empezar hoy con el trabajo de cliente: la API de batidos y la de snacks
> responden en dos puertos, exigiendo las firmas reales, el código de canje de un
> solo uso y la regla de replay de nonce.

## internal/config/config.go

### (archivo)

> Package config carga la topología del sistema (muebles y máquinas) desde un
> archivo JSON y las credenciales desde variables de entorno.
> 
> Separación deliberada: el archivo describe *qué* hay instalado, el entorno
> guarda *los secretos*. Así el archivo se puede versionar sin filtrar claves.

### const

> Documentado en "YS Vending Management Platform API Document V1.0.1".
> El host de producción es el que documenta el proveedor. No se renombra con
> el nombre de la máquina: es un contrato ajeno.

### Machine

> Machine es una máquina física dentro de un mueble.

### Machine

> El tipo de la máquina es `machine.Kind`: el vocabulario de la familia se
> declara una sola vez en `internal/machine` y ninguna capa consumidora lo
> repite. Los identificadores que cada proveedor pide siguen siendo campos de
> este struct: batidos necesita `deviceId` y `deviceNo` porque son distintos y
> `produceCoffee` pide ambos; snacks sólo necesita `mid`.

### Kiosk

> Kiosk es el mueble físico: dos máquinas y la tablet en el medio.

### Machine

> Machine devuelve la máquina del tipo pedido, si el mueble tiene una.

### Config

> Config es la configuración ya resuelta y validada.

### PublicBaseURL

> Base pública de este backend, la que el proveedor de batidos va a llamar. Es opcional a propósito: sin ella la campaña se crea sin `notifyUrl` y el callback de batidos no llega, pero nada más se rompe. No figura en `validate()` porque el hosting sigue sin decidirse (S13) y el valor real todavía puede faltar.
>
> Se normaliza sin barra final porque la URL del callback la arma `httpapi.BatidosCallbackURL`, y esa forma se decide en un solo lugar. Llega desde `public_base_url` en la raíz del archivo o desde `PUBLIC_BASE_URL`; el entorno gana, igual que con los secretos.

### Load

> Load lee el archivo de topología, aplica los reemplazos de secretos desde el
> entorno, resuelve los valores por defecto documentados y valida el resultado.

## internal/config/validate.go

### kioskProblems

> Un combo siempre empareja una bebida con un snack, así que cada mueble debe
> tener exactamente una máquina de cada tipo. Cualquier otra cosa se detecta
> acá y no en el momento de la venta.

## internal/config/config_test.go

### TestLoadUsesProductionURLWhenEnvironmentIsProduction

> Host de producción tal como figura en la documentación del proveedor.

## internal/dispenser/batidos/client.go

### (archivo)

> Rutas documentadas del comercio de Batidos. En Batidos no se pide un producto:
> se canjea un código que antes emitió una campaña (R2), así que una bebida son
> dos llamadas como mínimo.

### const

> successCode es el único code del sobre que significa éxito. Un HTTP 200
> con otro code sigue siendo un error.

### const

> defaultTimeout acota una llamada suelta. El proveedor no publica SLA y el
> presupuesto de 2 minutos por línea (S16) es del motor de órdenes, no del
> cliente: acá solo se evita que una conexión colgada lo consuma entero.

### const

> maxResponseBytes protege contra una respuesta absurda. Los sobres de esta
> API pesan unos pocos cientos de bytes.

### Option

> Option ajusta el cliente al construirlo. Existe sobre todo para los tests:
> reloj inyectable y cliente HTTP propio.

### WithHTTPClient

> WithHTTPClient reemplaza el cliente HTTP por defecto (timeout de 10s).

### WithClock

> WithClock reemplaza el reloj con el que se firma cada request. Sirve para que
> los tests sean deterministas y para tomar el timestamp de una fuente propia.

### Client

> Client habla con la API abierta de comercio de Batidos. No reintenta, no
> aplica backoff y no loguea: eso es responsabilidad del motor de órdenes (T9).
> Cada llamada devuelve el error tal como lo informó el proveedor para que el
> motor decida si es un rechazo (E2) o un intento incierto (E4).

### NewClient

> NewClient arma un cliente para una baseURL (esquema y host, sin la ruta del
> endpoint). La key y el secret los entrega el proveedor (S4).

### CampaignRequest

> CampaignRequest describe la campaña de canje que se emite antes de pedir una
> bebida (S6: la campaña se crea en cada compra).

### CampaignRequest

> Start y End son instantes absolutos: se convierten a UTC+8 al enviarlos.
> Nunca se formatean con la zona local (R3).

### CampaignRequest

> ExchangeType 1 = un solo producto, 2 = cualquiera de los listados.

### Campaign

> Campaign es la campaña creada y los códigos de canje que emitió.

### Campaign

> tal como lo informa el proveedor, en UTC+8

### Campaign

> tal como lo informa el proveedor, en UTC+8

### ProduceRequest

> ProduceRequest pide canjear un código. Los cuatro campos son obligatorios.

### OrderStatus

> OrderStatus es el estado del pedido que informa getOrderInfo.
> 
> La documentación del enum de getOrderInfo no existe: estos tres valores salen
> del contrato del callback (0 falló, 1 en preparación, 2 completado). Por eso
> el campo se expone como número: un valor nuevo del proveedor no debe romper la
> lectura, solo quedar como desconocido para quien lo interprete.

### OrderInfo

> OrderInfo es la verdad reconciliada sobre un canje.

### OrderInfo

> OutStockStatus informa un problema de stock. El significado de sus valores
> no está documentado, así que se expone crudo.

### APIError

> APIError es un rechazo informado por la API, ya sea porque el sobre trae un
> code distinto de 200 o porque el HTTP no fue 200. Se detecta con errors.As.

### CreateCampaign

> CreateCampaign emite la campaña de canje y devuelve los códigos que generó.

### CreateCampaign

> Las dos fechas se construyen en UTC+8: desde Argentina son 11 horas de
> diferencia y una campaña en hora local nace en el futuro (R3).

### CreateCampaign

> Sin código no hay nada que canjear: la campaña existe justamente para
> emitirlo, así que esto es un fallo aunque el sobre diga 200.

### ProduceCoffee

> ProduceCoffee canjea el código y le pide a la máquina que prepare el producto.
> 
> Aceptado NO es entregado (R5): un DispatchResult exitoso solo significa que el pedido
> fue tomado. La entrega real la confirma el callback y se verifica con
> OrderInfo. DispatchResult.ProviderRef queda vacío a propósito porque produceCoffee no
> devuelve orderNo; hay que leerlo de OrderInfo.

### OrderInfo

> OrderInfo le pregunta al proveedor qué pasó realmente con un canje. Es la
> única lectura con la que se reconcilia un intento incierto (S16) y con la que
> se confirma la entrega antes de cerrar una línea (R6).

### do

> do firma, envía y valida el sobre de un endpoint. No reintenta ni loguea: el
> error vuelve tal cual lo informó el proveedor o envuelto si falló la red.

### do

> La firma se arma con el reloj del cliente, no con el del sistema.

### do

> El error de red se devuelve envuelto y sin recortar: el motor necesita
> distinguir "no hubo respuesta" (E4) de un rechazo explícito (E2).

### devicePath / deviceGoodsPath

> Rutas de lectura de Batidos, las que alimentan el chequeo previo: antes de generar el QR hay que saber si la máquina está sana y si el producto se puede vender (S10). A diferencia de los tres endpoints de pedido, se invocan con GET.

### DeviceStatus / DeviceRunStatus / ScarceStatus

> Tres ejes distintos, y por eso son tres tipos: conexión (1 en línea, 2 fuera de línea), avería (1 normal, 2 avería) e insumos (1 normal, 3 sin stock). Una máquina conectada puede estar averiada, y ese es justo el caso que el chequeo previo tiene que frenar. El enum de insumos no es contiguo en la fuente: salta de 1 a 3 y el 2 no está documentado, así que no se le inventa significado.

### Device

> Lo que el chequeo previo necesita saber de la máquina antes de cobrar. Los tres estados se exponen como tipos con constantes para que quien decide no compare números crudos.

### DeviceGoods

> Un producto del catálogo de una máquina. `SoldOut` queda como int crudo a propósito: la tabla del proveedor documenta 0/1 pero todos sus ejemplos devuelven -1, así que el significado real no está confirmado. `Price` es el precio de lista que informa el proveedor; no es dinero con el que se calcule un cobro.

### Device / DeviceGoods (métodos)

> Lecturas puras: no cambian nada en el proveedor, no reintentan, no loguean. Un catálogo vacío es una respuesta válida (la máquina no tiene productos cargados), no un error: qué hacer con eso lo decide el chequeo previo.

### do / get / send

> `do` sigue siendo el POST de los tres endpoints de pedido y conserva su firma para que ningún llamador cambie; sólo delega en `send`. `get` es la lectura: mismo esquema de firma, porque el proveedor exige key, timestamp y key-md5 en todos sus endpoints, y sin cuerpo ni Content-Type, porque mandarlos sería inventar un contrato que la documentación no pide. El error de red vuelve envuelto y sin recortar: el motor necesita distinguir «no hubo respuesta» (E4) de un rechazo explícito (E2).

## internal/dispenser/batidos/client_test.go

### (archivo)

> Los tests viven en el paquete externo batidos_test a propósito: el cliente se
> ejercita contra el servidor falso real (fakeapi), que a su vez importa este
> paquete. Desde adentro de batidos eso sería un ciclo de importación.

### const

> campaignPath es la ruta documentada para emitir la campaña de canje.

### fixedNow

> fixedNow congela el reloj del cliente: la firma y las fechas dejan de depender
> del instante en que corre el test.

### (archivo)

> Recorrido completo: campaña, canje del código, y la verdad reconciliada por
> getOrderInfo antes y después del callback.

### TestClientCreatesCampaignAndFollowsTheDrinkUntilCompleted

> un solo producto

### TestClientCreatesCampaignAndFollowsTheDrinkUntilCompleted

> El callback del proveedor es el único que confirma la entrega real.

### (archivo)

> Un código de canje es de un solo uso: el segundo canje es un rechazo del
> proveedor y el mensaje tiene que llegar al llamador.

### (archivo)

> R3: las fechas de campaña viajan en UTC+8. Un instante UTC se corre +8 horas,
> y la firma sale del reloj inyectado.

### TestClientSendsCampaignTimesInChinaTime

> El resto del contrato del endpoint, para que el test falle si cambia.

### TestClientSendsCampaignTimesInChinaTime

> La firma se arma con c.now(), no con el reloj del sistema.

### (archivo)

> Un HTTP 200 con un code distinto de 200 tampoco es éxito: el sobre es el que
> decide, no el código de transporte.

### (archivo)

> Un HTTP 500 con sobre JSON sigue siendo un rechazo de la API, no un éxito.

### (archivo)

> El contexto manda: si el llamador lo corta, la llamada se corta con él.

### TestClientRespectsContextCancellation

> El handler se libera cuando el test termina, así srv.Close no espera al
> request abandonado.

### deviceWireResponse

> Copia lo que devuelve device/detail, sólo los campos que el chequeo previo necesita. El proveedor manda además la contraseña del equipo (devicePwd), el comercio y las curvas de temperatura: no se piden ni se guardan.

### deviceGoodsWireResponse

> Un producto de goods/deviceGoodsDetail. `listing` no figura en la tabla de parámetros pero sí en todos los ejemplos de respuesta; se lee tal cual.

### stateNumber

> Acepta el estado como número o como texto. La tabla del proveedor declara String para deviceRunStatus y scarceStatus mientras sus ejemplos los muestran como número, y un ejemplo de la lista de máquinas los muestra entre comillas: se toleran las dos formas para que un cambio de tipo no rompa el chequeo previo. Un valor no numérico es un error, nunca un cero silencioso.

## internal/dispenser/batidos/sign.go

### (archivo)

> Package batidos implementa la integración con Batidos Powermix (API abierta de
> comercio de Batidos).
> 
> Este archivo cubre solo el contrato de red que se puede verificar sin
> credenciales: la firma del request y el manejo de fechas. El cliente que llama
> a los endpoints se construye encima de esto.

### chinaTime

> chinaTime es UTC+8. China no tiene horario de verano, así que una zona fija es
> exacta y evita depender de que el sistema tenga tzdata instalado.

### campaignLayout

> campaignLayout es el único formato de fecha que acepta el endpoint de campaña.

### KeyMD5

> KeyMD5 devuelve MD5(key + secret + timestamp) en hex minúscula, como exige el
> header key-md5. La concatenación no lleva separadores.

### TimestampMillis

> TimestampMillis es el header timestamp: epoch en milisegundos. Es un instante
> absoluto, así que la nota del proveedor sobre "huso horario del este (UTC+8)"
> no lo modifica.

### CampaignTime

> CampaignTime formatea un instante para startTime/endTime, que el proveedor
> interpreta como UTC+8. Mandar hora local desde Argentina adelantaría la
> campaña once horas y el canje sería rechazado.

### ParseCampaignTime

> ParseCampaignTime lee una fecha de campaña interpretándola como UTC+8.

### SignatureHeaders

> SignatureHeaders arma los tres headers que lleva todo request a Batidos.

## internal/dispenser/batidos/sign_test.go

### (archivo)

> Vectores calculados con `openssl md5`, independientes de esta implementación.

### (archivo)

> La concatenación cruda key+secret+timestamp no debe confundir componentes
> distintos.

### (archivo)

> La campaña de canje viaja en hora de China: un instante UTC se corre +8 horas.

## internal/dispenser/batidos/wire.go

### envelope

> envelope es el sobre común de la API: {code, msg, data}. code == 200 es éxito
> incluso si el data viene vacío.

## internal/dispenser/dispenser.go

### (archivo)

> Package dispenser define el puerto por el que el motor de órdenes maneja una
> máquina expendedora.
> 
> El motor nunca debe enterarse de que una bebida requiere un código de canje
> mientras un snack se despacha de forma directa: desde acá los dos se ven
> igual. Cada proveedor implementa esta interfaz en su propio subpaquete
> (batidos, snacks).

### LineState

> LineState es el ciclo de vida de un producto dentro de una orden. Las dos
> líneas de un combo evolucionan de forma independiente.

### const

> LineInDoubt significa que no sabemos si el producto salió. Nunca se
> reintenta a ciegas: primero se reconcilia (ver Reconcile).

### MachineRef

> MachineRef identifica una máquina física y las direcciones con las que
> responde en cada proveedor.

### MachineRef

> El tipo es `machine.Kind`; sus valores son «batidos» y «snacks».

### MachineRef

> La familia de la máquina se llama `Kind` y no `Type` porque ya tiene su propio tipo —`machine.Kind`—: un campo `Type` de tipo `Kind` se lee al revés, y `Type` sugería que ahí vivía el tipo de dispositivo, que es otra cosa y es `DeviceTypeID`.

### MachineRef

> Batidos

### MachineRef

> Batidos

### MachineRef

> Batidos

### MachineRef

> Snacks

### Availability

> Availability es la respuesta del chequeo previo: es lo que habilita o no el QR.

### Request

> Request es un producto a despachar, correspondiente a una línea de la orden.

### Request

> goodsId de Batidos o productId de Snacks

### DispatchResult

> DispatchResult es la respuesta inmediata a Dispatch. "Aceptado" nunca significa
> "entregado".

### DispatchResult.ProviderRef

> orderNo de Batidos o subOrderId de Snacks

### Status

> Status es la verdad reconciliada sobre una línea.

### Dispenser

> Check informa si la máquina puede vender en este momento, antes de cobrar.

### Dispenser

> Dispatch le pide a la máquina que prepare o entregue el producto.

### Dispenser

> Reconcile le pregunta al proveedor qué pasó realmente con un intento
> ambiguo. Nunca despacha: solo lee.

## internal/dispenser/snacks/client.go

### shipmentPath

> shipmentPath es la ruta del despacho remoto. v7 corresponde a las regiones
> fuera de China continental; el PDF dice v6 para China. Es un contrato del
> proveedor y no se renombra.

### defaultTimeout

> defaultTimeout acota una llamada suelta. No es el timeout de negocio de dos
> minutos (S16): eso lo resuelve el motor de órdenes con el ctx que nos pasa.

### maxResponseBytes

> maxResponseBytes evita que una respuesta gigante o rota nos consuma memoria.

### ShipmentRequest

> ShipmentRequest es un despacho: nuestros identificadores, la máquina y los
> productos de la orden que salen en esa máquina.

### ShipmentRequest

> PaymentOrderID es nuestro id de orden, no un id del proveedor. El PDF no
> aclara si el proveedor lo trata como clave de idempotencia: sigue siendo un
> hueco abierto.

### ShipmentRequest

> MID identifica a la máquina de snacks (dispenser.MachineRef.MID).

### ShipmentRequest

> Products son las líneas de la orden que esta máquina debe despachar.

### ShipmentProduct

> ShipmentProduct es una línea de la orden dentro del despacho.

### ShipmentProduct

> OrderProductID es nuestro id de línea.

### ShipmentProduct

> ProductID es el productId del proveedor.

### ShipmentProduct

> RetailPrice es el precio de venta de esa línea. El PDF no dice si liquida
> algo o si es sólo informativo: hueco abierto.

### APIError

> APIError es un rechazo del proveedor: la API respondió y dijo que no. Los
> fallos de red y de contexto no son APIError.
> 
> Code trae el código propio del proveedor cuando la respuesta lo incluye
> (40001 falta un header obligatorio, 40003 timestamp fuera de la ventana,
> 40004 firma inválida, 40005 nonce repetido) y queda en 0 si no vino.

### Option

> Option ajusta el cliente en el constructor.

### WithHTTPClient

> WithHTTPClient reemplaza el cliente HTTP. Sirve para inyectar proxies, un
> transporte propio o timeouts distintos en los tests.

### WithClock

> WithClock reemplaza el reloj que alimenta el header timestamp. Con el reloj
> fijo se puede probar la ventana de ±5 minutos sin depender de la hora real.

### Client

> Client es el cliente de la API abierta de snacks. Sólo sabe hablar con el
> proveedor: no reintenta, no espera y no loguea (los reintentos son de T9).

### NewClient

> NewClient arma un cliente con timeout de 10s y reloj del sistema.

### Ship

> Ship pide el despacho de los productos de una orden en la máquina indicada.
> 
> Accepted significa "despacho encolado", nunca "entregado": el resultado real
> llega por callback (R5). Un error de red deja el intento en duda, no en
> fallido.

### Ship

> Los headers se arman en cada llamada, nunca se guardan ni se reusan:
> SignatureHeaders genera un nonce y un timestamp nuevos. Un reintento (T9)
> vuelve a llamar a Ship y por lo tanto estrena nonce; reusar el de un
> intento anterior se rechaza como replay (40005) durante diez minutos.

### Ship

> La API puede rechazar con HTTP 200 y code distinto de 200, y también con un
> status HTTP de error. Los dos casos son rechazos del proveedor. Un code 0
> significa que la respuesta no trajo código, no un error. La regla completa
> vive en isRejected y la comparte con get.

### body

> body traduce el pedido al JSON del proveedor.

### subOrderID

> subOrderID rescata el subOrderId si la respuesta lo trae. El ejemplo de éxito
> del PDF devuelve `result: {}`, así que en la práctica suele venir vacío y la
> referencia real aparece recién en el callback.

### devicesPath / slotsPath

> Las dos lecturas del chequeo previo de snacks: la página de máquinas dice si el equipo existe, está en línea y se puede vender; la lista de canales dice si el producto pedido tiene unidades. v7 por la misma razón que el despacho: la documentación pide v7 fuera de China continental.

### OnlineStatus / SellStatus / FaultState / LockType / StockoutState

> Cinco preguntas distintas sobre la misma máquina, y por eso cinco tipos: conexión (0/1), venta suspendida o prohibida (1/2/3), avería o sobretemperatura (0/1/2), bloqueo (sólo el 0 está documentado; cualquier otro valor bloquea y se expone crudo para no inventar nombres) y máquina entera sin stock (0/1). Una máquina en línea puede tener la venta suspendida: son ejes independientes.

### Device / DevicesPage / Slot / SlotStatus

> `DevicesPage` se expone paginada porque el proveedor exige Page y PageSize: no hay lectura de «todas». `Slot` informa stock y estado del canal por separado a propósito: son dos motivos distintos por los que un canal no puede vender. `SlotStatus` 255 no es un error de red, es un canal que el proveedor no reconoce.

### Devices

> Lee una página de máquinas, opcionalmente filtrada por DeviceCode. La query NO entra en la firma: la cadena canónica cubre sólo la ruta, y firmar el endpoint con la query produce 40004 en todas las llamadas.

### Slots

> Lee los canales de una máquina. El result es una lista, no una página: el proveedor no la pagina. Sin DeviceCode el proveedor no sabe de qué máquina hablar, así que en la práctica se pasa siempre. Devuelve lista vacía, nunca nil, cuando no hay canales.

### get

> Firma la ruta pelada y arma la URL con la query **después** de firmar: son dos cosas distintas y confundirlas invalida toda la llamada. El mensaje de un rechazo se toma del sobre y, si no vino, del cuerpo crudo, porque un 400 del proveedor puede no ser JSON.

### APIError

> El mensaje dice «la API rechazó la llamada» y no «el despacho», porque el tipo lo comparten el despacho y las dos lecturas del chequeo previo.

### isRejected

> Decide si una respuesta es un rechazo del proveedor: status HTTP fuera de 2xx, `code` distinto de 0 y de 200, o `success` presente y en `false`. Un `success` ausente no rechaza, porque la sección general de la documentación (`ys-vending.txt:351-363`) responde sin ese campo; sólo un `false` explícito cuenta. Sin esta última condición, `{"success":false,"code":0}` se tomaba como despacho encolado. Es una sola función para que `Ship` y `get` no puedan divergir.

## internal/dispenser/snacks/client_test.go

### (archivo)

> Este test vive en el paquete externo `snacks_test` a propósito: importa
> `internal/fakeapi`, que a su vez importa `snacks`. Desde adentro del paquete
> eso sería un ciclo de importación.

### newFake

> newFake arma el servidor falso real detrás de httptest. Ninguno de los flujos
> principales usa un stub escrito a mano: el falso es el que impone el contrato
> de red (firma, nonce, campos obligatorios).

### TestShipAcceptedAndRecordedByTheFake

> TestShipAcceptedAndRecordedByTheFake cubre el camino feliz: un producto, la
> máquina acepta y el falso registra el despacho.

### TestShipTwiceWithTheSameOrderUsesFreshNonces

> TestShipTwiceWithTheSameOrderUsesFreshNonces prueba que cada llamada arma sus
> headers de nuevo: el falso rechaza con 40005 un nonce repetido, así que dos
> llamadas exitosas solo son posibles con dos nonces distintos.

### TestShipWithWrongSecretReturnsAPIError

> TestShipWithWrongSecretReturnsAPIError comprueba el error tipado: el falso
> responde 401 con el código de firma inválida del proveedor.

### TestShipSendsTheProviderFieldNames

> TestShipSendsTheProviderFieldNames captura el cuerpo crudo con un handler
> propio y verifica los nombres de campo que dicta el PDF del proveedor.

### TestShipSendsTheProviderFieldNames

> El PDF lista AccessKey como parámetro del body además del header; se manda
> en los dos lados mientras el proveedor no aclare cuál corresponde.

### TestShipSendsTheProviderFieldNames

> Los nombres del proveedor van tal cual: ni snake_case ni camelCase.

### TestShipWithSkewedClockReturnsAPIError

> TestShipWithSkewedClockReturnsAPIError prueba que el timestamp sale del reloj
> inyectado: con un reloj diez minutos atrás, el falso responde 40003.

### TestShipTreatsHTTP200WithErrorCodeAsRejection

> TestShipTreatsHTTP200WithErrorCodeAsRejection cubre el rechazo que el PDF
> documenta con HTTP 200 y `code` distinto de 200: es un rechazo del proveedor,
> no un error de red. Este caso no lo produce el servidor falso, así que se
> arma con un handler mínimo.

### TestShipTreatsExplicitSuccessFalseAsRejection / TestDevicesTreatsExplicitSuccessFalseAsRejection

> Un `success: false` explícito con `code: 0` es un rechazo, tanto en el despacho como en las lecturas que pasan por get. Se cubren los dos caminos porque los dos llaman a isRejected desde sitios distintos.

### TestShipAcceptsAnEnvelopeWithoutSuccess

> Fija el comportamiento previo: una respuesta sin `success` y con `code: 0`, como la de la sección general de la documentación, sigue siendo un despacho aceptado. Si alguien endurece la regla a «exigir `success: true`», este test lo frena.

### TestShipRespectsCancelledContext

> TestShipRespectsCancelledContext confirma que la llamada respeta el ctx: con
> el contexto ya cancelado no se llega al servidor y no hay error tipado de API.

## internal/dispenser/snacks/sign.go

### (archivo)

> Package snacks implementa la integración con Snacks Powermix (API abierta de
> Snacks).
> 
> Este archivo cubre solo el contrato de red que se puede verificar sin
> credenciales: la firma HMAC, el nonce y la ventana de tiempo.

### maxSkew

> maxSkew es la tolerancia del proveedor. Convierte la sincronización del reloj
> en un requisito funcional, no en una prolijidad operativa.

### Canonical

> Canonical es la cadena exacta que se firma:
> 
> 	MÉTODO \n RUTA \n timestamp \n nonce
> 
> Los parámetros de query y el cuerpo no forman parte de ella.

### Sign

> Sign devuelve Base64(HMAC-SHA256(secret, cadena canónica)).

### TimestampMillis

> TimestampMillis es el header timestamp: epoch en milisegundos, 13 dígitos.

### NewNonce

> NewNonce devuelve un nonce nuevo de 32 caracteres hex en minúscula. Cada
> llamada necesita uno nuevo: repetirlo se rechaza como replay durante diez
> minutos.

### SignatureHeaders

> SignatureHeaders arma el juego completo de headers para un request. Un
> reintento debe volver a llamar a esto: necesita un nonce y un timestamp
> nuevos.

### VerifySignature

> VerifySignature revisa presencia de headers, ventana de reloj y firma. Sólo lo
> usa el servidor falso local. Nuestro callback de snacks **no** lo usa: la
> documentación del aviso de despacho (`ys-vending.txt:2023-2075`) describe
> sólo el cuerpo y ningún header de firma, y exigir una firma que el proveedor
> no manda rechazaría todos sus avisos. El callback se autentica sólo con el
> secreto de la ruta.

## internal/dispenser/snacks/sign_test.go

### (archivo)

> Vectores calculados con `openssl dgst -sha256 -hmac` + base64, independientes
> de esta implementación.

### (archivo)

> La cadena canónica tiene un orden y separadores exactos: cambiar el orden
> cambia la firma.

### (archivo)

> El nonce debe ser hex de 32 caracteres sin guiones y no repetirse.

## internal/dispenser/snacks/wire.go

### shipmentBody

> shipmentBody es el cuerpo tal como lo dicta el PDF del proveedor. Los nombres
> de campo son del proveedor y no se traducen ni se cambian de estilo.

### shipmentBody.AccessKey

> AccessKey se manda también en el cuerpo únicamente porque la tabla de
> parámetros del PDF está desalineada y lo lista como parámetro del body
> además del header. Esa duplicación existe sólo por la ambigüedad del
> documento y es una de las preguntas abiertas con el proveedor. Si la
> respuesta confirma que va sólo en el header, se quita de acá.

### envelope

> envelope es la respuesta del proveedor. El ejemplo de error del PDF devuelve
> `code` y `message`; el de éxito repite `code: 200` con `result` vacío.
> `Success` es un puntero porque el campo a veces no viene: `nil` significa
> «no vino» y es distinto de `false`, que es un rechazo explícito.

### devicesResultBody / deviceItemBody / slotItemBody

> Copian la página de máquinas y los canales tal como llegan. page, pageSize, total, totalPages, hasPrevPage y hasNextPage se pasan verbatim: el proveedor ya los calcula y recalcularlos es una forma de mentirle al llamador.

### scalarText

> Acepta un string o un número y lo deja como string. La documentación no fija el tipo de slotId, slotNo ni productId, y un identificador no debería romper la lectura por cómo vino escrito. Es el mismo problema que resuelve stateNumber en batidos, al revés.

## internal/fakeapi/batidos.go

### (archivo)

> Package fakeapi provee sustitutos locales de los dos backends de máquinas.
> 
> Existen para poder ejercitar el flujo completo sin credenciales ni máquinas
> reales, y para que el contrato de red (firmas, reloj, códigos de un solo uso,
> replay de nonce) falle en un test y no delante de un cliente.
> 
> Reflejan lo que dice la documentación. Donde la documentación calla, el falso
> no inventa comportamiento: queda anotado en el código.

### Batidos

> Batidos es el sustituto local de la API de comercio de Batidos.

### Advance

> Advance simula el callback del proveedor moviendo una orden a un estado nuevo
> (1 preparando, 2 completado, 0 falló).

### checkSignature

> checkSignature replica la autenticación documentada: key, timestamp y
> MD5(key+secret+timestamp). La documentación no describe una ventana de tiempo
> para esta API, así que acá no se exige ninguna.

### submitCampaign

> El proveedor lee estas dos fechas como UTC+8. Una ventana construida con
> hora local es una ventana en el futuro, así que se valida a propósito.

### submitCampaign

> "操作成功" es literalmente el mensaje que devuelve el proveedor: se
> conserva para que el falso se parezca al original.

### produceCoffee

> preparando: aceptado no es entregado

## internal/fakeapi/fakeapi_test.go

### TestBatidosHappyPathFromCampaignToBrewedDrink

> 1. Crear la campaña devuelve un código de canje.

### TestBatidosHappyPathFromCampaignToBrewedDrink

> 2. Canjear el código acepta el pedido.

### TestBatidosHappyPathFromCampaignToBrewedDrink

> 3. Consultar reporta "preparando".

### TestBatidosHappyPathFromCampaignToBrewedDrink

> 4. El callback avanza a "completado".

## internal/fakeapi/snacks.go

### Snacks

> Snacks es el sustituto local de la API abierta de snacks.

### ServeHTTP

> Anti-replay: un nonce es de un solo uso durante diez minutos.

### ShipmentCount

> ShipmentCount informa cuántos pedidos de despacho se aceptaron para una orden.

### SetResult

> SetResult simula el callback asincrónico de despacho para todos los productos
> de una orden: estado 1 significa que salió, 0 que falló con un motivo.

### (chequeo previo)

> Los dos servidores falsos ahora también sirven las lecturas del chequeo previo, con la misma verificación de firma y anti-replay que el despacho: un nonce es de un solo uso también en las lecturas, si no una firma capturada podría repetir la consulta indefinidamente. El ruteo es por ruta exacta, y un POST a una ruta documentada como GET responde 405: es error del llamador, no «ruta desconocida».

> Los setters (`SetDeviceOffline`, `SetSellStatus`, `SetSlotStock`, etc.) existen para provocar las formas en que una máquina no está vendible sin escribir respuestas a mano. Todo arranca sano: el falso sólo miente cuando un test se lo pide.

> El test que más importa del fake es el que firma la ruta **con** la query y exige un 40004. Si algún día el falso empezara a aceptarla, el cliente podría firmar mal sin que ningún test lo note.

## internal/logging/logging.go

### (archivo)

> Package logging centraliza cómo se construyen los loggers de la aplicación.
> 
> Regla de estilo del proyecto: el mensaje del log y el valor del atributo
> "error" se muestran con mayúscula inicial, para que la lectura y la búsqueda
> sean directas. Las cadenas de error del código se mantienen en minúscula,
> como manda la convención de Go (ST1005), porque los errores se envuelven
> dentro de otras frases: "no se pudo abrir: archivo ausente" se lee bien,
> "no se pudo abrir: Archivo ausente" no.
> 
> Por eso la mayúscula se aplica acá, en el borde de salida, y no en el origen.

### New

> New devuelve un logger de texto para consola, con el nivel indicado.

### NewWithOptions

> NewWithOptions devuelve un logger de texto que capitaliza el mensaje y el
> error, conservando el ReplaceAttr que haya definido el llamador.

### NewWithOptions

> copia: nunca se mutan las opciones del llamador

### Capitalize

> Capitalize pone en mayúscula la primera letra de una cadena y respeta UTF-8.
> Si la cadena está vacía o empieza con algo que no es una letra minúscula
> (un dígito, un signo, una mayúscula) la devuelve sin cambios.

## internal/logging/logging_test.go

### (archivo)

> El mensaje y el valor del error salen con mayúscula inicial, pero el resto de
> los campos no se toca.

### (archivo)

> Un ReplaceAttr propio del llamador debe seguir ejecutándose: el capitalizador
> se compone, no reemplaza.

### NewWithOptions

> NewWithOptions no debe mutar las opciones del llamador.

## internal/machine/machine.go

### (archivo)

> Package machine declara el único tipo de la familia de máquina del sistema.
> No importa nada del proyecto y no tiene funciones ni métodos: es vocabulario
> compartido, no comportamiento.

### Kind

> Kind es el tipo de máquina que atiende un producto del combo.
>
> Vive en un paquete propio porque lo nombran todas las capas —configuración,
> catálogo, puerto del despachador, store, motor, HTTP— y hasta acá cada una lo
> declaraba por su cuenta. El tipo no cambia ningún dato guardado: sus valores
> son el vocabulario que ya viajaba en el archivo del catálogo y en las columnas
> `combo_item.machine_type` y `order_line.machine_type`.

### const

> Batidos y Snacks, con los valores que ya están escritos en el JSON del
> catálogo y en las columnas SQL. Cambiarlos sería un cambio de datos, no de
> código.

## internal/notify/notify.go

### (archivo)

> Package notify aísla cómo llega una excepción hasta una persona.
> 
> El transporte sigue abierto (S18): hoy escribe una línea de log estructurada,
> y más adelante se puede enchufar un emisor de Telegram o WhatsApp sin tocar el
> motor de órdenes.

### Event

> Event es una excepción que merece la atención de una persona.

### Event

> por ejemplo "line_in_doubt", "line_failed", "partial_delivery"

### LogNotifier

> LogNotifier escribe el evento como una línea de log estructurada.

## internal/payments/refund.go

### (archivo)

> Package payments aísla el lado del dinero detrás de un puerto.
> 
> El proveedor de QR todavía no está elegido y la política de reembolso sigue
> abierta (S17). Las dos cosas viven detrás de esta interfaz para que el motor
> de órdenes se pueda construir y probar sin ninguna de ellas: el mock registra
> la intención y no mueve nada.

### RefundRequest

> RefundRequest es una línea a devolver, nunca una orden completa: el combo
> puede quedar entregado a medias.

### RefundResult

> RefundResult informa por separado qué se aceptó y, explícitamente, si el
> dinero se movió.

### MockRefunder

> MockRefunder registra la intención y nunca mueve dinero. Mientras no haya
> proveedor elegido y política definida, esta es la única implementación
> honesta.

## internal/payments/refund_test.go

### (archivo)

> El mock existe para no bloquear el motor mientras no haya proveedor de pagos:
> registra la intención de devolución pero nunca mueve dinero.

## migrations/0001_init.sql

### kiosk
> Esquema inicial del sistema de combos en muebles expendedores.
> 
> PostgreSQL. Se aplica con: psql "$DATABASE_URL" -f migrations/0001_init.sql
> Las claves primarias son text porque las genera la aplicación.
> El dinero se guarda como centavos enteros, nunca como punto flotante.

### machine
> Una máquina de cada tipo por mueble: un combo siempre empareja una bebida con
> un snack, así que el par es la clave natural.

### payment_intent_provider_intent_idx
> El webhook del proveedor de pagos puede llegar más de una vez; la misma
> intención nunca debe procesarse dos veces.

### orders
> Se llama "orders" porque ORDER es una palabra reservada en SQL.

### order_line
> Estado global, derivado de las líneas. "partial" e "in_doubt" existen
> porque las dos máquinas fallan de forma independiente.
> La unidad de ejecución: un producto, una máquina, su propio ciclo de vida.

### order_line (columna)
> orderNo de Batidos o subOrderId de Snacks

### order_line (columna)
> código de canje usado por esta línea, solo batidos

### order_line_state_deadline_idx
> Vencimiento de la línea. Una línea despachada que no tiene respuesta para
> esa hora no queda fallida sino in_doubt: hay que reconciliarla antes de
> cualquier otra cosa.
> Sirve al worker de reconciliación: buscar líneas vencidas.

### dispatch_attempt
> Auditoría de cada llamada hecha a una máquina. Sin esto, un timeout nos deja
> sin poder saber si el producto salió.

### webhook_event
> Webhooks entrantes en crudo, guardados antes de procesarse y deduplicados por
> la clave de evento del propio proveedor. Una repetición debe ser inofensiva,
> no una segunda bebida.

### catalog_snapshot
> Última foto conocida de lo que cada máquina puede vender; la usa el chequeo
> previo que habilita el QR. machine_key es "<kiosk_id>:<machine_type>".

## internal/preflight/checker.go

### (archivo)

> Package preflight responde una sola pregunta: ¿este mueble puede vender este combo ahora mismo, y si no, por qué no?
>
> Nace de S10 (sin conexión o sin producto, la venta se bloquea) y de S8 (el QR se genera solo cuando todo está sano). No cobra, no dispara y no decide qué hacer con un error: eso es del motor de órdenes.

### Batidos / Snacks (interfaces)

> Son lo que este paquete necesita de cada API, y nada más. Las declara el consumidor a propósito: así el chequeo no arrastra el cliente ni su configuración, y se prueba entero sin HTTP.

### Request

> Identifica las dos máquinas y los dos productos de un combo fijo (S3). BatidosSKU es el goodsId del catálogo: el mismo identificador que después exige produceCoffee, así que un catálogo que responde no garantiza que el canje vaya a aceptarlo.

### Reason

> Una causa de bloqueo, como cadena estable y legible por máquina. Es string y no int para que una razón desconocida se pueda leer y para no romper nada si falta una.

### ReasonBatidosSKUSoldOut

> La comparación es `soldOut != 0` y no `soldOut == 1` porque la documentación del proveedor dice 0/1 pero sus propios ejemplos devuelven -1. Ante el valor ambiguo se bloquea antes que prometer: es conservador a propósito. Cuando el proveedor aclare el -1, este es el único lugar a cambiar.

### ReasonSnacksSlotNotSellable

> El SKU tiene stock, pero solo en slots que no están en estado normal. No se reporta como «sin stock» porque sería falso: lo que falta es un slot vendible.

### Result

> El veredicto y la lista completa de causas. Sellable es verdadero solo cuando la lista está vacía, y las razones se acumulan: el operador necesita ver todos los problemas de una vez, no el primero.

### Checker / New

> Consulta las dos máquinas para un combo, sin estado ni caché: cada llamada es una foto nueva. Recibe interfaces y no clientes concretos para que el motor de órdenes pueda sustituirlos en las pruebas.

### Check

> Consulta los cuatro datos siempre, incluso cuando la primera máquina ya falló, y recién después decide. Un error de proveedor o de red se devuelve como error junto con un Result vacío; nunca se disfraza de razón. La regla de negocio es del llamador: acá no se decide no vender, solo se informa que no se pudo averiguar. Los dos errores se juntan con errors.Join para no perder el de la segunda máquina.

### checkBatidos

> Revisa estado, funcionamiento e insumos del dispositivo y después el producto en el catálogo, que solo vende si está listado y no agotado. Todos los estados se comparan contra «normal» y no contra una lista de valores malos: un código nuevo del proveedor bloquea, no se interpreta como sano.

### checkSnacks

> Revisa primero el dispositivo en la página y sus cinco estados, después los slots del producto. La página es 1 con tamaño 10: el mueble tiene una sola máquina de snacks. Si el código no aparece, se devuelve solo «no encontrado» y no se derivan razones de slot, porque los slots de un código inexistente no describen este mueble; igual se consultan, para que un error del proveedor no quede escondido detrás de la razón.

### slotReasons

> Decide si algún slot puede entregar el SKU y, si ninguno puede, cuál de las tres causas es la verdadera: no está cargado, tiene stock en slots no vendibles, o está agotado. El orden importa y son excluyentes: evaluar stock y estado por separado daría «vendible» cuando un slot tiene el estado y otro el stock, y ningún slot puede entregar nada.

## internal/dispenser/batidos/dispenser.go

### (archivo)

> Adaptador entre el puerto `dispenser` y la API abierta de Batidos. Traduce las tres preguntas del puerto a las llamadas que el proveedor realmente tiene: estado de la máquina, emisión de campaña más canje, y consulta del canje.
>
> No reintenta, no hace backoff y no loguea: el presupuesto de 2 minutos por línea y la política de reintentos son del motor de órdenes (T9).
>
> La aserción `var _ dispenser.Dispenser = (*Dispenser)(nil)` es deliberada: si el puerto cambia, esto deja de compilar.

### defaultCampaignWindow / WithCampaignWindow

> Diez minutos de vigencia para el código de canje. El código se crea en el instante del pago y se canjea en el mismo instante: la ventana es sólo un margen para que el canje no llegue vencido ni quede vivo para siempre.
>
> Sólo se acepta una ventana positiva. Cero o negativa produciría una campaña con endTime igual o anterior a startTime, que el proveedor rechaza por contrato: se conserva el valor por defecto antes que enviar algo inválido.

### WithCampaignName

> El nombre de la campaña es texto administrativo que aparece en el panel del proveedor, y lo exige no vacío. Un nombre vacío conserva el valor por defecto.

### WithNotifyURL

> La URL de aviso se arma por orden, porque el callback de batidos lleva el `orderId` en el path y el proveedor guarda la URL en la campaña: cada línea se crea con la suya. Una función vacía se ignora, y sin la opción la campaña viaja con `notifyUrl` vacío, exactamente como antes de T10b.
>
> El adaptador no conoce la forma de la URL ni el secreto: sólo llama a la función que le inyectan. Cambiar la ruta, el secreto o el hosting queda en `cmd/api`, no acá.

### Check

> Chequeo a nivel máquina: fuera de línea, avería y sin insumos, en ese orden. Los tres ejes se comparan contra las constantes documentadas, nunca contra números crudos. Reason queda vacío cuando la máquina está sana y, cuando no, enumera sólo los ejes que fallaron ("avería; sin insumos"), para que el motivo sea legible sin abrir el panel del proveedor.
>
> Deliberadamente no mira el producto: si el SKU del combo está listado y con stock es la regla de `internal/preflight`, que sí lee el catálogo.

### Dispatch

> Dos llamadas por bebida, en este orden, porque así funciona Batidos (R2): emitir la campaña que genera el código de canje (S6, un código por compra) y canjearlo enseguida. `exchangeType 1` significa un único producto, que es exactamente lo que es un combo fijo.
>
> El reloj es el del cliente y no uno propio: así `WithClock` vuelve determinista la ventana y adaptador y cliente comparten una sola noción de «ahora». La ventana se construye con instantes absolutos; el cliente la formatea en UTC+8 (R3).
>
> `DispatchResult.ProviderRef` es el código de canje, no un número de pedido: produceCoffee no devuelve orderNo, y el código es exactamente lo que `Reconcile` necesita para preguntar qué pasó.
>
> Si la campaña falla no hay código, así que el DispatchResult queda vacío. Si la campaña se emitió y el canje falla, el DispatchResult conserva el código aunque `Accepted` sea falso: un `ProviderRef` no vacío junto al error es la señal de «esto es reconciliable, no lo reintentes a ciegas» (E4). Perder ese código sería perder la única forma de saber si el batido salió.
>
> `notifyUrl` sólo se completa cuando `cmd/api` inyecta `WithNotifyURL`; el adaptador no inventa ninguna URL ni un valor por defecto.

### Reconcile

> Traduce el status de getOrderInfo con las constantes del cliente: 0 falló, 1 en preparación, 2 completado. Cualquier otro valor queda como `LineInDoubt` con el número informado en Reason: el enum no está documentado, así que un valor nuevo significa «no se sabe», y declarar `LineFailed` sería afirmar algo que el proveedor no dijo.
>
> `Status.ProviderOrderNo` es siempre el orderNo del proveedor, también en el caso fallido: es lo que identifica el pedido en la conciliación y en el panel.
>
> Si el proveedor rechaza la consulta, se devuelve el error con un Status vacío: el puerto no inventa un estado cuando no tuvo respuesta.

## internal/dispenser/batidos/dispenser_test.go

### (archivo)

> Los tests corren contra el servidor falso real detrás de `httptest` y un `Client` de verdad, porque el adaptador no tiene lógica propia que valga aislar: lo que hay que probar es la traducción contra el contrato del proveedor.
>
> Viven en el paquete externo `batidos_test` por el mismo motivo que `client_test.go`, y reutilizan sus ayudantes (credenciales, reloj fijo, `newTestClient`) porque comparten paquete: si ese archivo se renombra, estos tests se rompen.
>
> Los dos handlers propios cubren lo que el falso no puede representar: un status fuera del enum documentado (que debe quedar en duda, no en fallo) y la captura del cuerpo de la campaña para fijar la ventana enviada.

## internal/dispenser/snacks/client.go — Orders / Order

> La tercera lectura de snacks y la única que mira el pasado: la página de pedidos. `Page` y `PageSize` son obligatorios para el proveedor, así que se envían siempre; `OrderNo` sólo si viene. Se reutiliza `get`, que firma la ruta pelada.
>
> Sólo se modelan los nueve campos que la documentación muestra. Faltan a propósito `shipmentStatus`, `shipmentStatusName` y `shipmentReason`, que son los únicos que dirían si el snack salió: sin ellos el adaptador no puede pronunciarse, y eso es exactamente lo que pasa.
>
> `OrderStatus` se expone crudo, como número y con su nombre al lado. La tabla del PDF se desalinea al extraerse, así que cualquier constante con nombre sería una adivinanza disfrazada de contrato. `OrderAmount` y `PayAmount` son float64 porque el proveedor los declara Decimal: para mostrar y mandar, nunca para hacer cuentas.

## internal/dispenser/snacks/dispenser.go

### (archivo)

> Adaptador entre el puerto `dispenser` y la API abierta de snacks. Traduce las tres preguntas del puerto a las tres lecturas que el proveedor tiene: página de máquinas para el estado, despacho remoto para disparar, página de pedidos para reconciliar.
>
> No reintenta, no hace backoff y no loguea: el presupuesto de 2 minutos y la política de reintentos son del motor (T9). La aserción `var _ dispenser.Dispenser = (*Dispenser)(nil)` hace que un cambio en el puerto rompa acá y no en el motor.

### Check

> Chequeo a nivel máquina: fuera de línea, venta suspendida, avería, bloqueada y sin stock, en ese orden. Los cinco ejes se comparan contra la constante «normal» y no contra una lista de valores malos: un código nuevo del proveedor bloquea en vez de pasar como sano.
>
> La máquina se identifica por `MID` —el único identificador que la configuración exige para snacks— y se confirma por `DeviceCode` exacto sobre lo devuelto: el filtro de la query es una ayuda, no una garantía. Si el proveedor no informa la máquina se responde «máquina no encontrada» y sin error: no poder vender no es un fallo del proveedor. Un error de red o de API sí vuelve como error y nunca se disfraza de razón.
>
> Deliberadamente no mira el producto: si el SKU está en un canal vendible es la regla de `internal/preflight`.

### Dispatch

> Una sola llamada, no dos: snacks no tiene código que canjear (a diferencia de R2 en batidos), así que el despacho remoto es todo el disparo. `PaymentOrderId` es el id de la orden y `Mid` la máquina.
>
> `DispatchResult.ProviderRef` es lo que el proveedor haya contestado, normalmente vacío porque la documentación responde `result: {}`. No se rellena con el id de la orden: un ProviderRef vacío dice la verdad —«el proveedor no dio un asa»— y es por eso que `Reconcile` cae al OrderID.

### centsToUnits

> Convierte centavos enteros al decimal que exige `retailPrice`. Es dinero de exhibición: float sólo para mandar y loguear, nunca para cuentas. Adentro del sistema todo importe sigue siendo int64 en centavos.

### Reconcile

> Busca el pedido por `transactionOrderNo` igual al asa que tenemos (`req.KnownRef`, o el id de la orden cuando el ProviderRef quedó vacío). No se manda `OrderNo` como filtro a propósito: el filtro del proveedor es por `orderNo` y el asa que tenemos es el `transactionOrderNo` del pago; mandarlo excluiría justamente el pedido buscado. El match es local y exacto.
>
> Este método no puede dar un veredicto, y es deliberado. El único estado que trae la página de pedidos es el estado del PEDIDO —ciclo de pago—, no el resultado del despacho. Declarar entregado afirmaría algo que el proveedor no dijo; declarar fallido sería peor, porque dispararía un reintento y podría sacar dos snacks. Por eso siempre responde `LineInDoubt`: «el proveedor no informa el pedido» si no aparece, o el número y el nombre crudos del estado si aparece. La verdad por producto llega por callback (T10, R5).
>
> Si el proveedor rechaza la consulta se devuelve el error tal cual y con un Status vacío: el puerto no inventa un estado cuando no tuvo respuesta.

## internal/dispenser/snacks/dispenser_test.go

> Los dos tests que más importan son los de `Reconcile`: uno exige que un pedido conocido quede en duda con el estado del proveedor en el motivo, y el otro que un error del proveedor no se convierta en un estado inventado. Es el contrato que hereda T9.
>
> La máquina de prueba usa `MID = "snacks-dev-1"`, el mismo código que sirve el falso, porque para snacks `Mid` y `deviceCode` son la misma identidad: es lo que hace el propio proveedor en la página de pedidos.

## internal/fakeapi/snacks.go — handleOrders / SetOrderStatus

> Sirve `GET /open/v7/order/page` con la misma firma y anti-replay que el resto, y exige Page y PageSize igual que el proveedor. Los pedidos no se cargan a mano: se derivan de los despachos ya registrados, porque un pedido sin despacho no existiría en este sistema. `transactionOrderNo` es el PaymentOrderId que se despachó, que es el asa con la que el adaptador busca.
>
> El estado del pedido es configurable porque el enum real no es legible en la documentación: el falso no finge saber qué significa cada número, sólo repite el que se le pida.

## internal/store

### (archivo)

> Puerto de persistencia del motor, y el vocabulario que se guarda: orden, línea, intento y webhook.
>
> Es un puerto y no un driver porque el motor tiene que poder escribirse y probarse sin base de datos. Las columnas, los CHECK y los índices únicos de `migrations/0001_init.sql` son el contrato que este paquete refleja.

### OrderStatus

> Espeja el CHECK de la tabla `orders`. «partial» e «in_doubt» existen porque las dos máquinas fallan de forma independiente: un pedido no puede decir «delivered» cuando salió un solo producto.

### Order

> La cabecera de la compra: estado, importes, fechas e identidad del mueble y del combo. `Lines` viaja con ella porque una orden sin líneas no existe en el sistema, pero escribirlas no es tarea de la orden: `SaveOrder` guarda sólo la cabecera.
>
> `Version` lleva la cuenta de las escrituras de la cabecera y **la mueve sólo el store**: una orden nace en cero y cada `SaveOrder` aceptado la devuelve con la versión siguiente. No es un dato del dominio; existe porque la cabecera también tiene más de un escritor —dos `/pay` concurrentes, el `settle` de los callbacks y el del barrido— y sin número el último en escribir gana, exactamente el problema que `Line.Version` resolvió para las líneas.

### Line

> La unidad de ejecución. La familia de la máquina vive en un solo lugar: `Machine.Kind`. Antes la línea la guardaba dos veces —`MachineType` por un lado y la `MachineRef` por otro— y nada obligaba a que coincidieran: una línea podía decir «snacks» y llevar los identificadores del batido, y el motor elegía el driver de una máquina y le mandaba la identidad de la otra. Con un solo campo la incoherencia no se puede escribir: no compila.
>
> `State` reusa `dispenser.LineState` a propósito: el estado de una línea es el mismo que informa el puerto de las máquinas, y duplicarlo obligaría a traducir entre dos enums idénticos en cada borde.
>
> `Ref` tiene dos significados según la máquina —código de canje en batidos, asa del proveedor en snacks— porque el SQL tiene dos columnas y el motor nunca necesita las dos a la vez.
>
> `DeadlineAt` en cero significa «sin plazo armado», no un plazo vencido en el año 1.
>
> `Version` lleva la cuenta de las escrituras de esa línea y **la mueve sólo el store**: una línea recién creada nace en cero y cada `SaveLine` aceptado devuelve la línea con la versión siguiente. No es un dato del dominio: existe porque el worker de reconciliación y los callbacks escriben la misma línea desde goroutines distintas, y sin número el último en escribir gana.

### Attempt

> Auditoría de una llamada a una máquina; sin esto un timeout nos deja sin saber si el producto salió. `Outcome` «ambiguous» es el caso E4 y es el único que prohíbe reintentar a ciegas. `IdempotencyKey` es única en todo el sistema: es la garantía de que un mismo pago no produce dos entregas.

### Webhook

> `Source` usa el vocabulario del dominio y del CHECK de `webhook_event`: «payment», «batidos» o «snacks». La pareja (Source, DedupeKey) es la identidad del evento: una repetición del proveedor tiene que ser inofensiva, no una segunda bebida.

### ErrStale

> El conflicto de escritura, y el mismo error sirve para la línea y para la orden: alguien escribió después de que esta copia se leyera. Significa algo distinto según quién escribe —para el callback, alguien con más autoridad ya resolvió la línea; para el worker, la línea ya tiene dueño; para el segundo `/pay`, el cobro ya está registrado—, y por eso la reacción la decide el dominio y no el store. El mensaje dice el hecho —«el registro cambió desde que se leyó»— y no una acción, porque la reacción correcta no la puede decidir el store. Se compara con `errors.Is` desde el motor y nunca se muestra a un cliente.

### Store

> `CreateOrder`/`LoadOrder` mueven la orden entera con sus líneas, porque las líneas nacen sólo junto a su orden. `SaveOrder` toca únicamente la cabecera. `SaveLine` actualiza una línea suelta —lo que hace el worker al vencer un plazo— y nunca crea: una línea sin orden sería un producto sin quien lo pague. `RegisterWebhook` responde «¿es nuevo?», que es justo lo que el handler necesita. `DueLines` responde la pregunta del worker: qué venció.
>
> `SaveLine` recibe una línea con su versión y **devuelve la línea persistida con la versión nueva**: es un guardado condicional, no un reemplazo. Si la versión recibida no es la que tiene el store, devuelve `ErrStale` y no escribe nada; si coincide, guarda con `Version+1`. Devolver la línea y no sólo un error es lo que evita que el motor tenga que releer para la escritura siguiente, y es la diferencia entre un puerto que compite por la última escritura y uno que la rechaza.
>
> `SaveOrder` guarda sólo la cabecera y **lleva versión**, igual que `SaveLine`: recibe la orden con la versión que se leyó y devuelve la orden persistida con la versión siguiente, o `ErrStale` si alguien escribió antes. Las líneas quedan fuera a propósito, y siguen fuera con versión: escribirlas convertiría el guardado de la orden en una segunda escritura sin condición sobre las mismas filas que el callback y el worker se disputan. Devolver la orden y no sólo un error es lo que permite encadenar el guardado siguiente —`ConfirmPayment` guarda dos veces— sin releer la cabecera. `AppendAttempt`/`SaveAttempt` tampoco llevan versión, y no por omisión: la clave de idempotencia ya es única en todo el sistema y un intento, una vez registrado, no se reescribe. Un número de versión ahí protegería una escritura que no existe.

## internal/store/memory.go

> Implementación en memoria: permite escribir y probar el motor de punta a punta sin driver. No es un caché: es la implementación de referencia del puerto —la que define el comportamiento observable— y por eso impone las mismas reglas que impone la base —unicidad de orden, de clave de idempotencia y de dedupe— en vez de ser permisiva. `internal/store/postgres` aplica esas mismas reglas en SQL.
>
> Un solo mutex protege el estado entero: las operaciones duran microsegundos y un lock único elimina el deadlock por orden de adquisición.
>
> `cloneOrder` es la regla central: ni al guardar ni al leer se comparte el slice de líneas, así que quien llama no puede cambiar el estado interno mutando lo que recibió. Devuelve nil si el slice era nil, porque redondear a vacío haría fallar una comparación sin que nada haya cambiado.
>
> `putOrderLocked` reconstruye el índice de líneas al reemplazar la orden: sin borrar las entradas viejas, un `SaveOrder` que quita una línea dejaría el índice apuntando a algo que ya no existe.
>
> `RegisterWebhook` devuelve `first` y no un error de duplicado: un callback repetido es el caso normal de un proveedor que reintenta, no un fallo.
>
> `DueLines` ordena después de soltar el lock, por plazo ascendente y con el ID como desempate. Ese desempate no es cosmético: el recorrido del mapa es aleatorio y un worker con tope tendría resultados no deterministas. Sólo entran `sent` y `brewing`: `in_doubt` se reconcilia por su propio camino, y volver a despacharlo sería el reintento a ciegas que E4 prohíbe.
>
> `SaveLine` compara la versión con el lock tomado: si no coinciden devuelve `ErrStale` sin tocar el slice, y si coinciden escribe `Version+1` y devuelve la línea guardada. La comparación y la escritura ocurren dentro del mismo lock, que es lo único que convierte el número en una garantía y no en una sugerencia. Nunca se escribe «sólo algunos campos»: la versión protege la línea entera, y por eso el llamador tiene que traer la copia completa que leyó.
>
> `SaveOrder` aplica la misma comparación a la cabecera dentro del mismo lock: si la versión no coincide devuelve `ErrStale` sin tocar la copia guardada, así que un guardado viejo no puede revertir ni el estado ni las tres fechas; si coincide guarda con `Version+1` y devuelve la orden recibida con ese número, que es el que encadena el llamador.
>
> `CreateOrder` normaliza a cero la versión de cada línea antes de guardar, sobre una copia para no mutar lo que recibió. No es cosmético: sin eso un llamador podría crear una línea con versión inventada y quedar inmune a los conflictos durante toda su vida. La versión de la cabecera, en cambio, se guarda tal como llega: una orden nueva llega en cero y corregirla acá escondería un error de quien llama en vez de evitarlo.

## internal/store/memory_test.go

> Los tests viven en el paquete externo `store_test` porque prueban el contrato del puerto desde afuera, que es como lo va a usar el motor.
>
> Los que más importan no son los de camino feliz: uno fija que el store nunca comparte estado con quien llama —sin la copia, el motor podría cambiar una línea sin guardarla y el bug aparecería como una reconciliación fantasma—, otro fija que dos líneas con el mismo plazo salen siempre en el mismo orden, y el de concurrencia crea una orden por goroutine y después exige leerlas todas: es la única forma de que un mapa sin protección se note como corrupción de conteo y no como un test que pasa por suerte.
>
> Los dos de la versión de la orden trasladan ese mismo contrato a la cabecera: el guardado aceptado devuelve `Version+1` y es lo que se lee después, y el de una copia vieja devuelve `ErrStale` dejando intactos el estado, `paid_at`, `closed_at` y la versión.
>
> Los cuatro de la versión de la línea fijan el contrato del guardado condicional y no el camino feliz: una versión vieja devuelve `ErrStale` **y la línea guardada queda intacta** —se compara la línea entera contra la que se había guardado, no sólo el estado—, la versión correcta devuelve `Version+1` y persiste lo mismo que devolvió, dos guardados partiendo de la misma copia terminan con el segundo rechazado, y `CreateOrder` deja toda línea en cero aunque se le pase una versión inventada.

## internal/order/status.go

### DeriveOrderStatus

> El estado de la orden es una función de sus líneas, no un dato que alguien escriba a mano. El orden de las reglas es la política:
>
> 1. Sin líneas: `pending_payment`.
> 2. Cualquier línea `pending`, `sent` o `brewing`: `dispatching`. El vuelo gana sobre cualquier resultado ya conocido de la otra línea, porque la orden no terminó.
> 3. Todas entregadas: `delivered`.
> 4. Nada entregado y todas falladas: `failed`. Es el único estado en que «no se entregó nada» es un hecho (E2/E3).
> 5. Nada entregado y algo en duda: `in_doubt`. La duda domina al fallo, porque un fallo es conocimiento y una duda no (E4).
> 6. Algo entregado y algo fallado: `partial`, porque el faltante está demostrado.
> 7. Algo entregado y el resto sólo en duda: `in_doubt`. `partial` afirmaría un faltante conocido que no existe.
>
> `refunded` y `cancelled` no salen nunca de acá: los fija el motor. Un estado de línea no reconocido tampoco cuenta como entregado ni fallado, así que cae en las ramas de duda.

### CanOrderTransition

> Grafo explícito y cerrado: lo que no está listado es ilegal. `pending_payment` sale a `paid` o a `cancelled`; `paid` sólo avanza a `dispatching` (S8 exige disparar en el acto); `dispatching` termina en el resultado de sus líneas; `in_doubt` sólo se revisa por reconciliación (S16) y, si la reconciliación nunca llega a un dato, un humano puede devolver: por eso **`in_doubt → refunded` es legal**. Sin esa arista, una orden en duda quedaría encerrada para siempre, y con snacks en duda permanente hasta que exista el callback, ése sería el camino normal y no un borde.
>
> `failed`, `partial` y `delivered` sólo van a `refunded` (S17). `refunded` y `cancelled` son terminales: revivirlas inventaría una entrega o un cobro sin respaldo. `from == to` no es arista; el no-op lo resuelve `AdvanceOrder`.

### CanLineTransition

> `pending` sólo pasa a `sent`. `sent` puede ir a `brewing` o directo a `delivered`, `failed` o `in_doubt` (R5: aceptar no es entregar). `brewing` sólo termina en `delivered`, `failed` o `in_doubt`. `in_doubt` se resuelve con dato, nunca reenviando. `delivered` y `failed` son terminales: un retroceso permitiría dos entregas o dos devoluciones por el mismo pago.
>
> Nada vuelve a `pending`: significa que todavía nadie habló con la máquina. El reintento de S7 se modela como **intento nuevo sobre la misma línea**, no como retroceso de estado: por eso `failed → sent` no es una arista.

### AdvanceOrder / AdvanceLine

> Derivan el objetivo y lo validan contra el grafo. Si el objetivo es igual al actual devuelven el actual sin error: el motor deriva en cada vuelta del despacho y un no-op no es una transición. Si la arista no existe devuelven el estado actual intacto y `ErrInvalidTransition`, para que un llamador que ignore el error no quede con un estado inventado.

## internal/store — SaveAttempt

> `AppendAttempt` inserta y falla si la clave ya existe; `SaveAttempt` cierra el intento ya grabado y **nunca crea**. La distinción no es cosmética: el motor registra el intento antes de llamar a la máquina para que un corte en medio del despacho deje evidencia, y después necesita anotar cómo terminó. Si el cierre pudiera insertar, un error de orden de llamadas inventaría intentos que nunca ocurrieron.

## internal/order/engine.go

### (archivo)

> El motor: crea la orden cuando el cliente elige un combo y dispara las dos máquinas en paralelo en el instante en que se confirma el pago (S2, S8). Vive sobre dos puertos —`store.Store` y `dispenser.Dispenser`— y no conoce ni HTTP ni base de datos. El vocabulario de estados es de `status.go`; acá sólo se decide qué hacer con cada resultado.

### Draft / Item

> `Draft` es lo que la tablet manda antes de que exista una orden. `Item` lleva la máquina en un solo lugar: `Item.Machine`, y el tipo de máquina sale de `Item.Machine.Kind`. La orden guarda la referencia completa en `store.Line.Machine`, así que el motor no retiene nada en memoria y un reinicio no le quita con qué hablarle a la máquina.

### WithLineDeadline

> Sólo acepta valores positivos: cero o negativo produciría un plazo ya vencido en el instante del despacho, que es peor que no tener plazo. `WithClock` y `WithIDGenerator` ignoran `nil` para que un test no deje el motor sin reloj ni sin ids.

## internal/order/place.go

### El tipo de máquina como fuente única

> Antes el tipo estaba dos veces —`Item.MachineType` y la familia de la `MachineRef`— y `validateDraft` sólo miraba el primero: un ítem con `MachineType: "snacks"` y `MachineRef{Kind: "batidos", DeviceID: ...}` pasaba la validación, el motor elegía el driver de snacks y le mandaba la identidad de la máquina de batidos. El resultado era un rechazo del proveedor con un error que no explicaba nada.
>
> Se arregló por la vía limpia y no con una validación de coincidencia: dos campos que deben coincidir es una regla que se puede olvidar, un campo solo no. Ahora la incoherencia no se puede escribir —no compila—, que es más fuerte que cualquier chequeo en ejecución.
>
> Lo mismo pasaba un escalón más abajo, en la línea: `store.Line` guardaba `MachineType` y además la `MachineRef` completa. Se borró el campo de la línea, no se lo validó: la línea lleva sólo `Machine` y todo el motor lee `line.Machine.Kind` —el mapa de drivers del despacho, la reconciliación y las dos reglas de correlación del callback—. `catalog.Item.MachineType` y `order.Callback.MachineType` se quedan, porque no son la identidad de una máquina: el primero dice qué familia pide un combo en el menú y el segundo qué máquina habla en el aviso.
>
> `validateDraft` exige que `item.Machine.Kind` no esté vacío y que ese tipo exista en el mapa de drivers, que es el mismo mapa con el que `dispatchLine` busca la máquina después: lo que se valida en el alta y lo que se usa en el despacho es la misma fuente, así que un borrador aceptado no puede terminar en «no hay una máquina configurada para el tipo».
>
> Riesgo residual anotado: `machineIdentified` sigue aceptando **algún** identificador, así que `Machine.Kind: snacks` con un `DeviceID` de batidos es expresable. Ya no hay dos fuentes de tipo, pero la identidad no se valida contra el tipo.
>
> No se agregó ninguna validación nueva en el store. `validateDraft` ya rechaza un ítem sin familia y en PostgreSQL el `CHECK` de `machine_type` es la última red; sumar un chequeo en memoria duplicaría esa regla en un tercer lugar. El store en memoria queda más permisivo que Postgres —acepta una línea sin familia— y es una asimetría conocida y aceptada, no un olvido.

### Place

> Valida el borrador entero antes de crear nada: si un ítem está mal, no queda una orden a medio armar. El mensaje nombra la posición del ítem, porque el llamador necesita saber cuál corregir. La orden nace `pending_payment` con una línea `pending` por ítem y los importes copiados; el dinero es int64 en centavos en todo el sistema.
>
> La línea se arma copiando la `MachineRef` del ítem y nada más: no hay un `MachineType` aparte que setear ni que pueda desincronizarse.

## internal/order/payment.go

### ConfirmPayment

> Es el único disparador del despacho y tiene que ser inofensivo si el proveedor de pagos repite el webhook: si la orden ya no está `pending_payment`, responde nil sin tocar nada.
>
> La orden se escribe dos veces. Primero `paid` con `PaidAt`, que es la evidencia del cobro, sobre la versión que se acaba de leer. Después `dispatching`, partiendo de la orden que devolvió el primer guardado —con su versión nueva—: una línea sólo sale de `pending` por `sent`, así que sin ese estado intermedio una orden cuyas dos líneas fallan no tendría arista legal hasta `failed`. Recién entonces se lanza una goroutine por línea, cada una escribiendo su propio slot del slice de errores — sin lock compartido.
>
> Esa versión es lo que separa dos `/pay` concurrentes. Si el choque llega en el guardado a `paid`, la respuesta es nil y nada más: el otro `/pay` ya registró el cobro y va a despachar, así que reintentar sería cobrar dos veces o despachar dos veces. Si llega en el guardado a `dispatching`, en cambio, el error se devuelve y no se despacha: sin ese estado no hay arista legal hacia la entrega.
>
> Al final se **recarga** la orden con `refreshOrderStatus`, que deriva el estado con `AdvanceOrder` sobre las líneas leídas. Derivar de la copia en memoria que las goroutines mutaron sería leer exactamente aquello que se quería verificar.
>
> Sólo los errores de store vuelven de acá; el fallo de una máquina no es un error de infraestructura sino un resultado que se persiste.

## internal/order/dispatch.go

### dispatchLine

> El driver se elige por `line.Machine.Kind`, y por eso el mapa de drivers del motor y la familia escrita en la línea son el mismo dato: antes el despacho leía `line.MachineType` y mandaba `line.Machine` en el `dispenser.Request`, así que una línea incoherente elegía un driver y le pasaba la identidad del otro.
>
> El orden de las llamadas es la garantía contra la doble entrega: primero se registra el intento como `ambiguous`, después se llama a la máquina, y al final se cierra el intento y se guarda la línea. Si el proceso muere en el medio, queda un intento `ambiguous` —la evidencia de que hubo una llamada— y la línea sigue `pending`.
>
> La clave es `orden:línea:número de intento`, con el número sacado de `Attempts+1`. Si `AppendAttempt` dice que ya existe, ese intento ocurrió en una vida anterior del proceso: no se despacha, no se toca la línea, se devuelve nil. Es la razón por la que un reinicio no puede sacar dos productos.
>
> `Attempts` se incrementa en **todo** intento registrado, no sólo en los aceptados: si un rechazo no lo moviera, el reintento de T9 reusaría la clave y el store lo leería como replay, prohibiendo el reintento que S7 exige.
>
> La línea se guarda con `SaveLine` y nunca con `SaveOrder`: dos goroutines escribiendo la orden entera se pisarían.
>
> El `SaveLine` del despacho es condicional y puede responder `ErrStale`: significa que otro escritor —un callback, típicamente— tocó la línea mientras la llamada a la máquina estaba en vuelo. En ese caso se devuelve nil sin revertir nada y **sin volver a escribir**: el efecto del despacho ya ocurrió y su estado no lo puede decidir el despachador, lo resuelve la reconciliación con lo que diga el proveedor. Reintentar el guardado acá sería exactamente el «último que escribe gana» que motivó la versión, y revertir un cierre es irreversible. El cierre del intento se registra **antes** de guardar la línea, así que un conflicto no pierde la evidencia de la llamada.
>
> La línea que devuelve `SaveLine` se vuelve a usar dentro del bucle: es la que trae la versión nueva, y sin quedarse con ella el reintento del fallido fallaría por versión vieja contra su propio guardado anterior.

### classifyDispatch

> Tres desenlaces, y la diferencia es qué sabemos del producto: aceptado es «la máquina lo tomó» (R5: todavía no es entregado, por eso la línea vence y hay que reconciliar); rechazado es «la máquina dijo que no»; cualquier otra cosa es «no sabemos», y se marca `in_doubt` y nunca `failed` (E4), porque declarar fallido habilitaría un reintento a ciegas que podría sacar dos productos.
>
> El `ProviderRef` se copia a la línea aunque venga vacío: el asa del proveedor es opcional, pero perderla cuando existe sería perder la única forma de reconciliar.

## internal/order/reconcile.go

### transitionLine

> `status.go` permite una sola salida desde `pending`: `sent`. Un rechazo o una duda significan que la línea **sí** se despachó, así que el estado se recorre `pending → sent → failed|in_doubt` y se persiste sólo el estado final, con una escritura.

## internal/order/helpers_test.go

> `recordingStore` envuelve la memoria para poder ver lo que el puerto no deja leer: qué intentos se registraron, con qué clave y cómo se cerraron. Sin ese registro no habría forma de afirmar que el intento se grabó antes de la llamada ni que terminó en `rejected` o `error`.
>
> El test que sostiene la regla transversal es el de reinicio: pre-siembra el intento con la clave exacta que el motor va a calcular y comprueba que la máquina no recibe ninguna llamada y que la línea sigue `pending`.

## internal/dispenser — Rejection

> Un rechazo es un hecho del dominio, no un accidente de transporte: el proveedor contestó y dijo que no. Vive en el puerto para que el motor distinga «la máquina dijo no» (E2, la línea queda `failed`) de «no sabemos» (E4, la línea queda `in_doubt`) sin importar el paquete de ningún proveedor. `Provider` viaja como texto porque el motor lo escribe en la línea y en el intento, y ahí no debe depender de un tipo de proveedor; `HTTPStatus` y `Code` se conservan crudos porque son lo que va a necesitar quien lea el log del proveedor.
>
> El mensaje nombra siempre proveedor y código, porque termina persistido en `Line.LastError` y en `Attempt.Detail`: se lee semanas después, sin la respuesta HTTP a mano, y «rechazado» a secas no dice quién rechazó ni con qué código.

## internal/store — Line.Machine

> La referencia de la máquina es un dato de la línea, no del proceso. Antes vivía en un mapa en RAM del motor y eso rompía dos cosas reales: después de un reinicio una línea `pending` no se podía despachar, y un `Place` en una instancia con un `ConfirmPayment` en otra no funcionaba.
>
> Se guarda como un único campo con la `MachineRef` entera, no como cuatro strings sueltos, porque la referencia viaja junta al `dispenser.Request` y separarla obligaría a reconstruirla en cada borde. Sigue siendo un struct de strings, así que la copia por valor del slice de líneas sigue aislándose sin código nuevo.

## internal/order/place.go — cambios

> `Place` copia la máquina del ítem a la línea antes de persistir: es lo que convierte la orden en algo autosuficiente. El motor ya no retiene nada en memoria, así que puede perder el proceso entero entre el cobro y el despacho sin perder con qué hablarle a la máquina.

## internal/order/dispatch.go — cambios

> `dispatchLine` toma la máquina de la línea. Un reinicio es indistinguible de una llamada normal: sólo cambia de dónde vino el store. Ya no existe el caso «no se conoce la máquina» porque la línea no puede nacer sin máquina: `validateDraft` lo impide en `Place`.
>
> `isProviderRejection` pregunta por el error del puerto y no por los tipos de cada proveedor. Ese era el punto: el motor no debe conocer que Batidos y Snacks existen, y con los dos imports cualquier proveedor nuevo obligaba a editar el motor para clasificar sus rechazos.

## internal/dispenser/{batidos,snacks} — asPortRejection

> Traduce el `*APIError` propio a `*dispenser.Rejection` en el borde: en `Dispatch` y en `Reconcile`, que son los dos lugares por donde sale una respuesta del proveedor hacia el motor. Se envuelve después con `%w` para no perder el contexto, y por eso `errors.As` sigue encontrando el rechazo.
>
> Un error que no es del proveedor —red, JSON roto, contexto cortado— pasa intacto: no es un rechazo, y convertirlo en uno haría que una duda se declarara fallo.

## internal/order/payment_test.go — el test del reinicio

> Es el test que prueba que la deuda está pagada: el motor que cobra no es el que despacha. Un segundo `order.New` sobre el mismo store no comparte ni memoria ni mapa de referencias con el primero, así que si la máquina no estuviera guardada en la línea, el despacho no tendría con qué hablarle. Antes de mover la referencia al store, esto fallaba con «no se conoce la máquina de la línea».

## internal/order/dispatch.go — el reintento del fallido (T9a): `defaultMaxAttempts` y `dispatchLine`

### TestEngineConfirmPaymentKeepsARetriedLinePendingInsteadOfFailed

> Registra cada `SaveLine` (con `recordingStore.lineStatesFor`) y exige la secuencia `[pending, sent]` para una línea rechazada una vez y aceptada después. Mira las escrituras y no sólo el estado final porque el defecto era justamente un estado intermedio visible para otros escritores.

### CanLineTransition: `failed` es terminal

> `failed` no tiene salida: es el resultado del último rechazo, cuando ya no quedan intentos. Antes existía la arista `failed -> sent` para reintentar, y eso dejaba guardado un `failed` pasajero: un `settleOrder` concurrente (callback o barrido) podía verlo, marcar la orden `failed` y avisar `order_failed`, y la orden quedaba trabada porque `failed` sólo admite `refunded`. Un rechazo con intentos pendientes deja la línea en `pending`, que es exactamente lo que es: la máquina todavía no aceptó nada. La garantía anti-duplicado no es el estado, es la **clave de idempotencia**: un reintento es un intento nuevo con clave nueva, no un replay del anterior.
>
> `delivered` sigue siendo terminal (el producto ya salió) e `in_doubt` no puede volver a `sent` (E4: no se sabe si salió, y reintentar a ciegas puede entregar dos veces). Esa línea se reconcilia, no se reintenta.

### defaultMaxAttempts / WithMaxAttempts

> Dos intentos por línea: el inicial más un reintento del fallido (S7). Es el mínimo que cumple el pedido y la cantidad queda configurable porque la política de cuántos reintentos (E2) sigue abierta. Un valor menor que 1 se ignora: nunca se deja al motor sin poder despachar.

### dispatchLine — el bucle

> Un bucle acotado sobre **la misma** línea. En cada vuelta se arma el número de intento (`Attempts + 1`) y su clave, se **registra el intento como `ambiguous` antes de llamar** —así un corte en medio deja constancia de que algo pudo haber salido—, se llama, se clasifica, se cierra el intento y recién ahí se mueve la línea.
>
> Se detiene cuando: la línea ya gastó sus intentos; la clave ya existe (intento repetido tras un reinicio); fue aceptada; quedó en duda; o fue rechazada sin intentos restantes.
>
> Sólo vuelve a iterar ante un rechazo explícito y con intentos disponibles (`retrying`): la línea conserva su estado `pending`, se guarda **una sola vez** con el intento contado y el motivo en `LastError`, y la vuelta siguiente empieza con número y clave nuevos. Nunca se guarda `failed` antes de un reintento. En Batidos eso es un código de canje nuevo, porque el anterior probablemente quedó consumido (R2).
>
> E4 no entra nunca acá: sin respuesta no se sabe si el producto salió, y el reintento a ciegas puede producir dos entregas.

## internal/order — el worker de reconciliación (T9b): `reconcile.go`, `settle.go` y `dispatch.go`

### Presupuesto de tiempo

> 2 minutos esperando respuesta y 2 minutos más preguntando cada 30 segundos: **4 minutos en total**. No es una elección técnica, es del dueño del producto: una persona parada frente a un mueble en un gimnasio no espera más que eso, y ningún caso legítimo de las máquinas pasa de 60 segundos.
>
> `DeadlineAt` es **cuándo preguntar** (se mueve); `ReconcileUntil` es **cuándo dejar de preguntar** (fijo). Los dos se arman con el mismo `now` al aceptar el despacho, para que el total sea exactamente la suma y no «la suma más lo que tardó el loop».

### dispatchLine — la duda también arma plazos

> Cuando el despacho queda en duda (E4) la línea arma sus plazos igual que cuando fue aceptada: **no hay nada que esperar** porque ya sabemos que no hubo respuesta, así que la primera consulta va al intervalo (30 s) y el tope a la ventana (2 min). Antes de esto la línea quedaba en duda **sin plazo**, y como `DueLines` sólo miraba `sent` y `brewing`, ese caso —el más común de todos— no lo reconciliaba nadie nunca.

### lineDue

> También devuelve líneas `in_doubt` cuando tienen un plazo armado. Una línea cerrada en duda al agotarse el tope tiene los dos plazos en cero, así que deja de aparecer sola: el «en duda» que se sigue consultando es el que todavía tiene presupuesto, no el que ya se rindió.

### ReconcileDue / reconcileLine

> El worker que cierra E4: lee las líneas vencidas y le pregunta al proveedor qué pasó realmente con cada una. Las procesa de a una, en serie: un worker corre sobre una cadencia y el manejo de fallos se lee de arriba abajo. `limit` es la perilla — una máquina lenta retrasa el lote entero.
>
> Los errores no cortan el lote: se acumulan y se devuelven juntos, para que una línea que falló no deje sin reconciliar a las demás. Un error de consulta no pierde la línea: se trata como «sigue en duda» y se agenda el próximo intento.
>
> `reconcileLine` busca el driver con `line.Machine.Kind`, igual que el despacho, y por eso una línea que se reconcilia después de un reinicio elige exactamente el mismo driver que la despachó.

### extendReconcile

> Decide el próximo instante de consulta y, cuando ya no queda margen, cierra la línea en duda. Tres casos y los tres terminan: sin ventana armada se cierra en el acto; con ventana abierta se empuja un intervalo **recortado al tope** si el intervalo se pasaría; en el tope o después se cierra, limpia los dos plazos y guarda el motivo.
>
> El recorte no es cosmético: garantiza que el último escaneo caiga exactamente en el tope y nunca después, y es lo que convierte «preguntar hasta tener respuesta» en «preguntar hasta tener respuesta **o** hasta que se acabe el tiempo».
>
> Si el guardado responde `ErrStale` devuelve `false, nil` y nada más: ni reprograma ni cierra. La línea ya tiene dueño, y el que la tiene acaba de escribir un hecho más nuevo que el que traía esta copia. Reprogramar le rearmaría un plazo a una línea que quizá ya está cerrada —y eso incluye reabrirla si la copia vieja decía `sent`—, así que el silencio es la única respuesta honesta.

### closeLine / settleOrder / refreshOrderStatus / notifyOrder

> Cerrar es limpiar los dos plazos: una línea cerrada no vuelve a aparecer en `DueLines`, y eso es lo que impide interrogarla después de que alguien ya sabe la respuesta. Entregado limpia `LastError`; fallido conserva o reemplaza el motivo, nunca lo borra.
>
> `closeLine` es el llamador que **reacciona distinto** ante `ErrStale`, porque cerrar es irreversible y el que llega segundo no puede ganar por llegar segundo. Relee la orden, busca la línea por `ID` y decide: si ya está cerrada —entregada o fallida— no hace nada y devuelve nil, porque alguien con más autoridad la resolvió; si sigue abierta reintenta **una sola vez** la transición con la versión fresca. Una sola vez y no un bucle: si el segundo intento también encuentra la línea cambiada, hay un tercer escritor y volver a probar es una carrera sin final. Nunca se reabre ni se reescribe un cierre ajeno, y el `Ref` adoptado por el callback sobrevive porque la copia vieja simplemente no se escribe.
>
> Una línea cerrada no arregla la orden sola: si el worker no recalcula el estado, la orden se queda en `dispatching` para siempre. `refreshOrderStatus` es ese recálculo y ahora es compartido: recarga, avanza con la misma tabla de transiciones del resto del motor y guarda sólo si cambió.
>
> La cabecera también compite —el callback de snacks, `ConfirmPayment` y el barrido escriben la misma orden—, así que el guardado lleva versión y `refreshOrderStatus` reintenta **una sola vez** ante `ErrStale`: recarga y vuelve a derivar. Un segundo `ErrStale` se devuelve como error, porque un tercer escritor significa que reintentar es una carrera sin final. El reintento es seguro acá, a diferencia de la línea: el estado no se inventa, se vuelve a derivar de las líneas frescas.
>
> `settleOrder` es `refreshOrderStatus` más el aviso; la cola de `ConfirmPayment` usa `refreshOrderStatus` a secas porque no avisa. Mismo cálculo, dos políticas.
>
> Un aviso por orden afectada, y sólo cuando el estado final es duda o fallo: es la excepción que necesita una persona, no un parte de novedades. El detalle va en español porque lo lee alguien del equipo, no un programa.

## migrations/0003_line_reconcile_until.sql

> Agrega `reconcile_until` a `order_line`. Aditiva a propósito: sin CHECK, sin índice y sin migración de datos. La columna es nullable y su ausencia es exactamente el «no armado» que ya conoce el código, así que las filas viejas no necesitan backfill. No lleva índice porque el worker no busca por ella: sigue buscando por `(state, deadline_at)`.

## internal/httpapi/handler.go

### (archivo)

> Los cuatro endpoints con los que habla la tablet: el catálogo, la creación de la orden, el estado en vivo y el pago. `Handler` es el contrato de la tablet: no sabe de proveedores ni de firmas, traduce HTTP a las tres piezas que ya existen —catálogo, motor y store— y traduce lo que vuelve a JSON. El contrato de los proveedores es otro tipo, `Callbacks`, con su propio constructor y su propio `Register`, y vive en `callbacks.go`.
>
> Los contratos de entrada y salida son nuestros, así que van en inglés como el resto de los identificadores, y los mensajes de error van en español porque los lee una persona del equipo.

### New

> Falla cerrado por dos motivos: sin catálogo, motor, confirmador o store no hay nada que atender, y sin muebles configurados no se podría resolver a qué máquinas pertenece una orden. Un mueble repetido tampoco se acepta: dos muebles con el mismo id harían que el despacho fuera ambiguo.
>
> `simulated` se deriva de que el entorno sea exactamente `sandbox`. Cualquier otro valor —incluido uno inesperado— deja el pago simulado apagado: es la única forma de que un error de configuración no termine registrando pagos que nadie cobró.

### Register

> Las rutas se declaran con patrón de método (`GET /combos`, `POST /orders/{id}/pay`), así que un método no soportado lo rechaza el `ServeMux` con 405 antes de tocar el store.

## internal/httpapi/orders.go

### createOrder / readOrder / payOrder

> Primero el guardia del entorno y después el motor: en producción la petición muere antes de que nada se registre.
>
> Si el motor devuelve error, no se responde 500 a ciegas. Se relee la orden: si ya no está pendiente de pago, el pago quedó registrado y lo que falló fue el despacho, así que la respuesta es 200 con el estado real (y el detalle en cada línea). Si sigue pendiente, el pago no se registró y ahí sí es un 500 que el cliente puede reintentar. Un 5xx en el primer caso sería una mentira que la tablet traduciría en un pago repetido.

## internal/httpapi/draft.go

### resolveKiosk / buildDraft

> El `kiosk_id` puede venir vacío sólo cuando hay un único mueble configurado: con varios, adivinar sería elegir por el cliente. Un mueble que no está en la configuración es 404, igual que un combo inactivo o inexistente.
>
> Un combo cuyos ítems piden una máquina que el mueble no tiene es 409: la petición es válida, el estado del mueble no permite venderla. No se inventa la máquina que falta.
>
> De la configuración del mueble se arma el `dispenser.MachineRef` completo —incluidos `device_id`, `device_no` y `mid`—, que es lo que después permite despachar sin volver a mirar la topología.
>
> El importe de cada línea queda en cero: el catálogo define un precio por combo, no por ítem, y repartirlo sería inventar un dato que nadie definió. El importe de la orden es el del combo.

### decodeCreateOrder

> El cuerpo se lee con `DisallowUnknownFields` y con un tope de tamaño, y se exige un único objeto JSON. Un cuerpo inválido no llega a crear nada.

## internal/httpapi/errors.go

### fail

> `fail` es el único lugar que decide el código de estado: los errores propios viajan como `apiError` con su status, `catalog.ErrNotFound` y `store.ErrNotFound` son 404, y el resto es 500 con el detalle sólo en el log, nunca en la respuesta. El `path` que va al log pasa por `redactCallbackPath`: el secreto de los callbacks viaja en la ruta y no puede quedar escrito.

## internal/httpapi/responses.go

### newOrderResponse

> `newOrderResponse` armado a mano —y no serializando `store.Order`— porque el contrato de la tablet es una decisión nuestra: no quiero que un campo nuevo interno del store se filtre a la app, ni que las fechas en cero viajen como `0001-01-01`.

## internal/order/callback.go

### (archivo)

> La entrada de los callbacks de las máquinas. Un callback es un hecho externo que llega tarde, repetido y sin orden: por eso el motor lo trata como una clave de deduplicación más una referencia a una línea, y nunca como una orden de cerrar algo.
>
> Hay dos formas y no es un capricho. En snacks el callback **es** la única fuente de verdad (su API no tiene consulta de suborden: `Reconcile` siempre devuelve duda), así que se cierra con lo que dice. En batidos el callback no viene firmado y el proveedor sí tiene `getOrderInfo`, así que la regla R6 manda confirmar antes de cerrar: el callback es sólo la señal de que hay algo nuevo que preguntar.

### beginCallback / callbackLine / recordCallback / lineClosed

> La línea se resuelve con tres reglas, de la más exacta a la más tolerante, y en ese orden porque el orden es la garantía: una coincidencia exacta nunca compite con una deducción.
>
> 1. `line.Ref == cb.Ref`, con `cb.Ref` no vacío. Es el camino de siempre.
> 2. `line.ID == cb.ThirdOrderNo` **y** `line.Machine.Kind == cb.MachineType`, con `cb.ThirdOrderNo` y `cb.MachineType` no vacíos. El proveedor de snacks documenta ese campo como «thirdparty suborder number» y es nuestro propio `OrderProductId`, el `LineID` que mandamos al despachar: sirve aunque el despacho no haya devuelto ningún id de suborden. El filtro de máquina no es adorno: los identificadores de línea los genera el motor, y un aviso de snacks que traiga el `ThirdOrderNo` de la línea de batidos la cerraría con el resultado de la otra máquina, que es irreversible.
> 3. La **única** línea de la orden cuyo `Machine.Kind == cb.MachineType`, y sólo si su `Ref` está vacío. Las candidatas son **todas** las líneas de esa máquina, con `Ref` o sin él: la referencia del callback ya falló en la regla 1, así que no puede decidir nada acá. Es la red para cuando el proveedor tampoco manda el `ThirdOrderNo`. No se adivina nunca: con más de una candidata se devuelve `ErrCallbackAmbiguous` sin mirar sus `Ref` —una línea sin referencia entre dos no es la candidata, es la mitad de una ambigüedad— y no se toca ninguna línea; con ninguna candidata, o con una sola ya referenciada, `ErrCallbackUnknownLine` como antes. Sin `MachineType` las reglas 2 y 3 no aplican en absoluto: elegir entre máquinas sería inventar el dato, así que ahí sólo queda la coincidencia exacta de la regla 1.
>
> El orden importa y costó una corrección: **primero el efecto, después el registro**. La primera versión registraba el callback como visto antes de aplicarlo; si el store fallaba en el medio, la clave quedaba quemada y el reintento del proveedor se descartaba, perdiendo el evento para siempre. Ahora un fallo transitorio devuelve error sin registrar nada, así que el reintento puede funcionar.
>
> La contracara es el caso que nunca va a poder aplicarse: una orden o una referencia que no conocemos —y también un callback ambiguo— se registra igual antes de devolver el error. Ese evento no se va a poder aplicar nunca, y sin registrarlo el proveedor lo reintentaría para siempre.
>
> Una línea cerrada no se toca ni se reabre: se recalcula el estado de la orden, se registra el evento y se devuelve acuse. La tabla de transiciones tampoco permite reabrir, así que una corrección del proveedor después del cierre se ignora a propósito: con dinero de por medio, preferimos una línea que no se mueve a una que se mueve sola.
>
> Un evento incompleto se detecta antes de tocar el store. Incompleto quiere decir sin origen, sin clave de deduplicación, sin orden o sin estado; una referencia vacía **no** lo hace incompleto, porque la tercera regla existe justamente para eso.

### lineByRef / lineByThirdOrderNo / lineByMachineType

> Las tres reglas, una por función, para que el orden se lea en `callbackLine` y no quede escondido dentro de un bucle. `lineByThirdOrderNo` exige que el aviso diga de qué máquina habla, porque el `ID` de una línea no dice nada del aviso que lo trae, y compara `line.Machine.Kind` contra `cb.MachineType`. `lineByMachineType` cuenta primero todas las líneas de esa máquina y recién después mira la referencia: devuelve la candidata sólo si es exactamente una y está sin `Ref`; dos o más es `ErrCallbackAmbiguous` sin importar sus `Ref`, y ninguna —o una sola ya referenciada— es `ErrCallbackUnknownLine`. Es la misma regla conservadora que el resto del proyecto: antes que suponer, se acusa recibo y se deja la línea quieta.

### adoptRef

> Cuando la línea resuelta tiene el `Ref` vacío y el callback sí trae referencia, se adopta antes de aplicar el estado, así el próximo evento de esa línea entra por la primera regla y no vuelve a depender de una deducción. No agrega ninguna escritura: el guardado lo hacen `closeLine` y `markBrewing`, que ya iban a guardar la línea. Por eso tampoco se adopta en la reconciliación: ahí el que manda es el proveedor.

### CloseFromCallback / ConfirmFromCallback

> `CloseFromCallback` aplica el estado que informa el proveedor: entregado, fallido con su motivo, o en elaboración. `ConfirmFromCallback` **ignora** el estado del callback para cerrar y consulta al proveedor; el único uso que le da al callback es el de "en curso", que sube la línea a elaboración porque es información cierta y visible para la tablet, sin poder cerrar nada.
>
> Los dos terminan recalculando el estado de la orden, y sólo después. Una línea cerrada no arregla la orden sola: sin ese paso la orden se quedaba en `dispatching` para siempre.

### markBrewing

> Devuelve si aplicó, y el llamador sólo toca el estado en memoria cuando aplicó. La primera versión forzaba `LineBrewing` aunque la transición fuera ilegal, y eso permitía que un callback «en curso» que llegaba tarde degradara una línea ya entregada: el reconcile posterior le escribía encima `in_doubt` o `failed`. Una transición ilegal ahora es silencio, no error: si el callback dice «en curso» cuando la línea todavía no salió, se deja como está y sigue mandando el worker.
>
> `ErrStale` también termina en `(false, nil)`: significa que otro escritor tocó la línea entre la lectura y el guardado, y en esa carrera «en curso» es siempre la información más débil —un cierre entregado o fallido la supera, y si el otro escritor sólo movió un plazo no hay nada que corregir—. Devolver error obligaría al handler a responder 500 y el proveedor reintentaría un aviso que ya no puede aplicar; devolver `false` deja que el flujo siga y que el que ganó mande.

## internal/httpapi/callbacks.go

### (archivo)

> Las dos rutas que reciben a los proveedores, con los cuerpos y las respuestas que cada documentación describe: `POST /callbacks/batidos/{secret}/{orderId}` y `POST /callbacks/snacks/{secret}`.
>
> El `orderId` va en la ruta de batidos porque su callback no trae ningún identificador nuestro salvo el código de canje, y el `notifyUrl` se entrega por campaña: la campaña se crea por línea, así que la URL puede llevar la orden. En snacks el identificador viaja en el cuerpo (`PaymentOrderId`, que es la orden que nosotros mandamos al despachar).

### NewCallbacks / CallbackSecrets

> Sin receptor no se registran las rutas: un servidor al que nadie le cableó los callbacks responde 404, que es lo mismo que ve un atacante. Con receptor pero sin secreto configurado, las rutas existen y rechazan todo, también con 404. Las dos formas de fallar cerrado no se distinguen desde afuera.

### secretMatches

> Comparación en tiempo constante y rechazo cuando el secreto esperado está vacío. R6 pide autenticar el callback de batidos por ruta secreta porque no viene firmado; en snacks la documentación no dice cómo se autentica, así que se aplica la misma mitigación: la URL es el secreto y sólo la conoce el proveedor.

### batidosCallback

> El estado se traduce con una tabla explícita (0 fallo, 1 en curso, 2 completado) y un valor fuera de la tabla se acusa recibo **sin** tocarlo: no se cierra una línea por un código que no entendemos.
>
> La respuesta es exactamente la que el proveedor documenta, incluido el literal `操作成功`, porque no es texto para una persona: es el contrato de respuesta.
>
> La clave de deduplicación combina `orderNo`, `code` y `status`: un reintento del mismo evento se descarta, y el paso de "en curso" a "completado" del mismo código sí se procesa.

### snacksCallback

> Un solo callback trae varias subórdenes, y cada una es su propio evento: su propia clave (`orderNo|subOrderNo|shipmentStatus`), su propio estado y su propio motivo. Se aplican todas y se responde una sola vez.
>
> El evento lleva `ThirdOrderNo` y `MachineType` además de la referencia. Lo primero porque el proveedor documenta ese campo como el número de suborden de terceros, que es nuestro `OrderProductId`; lo segundo porque la línea todavía puede estar sin referencia —el despacho de snacks responde `"result": {}`— y la única forma de resolverla sin adivinar es saber de qué máquina habla el aviso.
>
> Los errores se acumulan con `errors.Join` y no se corta en el primero: si una suborden falla de verdad, la respuesta es 500 para que el proveedor reintente, y la deduplicación protege a las que ya se cerraron.

### writeCallbackResult / acknowledgeBrokenCallback / decodeCallback

> El mapeo de errores a código de respuesta es una decisión sobre el proveedor, no sobre nosotros: **sólo se devuelve error cuando reintentar puede ayudar**. Nada que cerrar (`ErrCallbackUnknownLine`, orden inexistente), un callback ambiguo (`ErrCallbackAmbiguous`), un cuerpo roto y un evento incompleto se acusan recibo con 200 y quedan en el log: son casos que no se van a poder aplicar nunca y un 4xx los volvería un reintento eterno. La ambigüedad se registra con nivel de error y no de aviso: dos líneas candidatas significan que algo se descartó mal, y una persona tiene que mirarlo. Lo demás es 500, que es el único caso donde el reintento sirve, y desde la corrección de arriba ya no hay clave quemada que lo inutilice.
>
> Los cuerpos de los proveedores se decodifican **sin** `DisallowUnknownFields`, al revés que el cuerpo de la tablet: sus contratos pueden crecer y no quiero que un campo nuevo rompa la recepción.
>
> Los tres logs de este bloque usan `redactCallbackPath` en lugar del path crudo: el secreto de la ruta nunca se escribe completo.

## internal/httpapi/callback_url.go

### BatidosCallbackURL

> Única fuente de la forma de la ruta del callback de batidos. Devuelve vacío si falta cualquiera de las tres piezas: una URL a medias haría que el proveedor avise a ninguna parte, y el silencio se descubre recién cuando un cliente se queda sin batido.
>
> La base se normaliza sin barra final y los dos segmentos variables se escapan con `url.PathEscape`, porque el secreto y el `orderId` viajan dentro del path. Si algún día cambia la ruta, acá y en el patrón de `Register` están los únicos dos lugares que hay que acompañar: nadie más arma la URL a mano.

### redactCallbackPath

> El secreto del callback viaja en el path, así que escribirlo completo en el log equivale a publicarlo: cualquiera con acceso al log puede forjar cierres de línea contra la máquina. Se conserva sólo el origen —lo que hace falta para saber qué proveedor avisó— y se tapa el resto.
>
> Cualquier otra ruta se devuelve intacta: la redacción es una regla de los callbacks, no un filtro general de logs.

## internal/config/validate.go — publicBaseURLProblems / callbackSecretProblems

> `public_base_url` es opcional, pero cuando está se valida: tiene que ser una URL absoluta con host, de esquema `http` o `https`, y **en producción tiene que ser `https`**. El motivo no es estético: esa URL viaja al proveedor dentro del `notifyUrl` con la ruta secreta del callback adentro, así que en claro el secreto queda expuesto en su log y en el de cualquier proxy. En sandbox se permite `http` a propósito, para poder probar con un túnel.
>
> El secreto de callback se valida contra la forma de la ruta: sólo letras, números, punto, guion y guion bajo, y nunca `.` ni `..`. Un secreto con `/` parte el path en más segmentos y un `.`/`..` se colapsa en la limpieza del path: los dos casos dejan el callback inalcanzable **en silencio**, con un 404 indistinguible de un secreto equivocado. Es preferible que el arranque falle a que los avisos no lleguen nunca y nadie se entere.

## internal/order/gate.go

### (archivo)

> El puerto por el que el motor pregunta si el combo se puede vender, sin saber quién contesta. El motor no importa `preflight` ni los drivers: conoce una interfaz de una sola función y un error de negocio, y el adaptador que traduce vive en `internal/gate`. Es la regla de dependencias del motor escrita en tipos: si mañana el chequeo cambia de proveedor, `internal/order` no se entera.

### Gate

> `Check` recibe el mismo `Draft` que se está por crear, no una consulta a medida, para que el adaptador pueda traducirlo sin que el motor le pase datos ya masticados. Devolver `nil` significa «se puede vender»: el contrato es de bloqueo explícito, no de permiso explícito.

### WithGate

> Ignora un `nil` a propósito. Con la puerta en `nil` el motor no llama a nadie, así que los tests y los llamadores que ya existían siguen comportándose igual: el chequeo previo se enciende cableándolo, no se apaga con un interruptor de configuración.

### ErrNotSellable

> El error de negocio, comparable con `errors.Is` desde cualquier capa. La capa HTTP lo traduce a 409 y no necesita saber nada de `preflight`: sólo pregunta si el error es esto.

### NotSellableError

> Lleva las razones para que el mensaje se entienda sin abrir el log, y las expone como campo para que la respuesta HTTP las pueda listar. `Unwrap` devuelve `ErrNotSellable`, así que `errors.Is` funciona y `errors.As` sigue dando acceso a las razones. Sin razones el mensaje es sólo la frase: un bloqueo que no se pudo explicar no inventa una causa.

## internal/order/place.go — Place y la puerta de disponibilidad

> La consulta va exactamente en un lugar: **después** de validar el draft, **después** del lookup de idempotencia por `client_key` y **antes** de generar cualquier identificador. Las tres posiciones son decisiones, no casualidad.
>
> Después de validar, porque un draft inválido no merece una consulta al proveedor y su error es más útil que un «no vendible».
>
> Después de la idempotencia, porque un reintento que devuelve una orden existente no se vuelve a chequear: la orden ya existe y bloquear el reintento sería mentir sobre un pedido ya hecho. Si la máquina se cayó después de la compra, el problema es del despacho, no de la creación.
>
> Antes de generar identificadores, porque un bloqueo no debe consumir ids ni dejar rastro en el store: si no se vende, no se creó nada.
>
> El error de la puerta se devuelve tal cual, sin envolver: un fallo del proveedor y un combo no vendible tienen que llegar distintos a la capa HTTP, que responde 500 o 409 según cuál sea.

## internal/gate/gate.go

### (archivo)

> El adaptador entre el motor de órdenes y `preflight`: traduce un `order.Draft` a la consulta de disponibilidad y el resultado al error del motor. Una sola responsabilidad, y por eso es un paquete aparte: es el único lugar del sistema donde se juntan los dos vocabularios.

### Checker

> La interfaz mínima la declara este paquete, no `preflight`: se necesita una sola función y así las pruebas no arrastran los clientes de los dos proveedores. `*preflight.Checker` la satisface sin cambios.

### New

> Acepta la interfaz y no el tipo concreto, por lo mismo. Con un verificador `nil` la puerta no explota: falla cerrado en `Check`.

### Check

> Un error del verificador se devuelve tal cual y **no** se convierte en `NotSellableError`: que un proveedor no conteste no es lo mismo que un combo agotado, y el llamador decide distinto en cada caso (reintentar sirve en uno, no en el otro). Sólo un resultado con `Sellable=false` se traduce al error de negocio, con las razones en el orden en que vinieron.

### request

> Traduce por tipo de máquina, no por posición ni por nombre de campo: batidos aporta `DeviceID` y snacks aporta `MID`, que es como cada proveedor identifica la máquina. El SKU es el del ítem.
>
> Si el draft no tiene exactamente un ítem de cada tipo, no se consulta nada y se devuelve un error que lo explica: la regla actual no sabe evaluar esa forma. **Fallar cerrado es la decisión** —no se vende algo que no se pudo chequear— pero tiene que ser un error visible y no un silencio que se lea como «todo bien». Por eso se cuentan los ítems de los dos tipos y se ignoran los de máquinas que la regla no conoce.

## internal/httpapi/errors.go — fail y el combo no vendible

> El mapeo nuevo vive donde ya se decidía el código de estado, y va antes del 500 genérico.
>
> `ErrNotSellable` es **409 Conflict**: el pedido está bien formado y el servidor lo entiende, pero el estado actual de las máquinas no permite crear la orden. Es un conflicto con el estado del recurso, no un error del cliente ni del servidor. El cuerpo lleva el mensaje en español y la lista de razones tomada del `NotSellableError` con `errors.As`, en el mismo orden en que las acumuló `preflight`. Sin razones se serializa una lista vacía y no `null`: el contrato del cuerpo dice lista.
>
> Un fallo del proveedor durante el chequeo **no** es 409: cae en el 500 que ya existía y sigue ahí por la misma razón, porque reintentar puede servir. Distinguirlos es lo que impide que la tablet le diga «combo agotado» a un cliente cuando en realidad el proveedor no contestaba.

## internal/fakeapi/admin.go — NewAdmin / adminHandler / decodeAdmin

> El tercer servidor del falso: una superficie de administración para poder preparar una prueba end-to-end sin escribir código. Existe porque los setters de `internal/fakeapi` son métodos Go: sin esto, un catálogo de batidos vacío bloqueaba cualquier venta (el chequeo previo responde `batidos_sku_not_listed`) y no había forma de sembrarlo desde afuera.
>
> Es una capa fina sobre los setters existentes: no cambia sus firmas ni la lógica de los handlers documentados. Los cuerpos rechazan campos desconocidos, responden `{"ok":true}` o `{"ok":false,"error":"…"}` en español, y un setter que no existe no se simula: se responde 501 con el motivo, porque un `ok` falso en una herramienta de prueba es peor que un error claro.

## cmd/fakeapi/main.go — requireLoopback

> `requireLoopback` es la única defensa que tiene: no hay autenticación y no la necesita, pero el servidor de administración se niega a arrancar fuera de loopback (incluidos `0.0.0.0` y cualquier IP no local). Es una herramienta de desarrollo, no una API.

## internal/order — el worker y los callbacks sobre la misma línea: `reconcile.go`, `callback.go` y `dispatch.go`

> `ReconcileDue` corre en su propia goroutine —el barrido cada 30 s de `internal/sweep`— mientras el callback llega por HTTP en otra. Los dos escriben la misma línea, y hasta que existió `Line.Version` el último en escribir ganaba: el worker tomaba una copia de las líneas al empezar (`DueLines`), el callback cerraba la línea como entregada en el medio, y el worker guardaba su copia vieja encima. El resultado era un `delivered` revertido y el `Ref` adoptado borrado, con la orden recalculada sobre el estado revertido. No era teórico: es la primera carrera real del sistema.
>
> `ErrStale` significa cosas distintas según quién escribe, y por eso cada llamador reacciona distinto. No hay una política única y no debería haberla: cerrar es irreversible y reprogramar no.
>
> - `closeLine` — relee la orden, busca la línea por `ID` y decide: si ya está cerrada (`delivered` o `failed`) es un no-op exitoso, alguien con más autoridad la resolvió; si sigue abierta reintenta **una sola vez** la transición con la versión fresca. Nunca reabre ni reescribe un cierre ajeno, y la copia vieja —con su `Ref` vacío— no llega al store.
> - `extendReconcile` — `(false, nil)`. La línea ya tiene dueño: no se reprograma ni se cierra, porque rearmar un plazo sobre una línea que otro acaba de cerrar es reabrirla de costado.
> - `dispatchLine` — nil, sin revertir nada. El efecto del despacho ya ocurrió y el estado de la línea lo resuelve la reconciliación con lo que diga el proveedor; el cierre del intento ya se registró antes, así que no se pierde la evidencia.
> - `markBrewing` — `(false, nil)`. «En curso» es la información más débil de las tres y no gana una carrera contra un cierre o contra un aplazo.
> - `settleOrder`, `Place` y `ConfirmPayment` — sin reacción propia: escriben la orden, no la línea, y `SaveOrder` guarda sólo la cabecera.
>
> La carrera se demuestra sin goroutines, porque una carrera reproducida por suerte no es un test. `staleDueStore` (`internal/order/callback_test.go`) guarda la copia que devolvió `DueLines` y la repite en las llamadas siguientes: es exactamente el estado en el que está el worker cuando el callback se le adelanta. El test `TestReconcileDueDoesNotOverwriteTheCloseThatACallbackAlreadyMade` copia la línea abierta, la cierra por el camino real del callback —que además adopta un `Ref`— y recién ahí corre el barrido con la copia vieja, en tres variantes según lo que la máquina le conteste al worker (cierre fallido, entrega y aplazo). Las tres afirman lo mismo: la línea sigue `delivered`, el `Ref` adoptado sigue ahí, los plazos siguen en cero, no hay error en `LastError` y la orden sigue `delivered`.
>
> `TestEngineDispatchDoesNotRevertTheLineThatACallbackClosedWhileItWasInFlight` cubre el mismo conflicto por el otro camino: el stub de la máquina dispara un `CloseFromCallback` real **dentro** de `Dispatch`, así que la línea queda cerrada entre la lectura y el guardado del despacho. La aserción es que la línea siga entregada, que el `Ref` tardío del despacho no se adopte sobre una línea cerrada, y que `ConfirmPayment` no devuelva error.

## internal/order — el versionado de la orden en el dominio (S3b)

> `competingOrderStore` (`internal/order/callback_test.go`) envuelve `store.Memory` y, antes de delegar un `SaveOrder`, guarda la orden que acaba de leer el store: escribe la cabecera con la versión siguiente sin cambiar ningún campo. Es la forma determinista de representar al otro escritor —un segundo `/pay`, otro callback, el barrido— sin goroutines ni suerte, igual que `competingWriteStore` hace con la línea. `arm` y `armFrom` eligen a partir de qué llamada compite.
>
> `TestEngineConfirmPaymentDoesNotDispatchWhenAnotherWriterPaidTheOrder` fija la decisión del segundo `/pay`: el guardado a `paid` choca, `ConfirmPayment` devuelve nil y las máquinas no reciben ni un despacho. `TestEngineConfirmPaymentDoesNotDispatchWhenTheDispatchingSaveIsStale` fija la otra mitad: si el choque llega en el guardado a `dispatching`, el error se devuelve y tampoco se despacha, porque sin ese estado no hay arista legal hacia la entrega.
>
> `TestCloseFromCallbackRetriesTheOrderSaveOnceWhenAnotherWriterTookIt` fija el reintento de `refreshOrderStatus` con dos casos y el mismo escenario: tras el pago se entrega por callback la línea de snacks y después falla la de batidos, así que el estado derivado pasa de `dispatching` a `partial` y hay que escribir la cabecera. Con un escritor adelantado se reintenta una sola vez y la orden termina en `partial`; con dos, el error devuelve `store.ErrStale` y la cabecera no se toca. Los dos casos cuentan las llamadas a `SaveOrder` para probar que el reintento es exactamente uno.

## internal/store — SaveOrder

> Guarda **sólo la cabecera** de la orden: estado, importes y las tres fechas. Nunca toca las líneas.
>
> No siempre fue así: antes reemplazaba la orden entera, líneas incluidas, y había un test que lo fijaba como intención. Ese contrato se volvió una carrera en cuanto las líneas empezaron a tener escritura propia y condicional: `settleOrder` y `ConfirmPayment` leen la orden, y si entre la lectura y el guardado un callback cierra una línea, el guardado de la orden devolvía la línea a su estado anterior — con el `Ref` adoptado borrado y la versión pisada. Ahora las líneas sólo cambian por `SaveLine`, que sí es condicional, y la orden sólo cambia por acá.
>
> El store sobre Postgres (`internal/store/postgres`) cumple ese contrato al pie de la letra: el guardado de la orden es un `UPDATE` de las columnas de cabecera, sin tocar `order_line`.
>
> Después de S3b la cabecera también se escribe con versión: `SaveOrder` recibe la orden con la versión leída y devuelve la guardada con `Version+1`, o `ErrStale` si otro escritor se adelantó. La razón es la misma que motivó la versión de la línea, un escalón más arriba: `ConfirmPayment` y `settleOrder` recalculan y guardan el estado de la orden desde caminos distintos, y sin número el último en escribir gana. Las líneas siguen afuera porque la versión protege la fila de `orders`, no la de `order_line`, que tiene la suya.

## internal/store/postgres

### (archivo)

> El puerto sobre PostgreSQL, con `database/sql` y el `stdlib` de `github.com/jackc/pgx/v5`, que es la primera y única dependencia externa del proyecto. Se eligió `pgx` por su `stdlib` y no por su API nativa: expone `database/sql`, así que el puerto sigue escrito contra la interfaz estándar y ningún otro paquete aprende qué driver hay abajo.
>
> `Open` abre el pool, hace `Ping` y recién entonces devuelve el store. El `Ping` no es ceremonia: sin él un DSN equivocado no se nota hasta la primera operación real, que es exactamente cuando hay dinero de por medio. `Close` cierra el pool.
>
> Todas las consultas usan `ctx` (`QueryContext`/`QueryRowContext`/`ExecContext`): el llamador que corta por plazo necesita que el corte llegue a la base y no que quede una conexión esperando.
>
> Un tiempo en cero se escribe como `NULL` y vuelve como `time.Time` en cero, que es la convención del puerto —`DeadlineAt` en cero significa «sin plazo armado», no un plazo vencido en el año 1—. Los `text` vacíos también viajan como `NULL`; los campos con `CHECK` (`state`, `outcome`) nunca, porque la base los rechazaría.
>
> Un conflicto de unicidad (`23505`) se traduce a `store.ErrAlreadyExists` en vez de propagarse como error de driver: para el motor es el mismo hecho que en memoria —se pidió crear algo que ya existe— y no tiene por qué enterarse de que abajo hay una base. Cualquier otro error de escritura se envuelve con el contexto de qué se estaba guardando.
>
> Los tres errores de dominio (`ErrNotFound`, `ErrAlreadyExists`, `ErrStale`) se devuelven como el valor centinela, sin envolver: así el llamador puede compararlos con `errors.Is` o con `==` y obtiene el mismo resultado, igual que con `store.Memory`.

### Mapeo de columnas

> - `Line.Ref` → `order_line.external_ref`. La columna `exchange_code` del esquema inicial quedó sin uso: era el nombre del proveedor de batidos y el asa de snacks no es un código de canje. Un campo con dos significados (R5) es menos código que dos campos de los cuales siempre se usa uno.
> - `Line.Machine` → las seis columnas `machine_*` (`machine_kiosk_id`, `machine_name`, `machine_device_id`, `machine_device_no`, `machine_device_type_id`, `machine_mid`). `Machine.Kind` no tiene columna propia: se escribe y se lee de `order_line.machine_type`. La columna sobrevive al campo Go que se borró, y la razón es que no eran lo mismo: la columna es el dato persistido —existe desde `0001_init.sql`, la escriben las migraciones y el seed, y tiene un `CHECK` con las dos familias— y `Line.MachineType` era la duplicación en memoria de algo que ya viajaba dentro de la `MachineRef`. Por eso no hay migración: `SaveLine` escribe `line.Machine.Kind` en `machine_type` y `scanLine` construye `dispenser.MachineRef{Kind: machine.Kind(<columna>)}` directamente. Seis columnas y no un `jsonb` porque cada identificador se audita por separado y así se puede buscar por `device_id` o por `mid`.
> - `Line.Version` → `order_line.version`, `bigint NOT NULL DEFAULT 0`.
> - `Order.ClientKey` → `orders.client_key`, con índice único parcial (`WHERE client_key IS NOT NULL`) para que dos órdenes sin clave no choquen entre sí.
> - `Order.Version` → `orders.version`, `bigint NOT NULL DEFAULT 0`. Misma columna y mismo significado que `order_line.version`, con el `DEFAULT` que deja las órdenes viejas en cero sin backfill.
> - `Attempt.Outcome`/`Detail` → `outcome`/`error_detail`. `request_payload` es `NOT NULL` en el esquema y se inserta `'{}'::jsonb`; `response_payload` y `http_status` quedan `NULL` porque hoy el motor no los recibe. No se inventa el cuerpo: un `NULL` dice «no hay dato» y un cuerpo vacío diría «el cuerpo estaba vacío».
> - `Webhook.Payload` no existe en el tipo, así que se inserta `'{}'::jsonb`. La deduplicación necesita la identidad `(source, dedupe_key)`, no el cuerpo.

### CreateOrder / SaveOrder

> `CreateOrder` va en una transacción: orden y líneas entran juntas o no entra ninguna, y cada línea se inserta con `version = 0` sin importar lo que traiga el llamador. Una línea que naciera con versión inventada quedaría inmune a los conflictos durante toda su vida. La cabecera se inserta con la versión que traiga la orden —cero en la práctica—, igual que en memoria. Si el `INSERT` de la orden choca con la clave primaria o con `client_key`, la transacción se revierte y el error es `ErrAlreadyExists`: no puede quedar media orden escrita.
>
> `SaveOrder` es un `UPDATE` de las nueve columnas de cabecera —`kiosk_id`, `combo_id`, `client_key`, `amount_cents`, `currency`, `status`, `created_at`, `paid_at`, `closed_at`— más `version = version + 1`, condicionado a `id = $1 AND version = $11` y con `RETURNING version`, y **nunca toca `order_line`**. Es la contraparte en SQL de la decisión del puerto: al escribir, la orden no es dueña de sus líneas. Si las escribiera, `settleOrder` o `ConfirmPayment` podrían revertir un `delivered` que un callback acaba de guardar —con su `Ref` adoptado y todo— aunque la línea tenga versión, porque un reemplazo total escribe todas las columnas y no compara ninguna.
>
> Con cero filas el `UPDATE` confunde dos causas y se separan como en `SaveLine`, con un `EXISTS` posterior y sólo en ese camino: la orden existe con otra versión (`ErrStale`) o no existe (`ErrNotFound`). La versión devuelta sale del `RETURNING` y no de la copia del llamador, que es la única forma de que el siguiente guardado parta del número real.

### SaveLine

> Guardado condicional de verdad: `UPDATE … SET …, version = version + 1, updated_at = now() WHERE id = $1 AND version = $2 RETURNING` la fila completa. No es un reemplazo: si la versión no coincide, no se escribe nada.
>
> Con cero filas afectadas el `UPDATE` confunde dos causas, y hay que separarlas: la línea existe con otra versión (`ErrStale`) o no existe (`ErrNotFound`). Se resuelve con un `EXISTS` posterior y sólo en ese camino; preguntar primero por la versión agregaría una consulta al camino feliz, que es el que corre siempre.
>
> El `RETURNING` es lo que hace que la línea devuelta traiga la versión de la base y no la que el llamador creía: el llamador encadena esa versión en el guardado siguiente sin releer la fila. Y se comprueba con `RowsAffected`, no con `err != nil`, porque un `UPDATE` que no toca nada no es un error para la base.

### AppendAttempt / SaveAttempt

> `AppendAttempt` inserta y traduce el `23505` a `ErrAlreadyExists`: sirve tanto para la `idempotency_key` como para el par `(line_id, attempt_no)`, que son las dos unicidades que impiden registrar dos veces el mismo intento.
>
> `SaveAttempt` actualiza por `idempotency_key` sólo `outcome`, `error_detail` y `finished_at`, y cero filas es `ErrNotFound`. Nunca crea y nunca toca la línea: si el cierre pudiera insertar, un error de orden de llamadas inventaría intentos que nunca ocurrieron, y si tocara la línea estaría escribiendo sin condición sobre las mismas filas que protege la versión.

### RegisterWebhook / DueLines

> `RegisterWebhook` es un `INSERT … ON CONFLICT (source, dedupe_key) DO NOTHING` y devuelve `first` según las filas afectadas: un webhook repetido es el caso normal de un proveedor que reintenta, no un error. Un `INSERT` ciego más un `ErrAlreadyExists` obligaría al llamador a tratar un duplicado como excepción.
>
> `DueLines` filtra `deadline_at IS NOT NULL AND deadline_at <= $1` y ordena por `deadline_at` con `id` como desempate, para que dos plazos iguales salgan siempre en el mismo orden y un worker con tope no se comporte distinto entre corridas. El `LIMIT` sólo se agrega cuando el tope es mayor que cero: el puerto define que cero significa «sin tope», y un `LIMIT 0` de SQL devolvería nada, que es lo contrario.

### Open / Close — la dependencia

> La versión de `pgx` se eligió entre las que no obligan a subir el lenguaje declarado en `go.mod`: agregar la primera dependencia externa no debe cambiar la versión mínima de Go del proyecto. Actualizarla es una decisión aparte, con su propio diff.

## internal/store/postgres/postgres_test.go

> Los dos tests nuevos son el contrato del guardado condicional de la cabecera, espejo de los de `SaveLine`: el aceptado devuelve `Version+1` y es lo que se lee después, y el de una copia vieja devuelve `ErrStale` sin mover ni el estado ni las fechas ni la versión.
>
> Se saltean sin `TEST_DATABASE_URL`, como todo el paquete: la migración `0005` y este contrato quedan escritos y sin ejecutar contra una base real.

## migrations/0004_store_postgres.sql

> Lo que el esquema inicial no tenía y el store sobre Postgres necesita para cumplir el puerto. Todo es aditivo y cada sentencia lleva `IF NOT EXISTS`, así que aplicarla dos veces no rompe nada. No se edita `0001_init.sql` a propósito: una migración ya aplicada no se reescribe, se corrige con otra.
>
> - `orders.client_key` y su índice único **parcial**: la idempotencia de la creación de la orden queda en la base, no en un `SELECT` previo, que sería un TOCTOU entre dos tablets tocando en el mismo instante. El `WHERE client_key IS NOT NULL` es lo que permite que muchas órdenes no tengan clave sin chocar entre sí.
> - `order_line.version` (`bigint NOT NULL DEFAULT 0`): el `DEFAULT` es lo que hace que las filas viejas queden en la versión correcta sin backfill, y el `NOT NULL` es lo que impide que una línea quede fuera del guardado condicional.
> - Las seis columnas `machine_*`: la `MachineRef` que antes sólo vivía en un mapa de RAM. Son nullable porque el esquema no puede exigir «las de batidos o la de snacks» sin repetir en SQL una regla que ya vive en el motor, y un `CHECK` duplicado sería una segunda fuente que se desincroniza.
> - `dispatch_attempt_key_idx` y `webhook_event_source_dedupe_idx` son índices únicos que ya existían con otro nombre en `0001_init.sql` (`dispatch_attempt_idempotency_idx` y `webhook_event_dedupe_idx`). El `IF NOT EXISTS` compara el nombre y no la definición, así que la base queda con dos índices únicos equivalentes por tabla. Es inocuo —la unicidad ya se cumplía y el costo es un índice de más al escribir— y se deja tal cual porque quitarlo es una migración aparte; conviene saberlo antes de mirar el esquema y creer que falta un índice o que sobra una regla.

## migrations/0005_order_version.sql

> Agrega `orders.version` (`bigint NOT NULL DEFAULT 0`). Aditiva y sin backfill por el mismo motivo que `order_line.version` en `0004`: el `DEFAULT` deja las órdenes existentes en la versión correcta y el `NOT NULL` impide que una orden quede fuera del guardado condicional. No lleva índice porque la versión viaja en el `WHERE` junto con la clave primaria, que ya está indexada.

## internal/config — Store / DatabaseURL: config.go y validate.go

> El proceso tenía un solo store cableado y ninguna forma de elegir otro. `store` (`memory` por defecto) y `database_url` son la perilla: el archivo elige el motor y el entorno puede pisarlo con `STORE` y `DATABASE_URL`, igual que ya pasa con los secretos. `STORE` existe porque el compose declara el store por entorno, no editando el JSON.
>
> La validación vive en `validate`, donde ya viven las demás: `store` sólo acepta `memory` o `postgres`, y `postgres` sin `database_url` es un error de configuración, no un fallo de arranque del driver. El mensaje nombra el campo para que el operador sepa qué corregir sin leer el código.

## internal/topology/seed.go

> Existe porque `orders` referencia `kiosk(id)` y `combo(id)`: contra Postgres ninguna orden entra si esas filas no existen. La topología ya está en la configuración y el catálogo ya está en el JSON de combos, así que se copian a la base al arrancar en vez de exigir un sembrado manual que se desincroniza en cuanto alguien edita la configuración.
>
> Firmas contra `Execer` (sólo `ExecContext`) y no contra `*sql.DB`: el sembrado se prueba sin base, con un registrador que anota las sentencias. La versión transaccional la aporta `postgres.Store.Seed`.
>
> Las columnas son exactamente las de `0001_init.sql`, sin inventar ninguna: `kiosk (id, name)`, `machine (kiosk_id, type, name, batidos_device_id, batidos_device_no, batidos_device_type_id, snacks_mid)`, `combo (id, name, price_cents, currency, active)` y `combo_item (combo_id, position, machine_type, sku)`. `description`, `sort_order`, `sku_name` y los timestamps quedan en su `DEFAULT`: el `catalog.Combo` no los tiene y agregarlos al contrato del catálogo sería otra decisión.
>
> Cada sentencia es `INSERT … SELECT … WHERE to_regclass('tabla') IS NOT NULL … ON CONFLICT … DO UPDATE`, por dos motivos. El `ON CONFLICT` es lo que hace idempotente el arranque: reiniciar no duplica ni pisa la clave primaria. El `WHERE to_regclass(...)` es el «si la tabla existe» pedido: con una migración sin aplicar el sembrado no rompe el arranque, pero tampoco escribe. El costo de esa elección es explícito: el proceso arranca igual y la primera orden falla contra la tabla que falta, así que las migraciones siguen siendo obligatorias antes de vender.
>
> `NULLIF($n, '')` convierte el vacío de la configuración en `NULL`, que es lo que el `CHECK` de `machine` exige: la máquina de batidos no lleva `snacks_mid` y la de snacks no lleva los tres `batidos_*`. Mandar cadena vacía en vez de `NULL` chocaría contra el `CHECK` y `char(3)` de `combo.currency` no acepta un vacío con sentido, así que `COALESCE(NULLIF($4, ''), 'ARS')` cae en la moneda por defecto de la tabla.
>
> `combo_item.position` es el índice del ítem en el combo, empezando en cero, igual que el orden en que llegan del JSON. No se borran ítems sobrantes: si el catálogo se acorta, la fila vieja queda en la base. Se acepta porque nadie lee `combo_item` para vender —el menú sale del JSON— y borrar filas es una decisión destructiva que no corresponde a un sembrado.

## internal/store/postgres/seed.go — Seed

> `Seed` existe para que el arranque tenga una sola llamada y no necesite el `*sql.DB` que el store mantiene privado. Abre una transacción, delega en `topology.Seed` y la confirma: si una sentencia falla, no queda media topología escrita y el llamador aborta el arranque con el error del mueble, la máquina o el combo que falló.

## cmd/api/main.go — openStore

> `openStore` devuelve el store del puerto más su cierre, y por eso el `main` puede tener un solo `defer closeStore()` sin saber cuál de los dos motores salió. Con `memory` avisa con el `WARN` de siempre: el estado se pierde al reiniciar y sólo puede correr una instancia. Con `postgres` abre, siembra y, si el sembrado falla, cierra lo abierto antes de devolver el error: un store abierto que nadie va a usar es una conexión colgada.
>
> El sembrado usa un contexto propio con tope de 30 segundos, no el del servidor: en ese punto las señales todavía no están conectadas y una base que no responde dejaría el arranque colgado para siempre.

## Dockerfile

> Dos etapas para no llevar compilador a producción: `golang:1.23` compila con `CGO_ENABLED=0` y la imagen final es `gcr.io/distroless/static:nonroot`, sin shell, sin gestor de paquetes y con usuario no root. `-trimpath` y `-ldflags "-s -w"` sacan rutas locales y símbolos de depuración del binario.
>
> Hay dos etapas finales, `runtime` y `fakeapi`, porque el compose necesita el binario de los proveedores falsos con la misma receta de compilación. El `COPY` es explícito (`go.mod`, `go.sum`, `cmd`, `internal`) y no `COPY . .`: así el contexto no arrastra `.git`, `design/` ni los documentos de terceros, y con BuildKit ni siquiera se envían.
>
> La imagen no lleva secretos: todo entra por variables de entorno en tiempo de ejecución.

## docker-compose.yml

> Cuatro servicios que reproducen el orden real de un despliegue. `db` es `postgres:16` con volumen nombrado y `healthcheck` de `pg_isready`. `migrate` usa la misma imagen, monta `./migrations` y aplica los `.sql` en orden alfabético con `psql`; corre una vez y termina. `api` depende de la base sana y de que `migrate` haya terminado bien, y `fakeapi` levanta los dos proveedores falsos.
>
> `migrate` no lleva tabla de versiones: es un `psql` sobre los archivos, como se pedía. La consecuencia es que el primer `up` sobre un volumen nuevo funciona y el segundo falla en el DDL ya aplicado; para repetir el esquema hay que bajar el volumen (`docker compose down -v`). Una tabla de control sería otra decisión y otro diff.
>
> `fakeapi` recibe `-batidos-addr 0.0.0.0:9401` y `-snacks-addr 0.0.0.0:9402` porque un proceso atado a `127.0.0.1` dentro del contenedor no es alcanzable ni por el servicio `api` ni por un puerto publicado. El puerto de administración (9403) **no** se publica: el falso se niega a escuchar su administración fuera de loopback, y esa defensa es correcta; publicarlo daría un puerto muerto. Los dos puertos de proveedor se publican sólo en `127.0.0.1` del host, igual que el arranque local.
>
> Los dos secretos de callback son de ejemplo y sólo llevan letras y guiones, porque la configuración valida el path del callback contra esos caracteres.

## config.docker.json

> Es `config.example.json` apuntado al compose: `store` en `postgres`, `public_base_url` con el puerto publicado (`18080`), y las dos URLs de proveedor con el nombre del servicio `fakeapi` en vez de `127.0.0.1`. Las credenciales son las que el falso espera por defecto (`mykey`/`mysecret`, `ak_test`/`testsecret`): valores de pruebas locales, no credenciales de nada, y así el stack levanta sin exportar variables. El `device_id` de batidos es `dev-e2e-1`, que es el dispositivo al que el falso le siembra el producto.
