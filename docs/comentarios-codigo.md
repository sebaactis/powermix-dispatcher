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

> Máquina de batidos. La API necesita los tres: deviceId y deviceNo son
> identificadores distintos y produceCoffee pide ambos.

### Machine

> Máquina de snacks.

### Kiosk

> Kiosk es el mueble físico: dos máquinas y la tablet en el medio.

### Machine

> Machine devuelve la máquina del tipo pedido, si el mueble tiene una.

### Config

> Config es la configuración ya resuelta y validada.

### Load

> Load lee el archivo de topología, aplica los reemplazos de secretos desde el
> entorno, resuelve los valores por defecto documentados y valida el resultado.

### (archivo)

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
> Aceptado NO es entregado (R5): un Attempt exitoso solo significa que el pedido
> fue tomado. La entrega real la confirma el callback y se verifica con
> OrderInfo. Attempt.Ref queda vacío a propósito porque produceCoffee no
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

> "batidos" o "snacks"

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

### Attempt

> Attempt es la respuesta inmediata a Dispatch. "Aceptado" nunca significa
> "entregado".

### Attempt

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
> significa que la respuesta no trajo código, no un error.

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

> VerifySignature revisa presencia de headers, ventana de reloj y firma. Lo usa
> el servidor falso local y también nuestros handlers de callback cuando el
> proveedor nos manda algo.

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

### Check

> Chequeo a nivel máquina: fuera de línea, avería y sin insumos, en ese orden. Los tres ejes se comparan contra las constantes documentadas, nunca contra números crudos. Reason queda vacío cuando la máquina está sana y, cuando no, enumera sólo los ejes que fallaron ("avería; sin insumos"), para que el motivo sea legible sin abrir el panel del proveedor.
>
> Deliberadamente no mira el producto: si el SKU del combo está listado y con stock es la regla de `internal/preflight`, que sí lee el catálogo.

### Dispatch

> Dos llamadas por bebida, en este orden, porque así funciona Batidos (R2): emitir la campaña que genera el código de canje (S6, un código por compra) y canjearlo enseguida. `exchangeType 1` significa un único producto, que es exactamente lo que es un combo fijo.
>
> El reloj es el del cliente y no uno propio: así `WithClock` vuelve determinista la ventana y adaptador y cliente comparten una sola noción de «ahora». La ventana se construye con instantes absolutos; el cliente la formatea en UTC+8 (R3).
>
> `Attempt.Ref` es el código de canje, no un número de pedido: produceCoffee no devuelve orderNo, y el código es exactamente lo que `Reconcile` necesita para preguntar qué pasó.
>
> Si la campaña falla no hay código, así que el Attempt queda vacío. Si la campaña se emitió y el canje falla, el Attempt conserva el código aunque `Accepted` sea falso: un `Ref` no vacío junto al error es la señal de «esto es reconciliable, no lo reintentes a ciegas» (E4). Perder ese código sería perder la única forma de saber si el batido salió.

### Reconcile

> Traduce el status de getOrderInfo con las constantes del cliente: 0 falló, 1 en preparación, 2 completado. Cualquier otro valor queda como `LineInDoubt` con el número informado en Reason: el enum no está documentado, así que un valor nuevo significa «no se sabe», y declarar `LineFailed` sería afirmar algo que el proveedor no dijo.
>
> `Status.Ref` es siempre el orderNo del proveedor, también en el caso fallido: es lo que identifica el pedido en la conciliación y en el panel.
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
> `Attempt.Ref` es lo que el proveedor haya contestado, normalmente vacío porque la documentación responde `result: {}`. No se rellena con el id de la orden: un Ref vacío dice la verdad —«el proveedor no dio un asa»— y es por eso que `Reconcile` cae al OrderID.

### centsToUnits

> Convierte centavos enteros al decimal que exige `retailPrice`. Es dinero de exhibición: float sólo para mandar y loguear, nunca para cuentas. Adentro del sistema todo importe sigue siendo int64 en centavos.

### Reconcile

> Busca el pedido por `transactionOrderNo` igual al asa que tenemos (`req.Ref`, o el id de la orden cuando el Ref quedó vacío). No se manda `OrderNo` como filtro a propósito: el filtro del proveedor es por `orderNo` y el asa que tenemos es el `transactionOrderNo` del pago; mandarlo excluiría justamente el pedido buscado. El match es local y exacto.
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

### Line

