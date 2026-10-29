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
cmd/api          backend for the tablet (health endpoint only, for now)
cmd/fakeapi      runs both local stand-ins
internal/config  topology from JSON, secrets from env, validated at startup
internal/dispenser          the port: Check / Dispatch / Reconcile
internal/dispenser/batidos       signature and UTC+8 campaign dates
internal/dispenser/snacks  HMAC signature, nonce, clock window
internal/fakeapi            local Batidos and Snacks, with their real rules enforced
internal/payments           Refunder port + mock that never moves money
internal/notify             Notifier port + structured log transport
migrations                 PostgreSQL schema
```

## Run

```bash
go test ./...            # includes the signature vectors taken from the docs
go vet ./...
go build ./...

cp config.example.json config.local.json     # topology only, no values
go run ./cmd/api -config config.local.json   # GET /healthz
```

Local stand-ins, so integration work can start without credentials:

```bash
go run ./cmd/fakeapi
# fake batidos API  -> http://127.0.0.1:9401
# fake snacks API   -> http://127.0.0.1:9402
```

## Secrets

Credentials never live in a tracked file. They come from four environment
variables, which override anything written in the JSON:

| Variable | Provider |
| --- | --- |
| `BATIDOS_KEY` | Batidos (GS merchant API) |
| `BATIDOS_SECRET` | Batidos |
| `SNACKS_ACCESS_KEY` | Snacks (OurVend open API) |
| `SNACKS_APP_SECRET` | Snacks |

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
