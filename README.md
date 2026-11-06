# compras-remotas

Backend for a kiosk that sells combos: one tablet, two Powermix vending
machines. A single purchase must dispatch the drink to the batidos machine and
the snack to the snacks machine, independently.

```
tablet (Kotlin, later)  ->  this backend  ->  Batidos API  ->  batidos machine
                                        ->  Snacks API   ->  snacks machine
```

The tablet never talks to a machine: both providers forbid shipping the secret in
a client, and neither API is callable from an app.

## The two machines are not symmetrical

| | Batidos | Snacks |
| --- | --- | --- |
| Dispatch | create a redemption campaign, then redeem the code | direct remote shipment by product |
| Calls per item | 2 | 1 |
| Auth | `key`, `timestamp`, `key-md5 = MD5(key+secret+timestamp)` | `accessKey`, `timestamp`, `nonce`, `sign = Base64(HMAC-SHA256)` |
| Campaign dates | strings read as **UTC+8** | n/a |

That asymmetry is the reason the order engine only knows the `dispenser.Dispenser`
port: it never learns that a drink needs a code.

## Layout

```
cmd/api          backend for the tablet: catalog, orders, live status, simulated pay
cmd/fakeapi      runs both local stand-ins plus their test-only admin surface
internal/config  topology from JSON, secrets from env, validated at startup
internal/dispenser          the port: Check / Dispatch / Reconcile
internal/dispenser/batidos       signature and UTC+8 campaign dates
internal/dispenser/snacks  HMAC signature, nonce, clock window
internal/fakeapi            local Batidos and Snacks, with their real rules enforced
internal/httpapi            the four tablet endpoints, mapped onto catalog + engine + store
internal/payments           Refunder port + mock that never moves money
internal/notify             Notifier port + structured log transport
migrations                 PostgreSQL schema
docs                       comments and rationale, kept out of the code
```

The source carries **no comments on purpose**. Every explanation, warning and
decision lives in `docs/comentarios-codigo.md`, indexed by symbol
(`Símbolo --> "texto"`). Keep it updated when behaviour changes; a stale note is
worse than none.

## Run

```bash
go test ./...            # includes the signature vectors taken from the docs
go vet ./...
go build ./...

cp config.example.json config.local.json     # topology only, no values
cp combos.example.json combos.local.json     # the menu

go run ./cmd/api -config config.local.json -combos combos.local.json

# GET  /healthz
# GET  /combos
# POST /orders               {"combo_id":"...","client_key":"...","kiosk_id":"..."}
# GET  /orders/{id}
# POST /orders/{id}/pay      simulated, sandbox only: 403 in production
```

Local stand-ins, so integration work can start without credentials:

```bash
go run ./cmd/fakeapi
# fake batidos API  -> http://127.0.0.1:9401
# fake snacks API   -> http://127.0.0.1:9402
# admin surface     -> http://127.0.0.1:9403   (POST only, loopback only)
```

The fake also seeds one listed batidos product on startup, so a fresh fake plus
this backend can sell a combo without preparing anything: `-batidos-goods`
(`goods-1` by default) for the device named by `-batidos-device` (`dev-e2e-1` by
default). Pass `-batidos-goods ""` to start with an empty batidos catalogue and
load the products through the admin surface instead. The snacks fake needs no
seed: it already starts with slots that have stock.

### Admin surface of the fake

Test-only, and thin: every route calls one setter the fake already had, so an
end-to-end run can put the machines in the state it needs. Every route is `POST`,
takes a JSON body, answers `{"ok":true}` or `{"ok":false,"error":"…"}`, and
rejects bodies with unknown fields. Values are not persisted: a restart of the
fake forgets them.