> La unidad de ejecución. `State` reusa `dispenser.LineState` a propósito: el estado de una línea es el mismo que informa el puerto de las máquinas, y duplicarlo obligaría a traducir entre dos enums idénticos en cada borde.
>
> `Ref` tiene dos significados según la máquina —código de canje en batidos, asa del proveedor en snacks— porque el SQL tiene dos columnas y el motor nunca necesita las dos a la vez.
>
> `DeadlineAt` en cero significa «sin plazo armado», no un plazo vencido en el año 1.

### Attempt

> Auditoría de una llamada a una máquina; sin esto un timeout nos deja sin saber si el producto salió. `Outcome` «ambiguous» es el caso E4 y es el único que prohíbe reintentar a ciegas. `IdempotencyKey` es única en todo el sistema: es la garantía de que un mismo pago no produce dos entregas.

### Webhook

> `Source` usa el vocabulario del dominio y del CHECK de `webhook_event`: «payment», «batidos» o «snacks». La pareja (Source, DedupeKey) es la identidad del evento: una repetición del proveedor tiene que ser inofensiva, no una segunda bebida.

### Store

> `CreateOrder`/`Order`/`SaveOrder` mueven la orden entera con sus líneas, porque el motor la lee y la reescribe como unidad y las líneas nacen sólo junto a su orden. `SaveLine` actualiza una línea suelta —lo que hace el worker al vencer un plazo— y nunca crea: una línea sin orden sería un producto sin quien lo pague. `RegisterWebhook` responde «¿es nuevo?», que es justo lo que el handler necesita. `DueLines` responde la pregunta del worker: qué venció.

## internal/store/memory.go

> Implementación en memoria: permite escribir y probar el motor de punta a punta sin driver. No es un caché, es la única implementación hasta que exista el SQL, y por eso impone las mismas reglas que impondría la base —unicidad de orden, de clave de idempotencia y de dedupe— en vez de ser permisiva.
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

## internal/store/memory_test.go

> Los tests viven en el paquete externo `store_test` porque prueban el contrato del puerto desde afuera, que es como lo va a usar el motor.
>
> Los que más importan no son los de camino feliz: uno fija que el store nunca comparte estado con quien llama —sin la copia, el motor podría cambiar una línea sin guardarla y el bug aparecería como una reconciliación fantasma—, otro fija que dos líneas con el mismo plazo salen siempre en el mismo orden, y el de concurrencia crea una orden por goroutine y después exige leerlas todas: es la única forma de que un mapa sin protección se note como corrupción de conteo y no como un test que pasa por suerte.

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

> `Draft` es lo que la tablet manda antes de que exista una orden. `Item` lleva la máquina en un solo lugar: `Item.Machine`, y el tipo de máquina sale de `Item.Machine.Type`. La orden guarda la referencia completa en `store.Line.Machine`, así que el motor no retiene nada en memoria y un reinicio no le quita con qué hablarle a la máquina.

### El tipo de máquina como fuente única

> Antes el tipo estaba dos veces —`Item.MachineType` y `Item.Machine.Type`— y `validateDraft` sólo miraba el primero: un ítem con `MachineType: "snacks"` y `MachineRef{Type: "batidos", DeviceID: ...}` pasaba la validación, el motor elegía el driver de snacks y le mandaba la identidad de la máquina de batidos. El resultado era un rechazo del proveedor con un error que no explicaba nada.
>
> Se arregló por la vía limpia y no con una validación de coincidencia: dos campos que deben coincidir es una regla que se puede olvidar, un campo solo no. Ahora la incoherencia no se puede escribir —no compila—, que es más fuerte que cualquier chequeo en ejecución.
>
> `validateDraft` exige que `item.Machine.Type` no esté vacío y que ese tipo exista en el mapa de drivers, que es el mismo mapa con el que `dispatchLine` busca la máquina después: lo que se valida en el alta y lo que se usa en el despacho es la misma fuente, así que un borrador aceptado no puede terminar en «no hay una máquina configurada para el tipo».
>
> Riesgo residual anotado: `machineIdentified` sigue aceptando **algún** identificador, así que `Machine.Type: snacks` con un `DeviceID` de batidos es expresable. Ya no hay dos fuentes de tipo, pero la identidad no se valida contra el tipo.

### WithLineDeadline

> Sólo acepta valores positivos: cero o negativo produciría un plazo ya vencido en el instante del despacho, que es peor que no tener plazo. `WithClock` y `WithIDGenerator` ignoran `nil` para que un test no deje el motor sin reloj ni sin ids.

