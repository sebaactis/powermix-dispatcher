# Diseño: cobro con QR de Mercado Pago

Estado: diseño acordado el 2026-11-16 (L47 en `odd/tasks/combos-remotos.md`). Sin implementar.

Para el contexto general, consulta [arquitectura.md](arquitectura.md); para el estado de las tareas, [pendientes.md](pendientes.md).

---

## Decisiones

| # | Decisión | Por qué |
| --- | --- | --- |
| 1 | **QR dinámico** (`config.qr.mode: "dynamic"`) de la Orders API (`POST /v1/orders`) | La tablet dibuja un QR único por orden: nadie puede pagar la orden de otro |
| 2 | **El QR vence a los 180 s** (`expiration_time: "PT3M"`) | Igual que `QR_EXPIRY_SEC` en `vps-powermix` |
| 3 | **Si Mercado Pago dice que se pagó, se despacha**, aunque haya pasado el plazo | Sin devoluciones en el MVP, cobrar sin entregar es el peor resultado |
| 4 | **El monto pagado se compara con el del catálogo**; si no coincide, no se despacha y se avisa a soporte | Invariante 1: el precio sale del catálogo |
| 5 | **Una aplicación de Mercado Pago propia** para compras-remotas | El webhook es uno por aplicación; compartirla mezclaría avisos de otros sistemas |
| 6 | **El webhook responde enseguida y el despacho corre en segundo plano** | `ConfirmPayment` puede tardar hasta 2 min y Mercado Pago reintenta si no respondemos rápido |
| 7 | **Secreto en la ruta además de la firma** | Misma defensa que los callbacks de batidos y snacks |
| 8 | **La tablet consulta `GET /orders/{id}` cada 2 s** | Suficiente para el MVP; no hace falta SSE ni WebSocket |

## Regla central: manda Mercado Pago, no nuestro reloj

Nunca se cancela una orden por cuenta propia. Al vencer el plazo se le pide a Mercado Pago que cancele y se obedece su respuesta: si dice «pagada», se despacha; si dice «cancelada» o «vencida», recién entonces se cancela la nuestra. Así no hace falta abrir la transición `cancelled → paid` (`internal/order/status.go:52`).

## Requisitos de Mercado Pago (confirmados en la documentación)

- `config.qr.external_pos_id` es **obligatorio** también en modo dinámico: cada mueble necesita **una sucursal y una caja** dadas de alta en Mercado Pago, una sola vez.
- La URL del webhook **no viaja en la orden**: se configura en el panel de la aplicación, tópico **«Order (Mercado Pago)»**.
- `POST /v1/orders` exige `X-Idempotency-Key`. `external_reference` admite hasta 64 caracteres `[A-Za-z0-9_-]`, y nuestro id de orden (hex) cumple.
- `expiration_time` es una duración ISO 8601; sin ella vale 15 minutos.

## El webhook

```
POST /callbacks/mercadopago/{secret}?data.id=ORD01JQ4S4KY8HWQ6NA5PXB65B3D3&type=order
X-Request-Id: 2066ca19-c6f1-498a-be75-1923005edd06
X-Signature: ts=1742505638683,v1=ced36ab6…
```

| Dato | Qué es |
| --- | --- |
| `data.id` | Id de la orden en Mercado Pago (`ORD…`), el que devolvió `POST /v1/orders` |
| `x-request-id` | Id único de cada envío del aviso. No se usa para deduplicar: no está documentado si un reintento lo repite |
| `x-signature` | `ts` (milisegundos) y `v1 = hex(HMAC-SHA256(secreto_webhook, "id:<data.id>;request-id:<x-request-id>;ts:<ts>;"))`. `data.id` va **en minúsculas** dentro del manifiesto y un par vacío se omite (SDK oficial de Go) |

Validación en dos capas:

1. **Firma** y `ts` dentro de una ventana de 5 minutos → prueba que el aviso viene de Mercado Pago. Si falla, 401.
2. **`GET /v1/orders/{data.id}`** → prueba qué pasó de verdad: estado, monto pagado y `external_reference`. La firma no cubre el cuerpo, así que el cuerpo no se usa.

La deduplicación no hace falta: `ConfirmPayment` ignora una orden que ya no está en `pending_payment`.

## Piezas

### Datos — migración `0007`

Se reutiliza `payment_intent`, que existe desde `0001` y nunca se usó. La migración agrega:

- FK de `order_id` a `orders` e índice único por `order_id` (un QR por orden).
- `expires_at timestamptz`.
- `version bigint` para el guardado condicional.
- El estado `cancelled` en el `CHECK`.

