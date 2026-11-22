# Despacho: errores ambiguos sin reintento

Un error del proveedor que no prueba que el despacho no ocurrió deja la línea `in_doubt` para reconciliar, nunca reintenta. Evita el doble despacho.
Rama: `fix/partial-order-alerts` (sigue sobre el fix 1). Tests: flujo `archive/tests` (`docs/arquitectura.md`, «Tests: dónde viven y cómo se usan»).

## Specs

- **S1** — Pregunta: «Cuando un proveedor responde 5xx, 429, timeout o una respuesta ilegible al despachar, ¿qué hacemos?» Respuesta: «No reintentar: in_doubt y reconciliar (recomendado)» — «Nunca arriesga un doble despacho. El barrido consulta al proveedor y, si no logra confirmar, soporte recibe el aviso de in_doubt. Solo se reintenta un rechazo de negocio claro (4xx con código del proveedor)».
- **S2** — Pregunta: «En batidos, si el código de canje ya se emitió y falla el paso de preparar, ¿qué hacemos?» Respuesta: «Reconciliar con ese código, sin campaña nueva» — «la línea queda in_doubt con el código guardado y el barrido consulta OrderInfo con ese código, que ya funciona (batidos/dispenser.go:136)».
- **S3** — Un rechazo de negocio claro antes de que el proveedor emita algo (4xx con código del proveedor) sigue siendo `Failed` y se reintenta como hoy.

## Tasks

- [x] T1 — S1, S2, S3 — clasificación en `asPortRejection` de batidos y snacks; paso 2 de batidos siempre ambiguo — worker — commit: 9996d4e

## Log

- **L1** — «Seguí con el fix 2. Te sumo: para el catalogo, pone el price_cents porque son datos mockeados, por ahora no son los datos reales.»
- **L2** — Evidencia: `classifyDispatch` (`internal/order/dispatch.go:131-140`) trata todo `*dispenser.Rejection` como `Failed` y reintenta (`dispatch.go:87`). `batidos/client.go:327-338` y `snacks/client.go:459-467` convierten todo status no-200, y en batidos también el JSON inválido, en `APIError` → `Rejection` (`asPortRejection`). El paso 2 de batidos (`ProduceCoffee`, `batidos/dispenser.go:117-131`) envuelve el error como `Rejection`, y el reintento crea otra campaña. `Rejection` sólo lo consume `classifyDispatch`.
- **L3** — Decisión de diseño: cada adaptador decide qué es rechazo definitivo en `asPortRejection`; el motor no cambia.
- **L4** — T1: RED en `TestDispenserDispatchTreatsAmbiguousProviderResponsesAsNonRejection` (batidos y snacks), `TestDispenserDispatchReturnsNonRejectionWithRefWhenProduceCoffeeFails` y `TestEngineDispatchCreatesOnlyOneCampaignWhenBatidosStep2Fails` («se crearon 2 campañas, se esperaba exactamente 1»); GREEN `go test -count=1 ./...` exit 0. Regla: `Rejection` sólo si el sobre trae `code` y el status es 200 o 4xx salvo 408/429. `Reconcile` ahora devuelve error plano ante 5xx; el barrido lo trata igual (`extendReconcile`). Tests archivados en `archive/tests`, sin push.