### Place

> Valida el borrador entero antes de crear nada: si un ítem está mal, no queda una orden a medio armar. El mensaje nombra la posición del ítem, porque el llamador necesita saber cuál corregir. La orden nace `pending_payment` con una línea `pending` por ítem y los importes copiados; el dinero es int64 en centavos en todo el sistema.

### ConfirmPayment

> Es el único disparador del despacho y tiene que ser inofensivo si el proveedor de pagos repite el webhook: si la orden ya no está `pending_payment`, responde nil sin tocar nada.
>
> La orden se escribe tres veces. Primero `paid` con `PaidAt`, que es la evidencia del cobro. Después `dispatching`: una línea sólo sale de `pending` por `sent`, así que sin ese estado intermedio una orden cuyas dos líneas fallan no tendría arista legal hasta `failed`. Recién entonces se lanza una goroutine por línea, cada una escribiendo su propio slot del slice de errores — sin lock compartido.
>
> Al final se **recarga** la orden y se deriva el estado con `AdvanceOrder` sobre las líneas leídas. Derivar de la copia en memoria que las goroutines mutaron sería leer exactamente aquello que se quería verificar.
>
> Sólo los errores de store vuelven de acá; el fallo de una máquina no es un error de infraestructura sino un resultado que se persiste.

### dispatchLine

> El orden de las llamadas es la garantía contra la doble entrega: primero se registra el intento como `ambiguous`, después se llama a la máquina, y al final se cierra el intento y se guarda la línea. Si el proceso muere en el medio, queda un intento `ambiguous` —la evidencia de que hubo una llamada— y la línea sigue `pending`.
>
> La clave es `orden:línea:número de intento`, con el número sacado de `Attempts+1`. Si `AppendAttempt` dice que ya existe, ese intento ocurrió en una vida anterior del proceso: no se despacha, no se toca la línea, se devuelve nil. Es la razón por la que un reinicio no puede sacar dos productos.
>
> `Attempts` se incrementa en **todo** intento registrado, no sólo en los aceptados: si un rechazo no lo moviera, el reintento de T9 reusaría la clave y el store lo leería como replay, prohibiendo el reintento que S7 exige.
>
> La línea se guarda con `SaveLine` y nunca con `SaveOrder`: dos goroutines escribiendo la orden entera se pisarían.

### classifyDispatch

> Tres desenlaces, y la diferencia es qué sabemos del producto: aceptado es «la máquina lo tomó» (R5: todavía no es entregado, por eso la línea vence y hay que reconciliar); rechazado es «la máquina dijo que no»; cualquier otra cosa es «no sabemos», y se marca `in_doubt` y nunca `failed` (E4), porque declarar fallido habilitaría un reintento a ciegas que podría sacar dos productos.
>
> El `Ref` se copia a la línea aunque venga vacío: el asa del proveedor es opcional, pero perderla cuando existe sería perder la única forma de reconciliar.

### transitionLine

> `status.go` permite una sola salida desde `pending`: `sent`. Un rechazo o una duda significan que la línea **sí** se despachó, así que el estado se recorre `pending → sent → failed|in_doubt` y se persiste sólo el estado final, con una escritura.

## internal/order/engine_test.go

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

## internal/order/engine.go — cambios

> `Place` copia la máquina del ítem a la línea antes de persistir: es lo que convierte la orden en algo autosuficiente. El motor ya no retiene nada en memoria, así que puede perder el proceso entero entre el cobro y el despacho sin perder con qué hablarle a la máquina.
>
> `dispatchLine` toma la máquina de la línea. Un reinicio es indistinguible de una llamada normal: sólo cambia de dónde vino el store. Ya no existe el caso «no se conoce la máquina» porque la línea no puede nacer sin máquina: `validateDraft` lo impide en `Place`.
>
> `isProviderRejection` pregunta por el error del puerto y no por los tipos de cada proveedor. Ese era el punto: el motor no debe conocer que Batidos y Snacks existen, y con los dos imports cualquier proveedor nuevo obligaba a editar el motor para clasificar sus rechazos.

## internal/dispenser/{batidos,snacks} — asPortRejection

> Traduce el `*APIError` propio a `*dispenser.Rejection` en el borde: en `Dispatch` y en `Reconcile`, que son los dos lugares por donde sale una respuesta del proveedor hacia el motor. Se envuelve después con `%w` para no perder el contexto, y por eso `errors.As` sigue encontrando el rechazo.
>
> Un error que no es del proveedor —red, JSON roto, contexto cortado— pasa intacto: no es un rechazo, y convertirlo en uno haría que una duda se declarara fallo.

