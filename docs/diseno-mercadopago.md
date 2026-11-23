# Diseño: cobro con QR de Mercado Pago

Implementado y fusionado en `main`, probado de punta a punta contra el Mercado Pago simulado de `cmd/fakeapi`. Queda pendiente validarlo contra el sandbox real de Mercado Pago (ver [pendientes.md](pendientes.md)).

Para la arquitectura general del backend consulta [arquitectura.md](arquitectura.md); para la configuración de variables y prueba local, [README.md](../README.md).

---

## Decisiones de diseño

| # | Decisión | Motivo |
| --- | --- | --- |
| 1 | **QR dinámico** (`config.qr.mode: "dynamic"`) en `POST /v1/orders` | Cada orden genera un QR único; nadie puede pagar la orden de otro |
| 2 | **Vencimiento del QR a los 180 s** (`expiration_time: "PT3M"`, `defaultQRExpiry`) | Mismo criterio que `QR_EXPIRY_SEC` en `vps-powermix` |
| 3 | **Si Mercado Pago informa pago aprobado, se despacha**, incluso tras vencer el plazo local | Sin devoluciones en el MVP, cobrar sin entregar es el peor escenario |
| 4 | **Validación estricta del monto contra el catálogo** | Invariante 1: si difiere, no se despacha (`amount_mismatch`) y se avisa a soporte |
| 5 | **Aplicación propia de Mercado Pago** para `compras-remotas` | El webhook es uno por aplicación; evita mezclar avisos de otros sistemas |
| 6 | **El webhook responde rápido y el despacho corre en segundo plano (`e.spawn`)** | `dispatchLines` puede tardar hasta 2 min y Mercado Pago reintenta si no recibe `200` |
| 7 | **Secreto en la ruta además de la firma `X-Signature`** | Misma defensa que los callbacks de máquinas |
| 8 | **La tablet consulta `GET /orders/{id}` cada 2 s** | Suficiente para el MVP sin agregar SSE ni WebSocket |
| 9 | **Todo o nada en configuración** | Con Mercado Pago activo, `POST /orders/{id}/pay` responde `403` también en `sandbox`; una configuración incompleta falla al arrancar (`internal/config/validate.go:90-126`) |
| 10 | **`qr_data` sólo viaja mientras el cobro está `pending`** | Evita que la tablet muestre un QR que ya no puede pagarse (`internal/httpapi/responses.go:34-36`) |

## Regla central: manda Mercado Pago, no nuestro reloj

Nunca se cancela una orden por cuenta propia. Al vencer el plazo (`expires_at + 30 s` de gracia), el barrido pide la cancelación a Mercado Pago y obedece su respuesta: si dice «pagada» (`processed`), se despacha; si dice «cancelada» o «vencida», recién entonces pasa nuestra orden a `cancelled`. Así no hace falta abrir la transición `cancelled → paid` (`internal/order/status.go:51-52`).

## Requisitos del contrato de Mercado Pago

- `config.qr.external_pos_id` es obligatorio en modo dinámico: cada mueble requiere una sucursal y una caja dadas de alta en Mercado Pago (`mp_pos_id`).
- La URL del webhook se configura en el panel de la aplicación de Mercado Pago bajo el tópico **«Order»** (no viaja en el payload de `POST /v1/orders`).
- `POST /v1/orders` exige `X-Idempotency-Key`. `external_reference` admite hasta 64 caracteres `[A-Za-z0-9_-]`, que nuestro ID de orden cumple (`internal/payments/mercadopago/client.go:123`).
- `expiration_time` es una duración ISO 8601 (`PT3M`).

## El webhook

```text
POST /callbacks/mercadopago/{secret}?data.id=ORD01JQ4S4KY8HWQ6NA5PXB65B3D3&type=order
X-Request-Id: 2066ca19-c6f1-498a-be75-1923005edd06
X-Signature: ts=1742505638683,v1=ced36ab6…
```

| Dato | Descripción |
| --- | --- |
| `data.id` | ID de la orden en Mercado Pago (`ORD…`) devuelto por `POST /v1/orders`. Se lee de la query, que es lo que firma Mercado Pago; si no viene, se toma `data.id` (y `type`) del cuerpo JSON |
| `x-request-id` | ID único del envío del aviso (usado en el manifiesto de firma, no para deduplicar) |
| `x-signature` | `ts` (milisegundos para el tópico «order»; también se aceptan segundos, porque la documentación de Mercado Pago usa ambos) y `v1 = hex(HMAC-SHA256(secreto_webhook, "id:<data.id>;request-id:<x-request-id>;ts:<ts>;"))`, con `data.id` en minúsculas y omitiendo pares vacíos (`internal/payments/mercadopago/signature.go:81-93`) |

Validación en `internal/httpapi/mercadopago.go:78` (`receive`):

1. Secreto de ruta en tiempo constante → si falla, `404`.
2. `data.id` en la query o, si falta, en el cuerpo (con un `WARN` en el log) → si no está en ninguno, `400`.
3. Firma `X-Signature` (calculada con el `data.id` de la query; si no vino, el par `id:` se omite, como hace Mercado Pago) y ventana `SignatureTolerance` de 5 min (`internal/payments/mercadopago/signature.go:14-49`) → si falla, `401`.
4. `type == "order"` → si es otro tópico, responde `200` sin tocar nada.
5. `SyncPayment` (`GET /v1/orders/{data.id}`): nunca confía en el estado que trae el cuerpo del webhook. Si el cobro no pertenece al sistema (`ErrUnknownCharge`), responde `200`; ante error transitorio responde `500` para que Mercado Pago reintente.

