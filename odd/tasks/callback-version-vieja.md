# F8 — Callback de batidos con una versión vieja de la línea

Fix F8 de `odd/tasks/revision-produccion.md`, rama `fix/batidos-callback-stale-line` desde `main`.
`markBrewing` guarda la línea (la versión sube) pero descarta la línea guardada; después `extendReconcile` guarda con la versión vieja, recibe `ErrStale` y lo ignora: la extensión del plazo se pierde sin error.

## Specs

- **S1** — Después de un callback «progress» de batidos (`SyncLineFromCallback`, `internal/order/callback.go:65-78`), la reconciliación trabaja sobre la línea **guardada** por `markBrewing` (`callback.go:222-243`), no sobre la copia vieja: si la máquina no terminó, el próximo chequeo queda en `now + reconcileInterval` (sin pasarse de `ReconcileUntil`) y `LastError` se guarda; si venció la ventana, la línea pasa a `in_doubt` en ese momento.
- **S2** — El otro llamador de `markBrewing` (`callback.go:214`) también usa la línea guardada si sigue trabajando con ella.
- **S3** — Si `markBrewing` recibe `ErrStale` porque otro proceso cambió la línea, el callback no sigue con una copia vieja: recarga la línea o deja que la reconciliación la recargue, de modo que ningún guardado posterior se pierda en silencio.
- **S4** — Revisar si `extendReconcile` (`internal/order/reconcile.go:210-250`) debería recargar y reintentar ante `ErrStale` como hace `closeLine` (`reconcile.go:154-175`). Informar con argumento; cambiarlo sólo si una línea extendida podría perderse en otro camino además del de S1.

## Tasks

- [x] T1 — S1-S4 — writer `gentle-ai-worker` — commit: 6bef9cd; tests en `archive/tests` f8e7143.

## Log

- **L1** — «Si dale.» a publicar F7 y arrancar F8 (2026-11-22).
- **L2** — Mapa: `closeLine` recarga ante `ErrStale`, así que entregado y fallido no se pierden. Lo que se pierde es la extensión del plazo, `LastError` y el paso a `in_doubt` cuando vence la ventana (lo hace el barrido más tarde).
- **L3** — Evidencia: RED (sin `LastError`, `DeadlineAt` sin extender, línea cerrada reconciliada igual); GREEN; la suite de `archive/tests` da exit 0. Verificación independiente: S1-S4 PASS sin bloqueantes. Su should-fix (un doble stale sin log) se corrigió con un `warn`; el camino de error ahora devuelve la línea sin modificar y el test comprueba que se conserva el `LastError` del otro escritor. S4: `extendReconcile` no cambia, porque un stale fuera de S1 significa que escribió alguien más nuevo y el barrido retoma la línea vencida. Duplicación anotada: «recargar y reintentar ante stale» en `markBrewing` y `closeLine` (2 lugares, se deja así). No verificado: Postgres real, `-race`.