## internal/order/engine_test.go — el test del reinicio

> Es el test que prueba que la deuda está pagada: el motor que cobra no es el que despacha. Un segundo `order.New` sobre el mismo store no comparte ni memoria ni mapa de referencias con el primero, así que si la máquina no estuviera guardada en la línea, el despacho no tendría con qué hablarle. Antes de mover la referencia al store, esto fallaba con «no se conoce la máquina de la línea».

## internal/order — el reintento del fallido (T9a)

### CanLineTransition: `failed -> sent`

> `failed` deja de ser terminal a propósito: un rechazo explícito del proveedor (E2) significa «no salió», así que la línea puede volver a `sent` para un intento nuevo. La garantía anti-duplicado no es el estado, es la **clave de idempotencia**: un reintento es un intento nuevo con clave nueva, no un replay del anterior.
>
> `delivered` sigue siendo terminal (el producto ya salió) e `in_doubt` no puede volver a `sent` (E4: no se sabe si salió, y reintentar a ciegas puede entregar dos veces). Esa línea se reconcilia, no se reintenta.

### defaultMaxAttempts / WithMaxAttempts

> Dos intentos por línea: el inicial más un reintento del fallido (S7). Es el mínimo que cumple el pedido y la cantidad queda configurable porque la política de cuántos reintentos (E2) sigue abierta. Un valor menor que 1 se ignora: nunca se deja al motor sin poder despachar.

### dispatchLine — el bucle

> Un bucle acotado sobre **la misma** línea. En cada vuelta se arma el número de intento (`Attempts + 1`) y su clave, se **registra el intento como `ambiguous` antes de llamar** —así un corte en medio deja constancia de que algo pudo haber salido—, se llama, se clasifica, se cierra el intento y recién ahí se mueve la línea.
>
> Se detiene cuando: la línea ya gastó sus intentos; la clave ya existe (intento repetido tras un reinicio); fue aceptada; quedó en duda; o fue rechazada sin intentos restantes.
>
> Sólo vuelve a iterar ante un rechazo explícito y con intentos disponibles: mueve la línea `failed -> sent` (la arista habilitada para esto) y empieza con número y clave nuevos. En Batidos eso es un código de canje nuevo, porque el anterior probablemente quedó consumido (R2).
>
> E4 no entra nunca acá: sin respuesta no se sabe si el producto salió, y el reintento a ciegas puede producir dos entregas.

## internal/order — el worker de reconciliación (T9b)

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

### extendReconcile

> Decide el próximo instante de consulta y, cuando ya no queda margen, cierra la línea en duda. Tres casos y los tres terminan: sin ventana armada se cierra en el acto; con ventana abierta se empuja un intervalo **recortado al tope** si el intervalo se pasaría; en el tope o después se cierra, limpia los dos plazos y guarda el motivo.
>
> El recorte no es cosmético: garantiza que el último escaneo caiga exactamente en el tope y nunca después, y es lo que convierte «preguntar hasta tener respuesta» en «preguntar hasta tener respuesta **o** hasta que se acabe el tiempo».

### closeLine / settleOrder / notifyOrder

> Cerrar es limpiar los dos plazos: una línea cerrada no vuelve a aparecer en `DueLines`, y eso es lo que impide interrogarla después de que alguien ya sabe la respuesta. Entregado limpia `LastError`; fallido conserva o reemplaza el motivo, nunca lo borra.
>
> Una línea cerrada no arregla la orden sola: si el worker no recalcula el estado, la orden se queda en `dispatching` para siempre. Se recarga, se avanza con la misma tabla de transiciones del resto del motor y se guarda sólo si cambió.
>
> Un aviso por orden afectada, y sólo cuando el estado final es duda o fallo: es la excepción que necesita una persona, no un parte de novedades. El detalle va en español porque lo lee alguien del equipo, no un programa.

## migrations/0003_line_reconcile_until.sql

> Agrega `reconcile_until` a `order_line`. Aditiva a propósito: sin CHECK, sin índice y sin migración de datos. La columna es nullable y su ausencia es exactamente el «no armado» que ya conoce el código, así que las filas viejas no necesitan backfill. No lleva índice porque el worker no busca por ella: sigue buscando por `(state, deadline_at)`.