| Route | Body | Effect |
| --- | --- | --- |
| `/admin/batidos/goods` | `{"device_id":"dev-e2e-1","goods":[{"id":"goods-1","listing":true,"sold_out":0}]}` | replaces the device catalogue (`[]` leaves it empty) |
| `/admin/batidos/offline` | `{"device_id":"dev-e2e-1","offline":true}` | takes the device offline; `false` is rejected because there is no setter to bring it back |
| `/admin/batidos/abnormal` | `{"device_id":"dev-e2e-1","abnormal":true}` | marks a fault; `false` is rejected for the same reason |
| `/admin/batidos/out_of_stock` | `{"device_id":"dev-e2e-1","out_of_stock":true}` | marks it without supplies; `false` is rejected for the same reason |
| `/admin/batidos/advance` | `{"code":"000001","status":2}` | advances a redemption code, so `getOrderInfo` reports `0` failed, `1` brewing or `2` done |
| `/admin/snacks/device` | `{"online":true,"sell_status":1,"fault_state":0,"lock_type":0,"stockout_state":0}` | applies only the fields present |
| `/admin/snacks/slot` | `{"slot_no":"1","stock":5,"slot_status":1}` | sets the stock and/or the status of one slot |
| `/admin/snacks/order` | `{"status":1,"name":"…"}` or `{"empty":true}` | changes what the orders page reports, or empties it |

> Local tests only. There is no authentication, so the admin server must never be
> reachable from another machine: `-admin-addr` defaults to `127.0.0.1:9403` and
> the process refuses to start with a non-loopback host.

## Secrets

Credentials never live in a tracked file. They come from four environment
variables, which override anything written in the JSON:

| Variable | Provider |
| --- | --- |
| `BATIDOS_KEY` | Batidos (GS merchant API) |
| `BATIDOS_SECRET` | Batidos |
| `SNACKS_ACCESS_KEY` | Snacks (OurVend open API) |
| `SNACKS_APP_SECRET` | Snacks |
| `BATIDOS_CALLBACK_SECRET` | Secret path segment for the Batidos callback (optional) |
| `SNACKS_CALLBACK_SECRET` | Secret path segment for the Snacks callback (optional) |
| `PUBLIC_BASE_URL` | Public base URL of this backend (optional). Without it the Batidos campaign is created without `notifyUrl`, so the Batidos callback never arrives |

Both callback secrets are optional. When a secret is empty, that callback
endpoint answers 404: an unauthenticated machine callback never reaches the
order. The Batidos callback is not signed by the provider (R6), so the secret
lives in the URL path instead of a header.

Keep them in a local `.env` file, which is git-ignored, and load it in the shell:

```bash
set -a; source .env; set +a
```

Rules, so nothing leaks into history:

- `.env` and `*.local.json` are ignored by git. Do not force-add them.
- `config.example.json` keeps placeholders (`replace-me`). It is committed, so it
  must never hold a real value, not even a revoked one.
- Test fixtures (`mysecret`, `testsecret`, `ak_test`) are synthetic values for the
  local fakes. They are not credentials for anything, but a secret scanner may
  flag them; that is expected.

## What is in the repository

Code, plus `odd/tasks/combos-remotos.md`: the specification, the decisions taken
and the questions still open. It is versioned on purpose — it is the only record
of why the code looks the way it does.

## What is not in the repository

| Kept out | Why |
| --- | --- |
| `Batidos-Remoto-API.md`, the Snacks PDF, `ys-vending.txt` | Third-party material with the vendor's copyright notice |
| `design/` | Internal design boards and their screenshots |
| `.env`, `*.local.json` | Real credentials and real topology |

Consequence: a fresh clone cannot read the source contracts. The wire rules those
documents define live in code and in tests (`internal/dispenser/*/sign*.go`, the
`openssl` vectors), which is what actually prevents a regression.

## Tests that exist because the docs were ambiguous

- Two Base64 HMAC vectors and two MD5 vectors computed with `openssl`, not with Go,
  so the test cannot agree with the implementation by accident.
- `CampaignTime` is checked across a midnight boundary: sending local time instead
  of UTC+8 pushes the campaign eleven hours into the future.
- The fake Batidos rejects a reused exchange code; the fake Snacks rejects a replayed
  nonce and a bad signature.

## Known open questions

These still block real integration: the real Batidos API domain, whether a
redemption code is single use, whether `deviceNo` is really required, the exact
JSON of the Snacks remote shipment call, and whether `PaymentOrderId` is an
idempotency key. They are tracked in `odd/tasks/combos-remotos.md`.