La deduplicación explícita no es necesaria: `applyCharge` ignora cualquier `PaymentIntent` que ya no esté en `pending` (`internal/order/charge.go:74`) y `claimPaid` ignora órdenes que ya salieron de `pending_payment` (`internal/order/payment.go:32`).

## Piezas

### Datos — migración `0007`

`migrations/0007_payment_intent.sql` completa la tabla `payment_intent` con:

- FK `order_id → orders(id)` e índice único `payment_intent_order_idx` (un cobro por orden).
- `expires_at timestamptz`, `paid_amount_cents bigint` y `version bigint` para *optimistic locking*.
- `CHECK` con `pending`, `approved`, `rejected`, `expired`, `cancelled` y `amount_mismatch`.

Métodos en `internal/store/store.go:129-132`: `CreatePaymentIntent`, `PaymentIntentByOrder`, `SavePaymentIntent` (`ErrStale`) y `PendingPaymentIntents`.

### Puerto `payments.Gateway`

Definido en `internal/payments/gateway.go:42-46`:

```go
type Gateway interface {
	CreateQR(ctx context.Context, req QRRequest) (Charge, error)
	Lookup(ctx context.Context, providerID string) (Charge, error)
	Cancel(ctx context.Context, providerID string) (Charge, error)
}
```

`Charge` incluye `ProviderID`, `ExternalRef`, `QRData`, `Status` (`pending`, `approved`, `expired`, `cancelled`, `unknown`), `ProviderStatus`, `ProviderStatusDetail`, `AmountCents` y `PaidAmountCents`. `unknown` cubre `refunded`, `failed` o estados no previstos: no despacha ni cancela y avisa a soporte.

### Adaptador `internal/payments/mercadopago`

- `client.go`: llamadas HTTP (`net/http`, sin SDK) a `/v1/orders` y `/v1/orders/{id}/cancel`.
- `wire.go`: structs de transporte JSON del proveedor.
- `signature.go`: verificación de `X-Signature` con tolerancia de 5 min.
- `status.go`: mapeo `created` / `processed` / `expired` / `canceled` → `ChargeStatus` (pendiente confirmar contra sandbox real).
- `amount.go`: parseo y formato decimal entre centavos (`int64`) y strings de monto.

### Mercado Pago simulado (`internal/fakeapi`)

Escucha por defecto en `127.0.0.1:9404` (`cmd/fakeapi/main.go:48`): crea, consulta y cancela órdenes y envía webhooks firmados hacia `-mp-webhook-url`.

### Motor (`internal/order`)

| Método | Comportamiento |
| --- | --- |
| `RequestQR` (`qr.go:21`) | Idempotente: si ya existe `PaymentIntent` para la orden, lo devuelve; si no, verifica estado `pending_payment` (`ErrNotPayable`), crea el QR (`X-Idempotency-Key = orderID`) y lo persiste |
| `SyncPayment` (`charge.go:43`) | `Lookup` → valida `external_reference` y `ProviderID` → si `approved` con monto exacto: `claimPaid` + `e.spawn` (`dispatchLines` en segundo plano con `recover()`); si difiere el monto: `amount_mismatch` + aviso `payment_mismatch`; si `expired`/`cancelled`: cancela la orden; si `unknown`: aviso `payment_unknown` |
| `SyncPendingPayments` (`reconcile_payments.go:17`) | Ejecutado en cada tick de 30 s del barrido sobre QRs `pending` con > 10 s de antigüedad; si pasaron > 30 s desde `expires_at` y sigue `pending`, llama a `Cancel` en Mercado Pago y aplica la respuesta |

Las escrituras siguen siempre el orden **efecto antes que cierre del cobro**: si el proceso muere entre `claimPaid` y `SavePaymentIntent`, el intento sigue `pending` y el barrido lo completa; si muere tras marcar `paid` y antes de despachar, `resumeStalledLine` retoma las líneas `pending`.

### Borde HTTP y configuración

- `POST /orders` (`internal/httpapi/orders.go:10`): `CreateOrder` + `RequestQR`. Si falla la creación del QR responde `502`; si la orden no es pagable, `409`; un reintento con el mismo `client_key` (y mismo combo/mueble) devuelve orden y QR existentes sin duplicar.
- `GET /orders/{id}` (`internal/httpapi/orders.go:56`): incluye `"payment"` cuando Mercado Pago está activo.
- `POST /orders/{id}/pay` (`internal/httpapi/orders.go:79`): deshabilitado (`403`) con Mercado Pago activo o en `production`.
- Variables de entorno: `MP_ACCESS_TOKEN`, `MP_WEBHOOK_SECRET`, `MP_CALLBACK_SECRET`, `MP_BASE_URL`; por mueble `"mp_pos_id"`. En `production` Mercado Pago es obligatorio; en `sandbox` es opcional.