Puerto `store`, cuatro métodos nuevos: `CreatePaymentIntent`, `PaymentIntentByOrder`, `SavePaymentIntent` (con versión, `ErrStale` como las líneas) y `PendingPaymentIntents(olderThan, limit)`.

### Puerto `payments.Gateway`

```go
type Gateway interface {
    CreateQR(ctx context.Context, req QRRequest) (Charge, error)
    Lookup(ctx context.Context, providerID string) (Charge, error)
    Cancel(ctx context.Context, providerID string) (Charge, error)
}
```

`Charge` lleva `ProviderID`, `ExternalRef`, `QRData`, `Status` (`pending`, `approved`, `expired`, `cancelled`, `unknown`) y `PaidAmountCents`. `unknown` cubre `refunded`, `failed` y cualquier estado no previsto: el motor no despacha ni cancela, avisa.

### Adaptador `internal/payments/mercadopago`

`client.go` con `net/http` (sin SDK), `wire.go` con el JSON del proveedor (convención L27) y `signature.go` con la validación de `x-signature`.

**A confirmar contra el sandbox real:** el mapeo de `status`/`status_detail` de Mercado Pago a nuestros estados.

### Mercado Pago falso

En `internal/fakeapi`, puerto 9404: crea, consulta y cancela órdenes, y envía webhooks firmados. Sin credenciales, es lo que permite probar el circuito completo.

### Motor (`internal/order`)

| Método | Qué hace |
| --- | --- |
| `RequestQR(orderID, posID)` | Idempotente: si hay un QR pendiente lo devuelve; si no, lo crea en Mercado Pago (clave de idempotencia = id de orden) y lo guarda |
| `ConfirmFromGateway(providerID)` | `Lookup` → verifica `external_reference` y que sea el cobro guardado de esa orden → aprobada y monto correcto: marca pagada y despacha en segundo plano. Monto distinto: no despacha, la orden queda en `pending_payment`, el pago en `amount_mismatch` y se avisa `payment_mismatch`. Estado desconocido: avisa `payment_unknown` y no toca nada |
| `ReconcilePayments(limit)` | En el barrido, sobre QR pendientes de más de 10 s: aprobada → mismo camino que el webhook; vencida o cancelada → cancela la orden; pendiente y pasado `expires_at` + 30 s → `Cancel` en Mercado Pago y se obedece la respuesta |

Las escrituras van siempre en el orden **primero el efecto, después el pago**: si el proceso muere entre las dos, el pago sigue `pending` y el barrido completa lo que faltó. Si el proceso muere después de marcar pagada y antes de despachar, el barrido existente (`resumeStalledLine`) retoma las líneas `pending`. Si muere antes de marcar pagada, `ReconcilePayments` encuentra el QR aprobado.

### HTTP

- `POST /orders` = `Place` + `RequestQR`. La respuesta suma `"payment": {"status", "qr_data", "expires_at"}`. Si Mercado Pago falla: 502, y la tablet reintenta con el mismo `client_key` sin duplicar orden ni QR.
- `GET /orders/{id}` incluye el mismo bloque `payment`.
- `POST /callbacks/mercadopago/{secret}`: secreto de ruta → firma → `type == order` → `ConfirmFromGateway` → 200.
- `POST /orders/{id}/pay` sigue sólo en `sandbox`.

### Configuración

- Entorno: `MP_ACCESS_TOKEN`, `MP_WEBHOOK_SECRET`, `MP_CALLBACK_SECRET`, `MP_BASE_URL` (por defecto `https://api.mercadopago.com`).
- Por mueble: `"mp_pos_id"` (el `external_pos_id` de su caja).
- En `production` Mercado Pago es obligatorio; en `sandbox` es opcional y sin él queda el pago simulado.

## Orden de implementación

1. Migración `0007` + store (memoria y Postgres).
2. Puerto + adaptador de Mercado Pago + firma + Mercado Pago falso.
3. Motor: `RequestQR`, `ConfirmFromGateway`, `ReconcilePayments`.
4. HTTP + configuración + wiring en `cmd/api`.
5. Documentación.

## Fuera del código

- Crear la aplicación de Mercado Pago propia y obtener: access token (prueba y producción), secreto del webhook y user id.
- Dar de alta una sucursal y una caja por mueble.
- Configurar el webhook de la aplicación con el tópico «Order» apuntando a `PUBLIC_BASE_URL/callbacks/mercadopago/{MP_CALLBACK_SECRET}`.
