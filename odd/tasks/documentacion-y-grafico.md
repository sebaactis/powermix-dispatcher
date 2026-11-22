# P2 + P4 — Documentación y gráfico del sistema

Partes P2 y P4 de `odd/tasks/revision-produccion.md`, rama `docs/update-docs-and-diagram` desde `main` (que ya incluye F1-F9).
La documentación y el gráfico tienen que reflejar el código actual de `main`; el código es la fuente de verdad.

## Specs

- **S1** — Documentación (P2, pedido original): «Revisemos que mi doc esté actualizada, clara, y consisa.» Pedido de hoy: «updatea la documentacion». Aplica a `README.md`, `docs/arquitectura.md`, `docs/pendientes.md` y `docs/diseno-mercadopago.md`: corregir lo desactualizado contra el código, sacar repeticiones y contenido obsoleto, y dejar referencias `archivo:línea` correctas. `docs/pendientes.md` refleja lo que queda abierto en `odd/tasks/revision-produccion.md` (F4, F7b, F10-F16, D3-D5, P3) sin duplicar el detalle.
- **S2** — `docs/comentarios-codigo.md` (2640 líneas) actualizado al código actual de `main`: lo que describe funciones o archivos que cambiaron en F1-F9 se corrige, lo que ya no existe se saca, y lo nuevo relevante se agrega con el mismo estilo del documento.
- **S3** — Gráfico (P4, pedido original): «que mi grafico visual tambien esté actualizado y sea claro y moderno». Pedido de hoy: «el grafico completo de todo lo que tenemos. Que el gráfico quede super claro y pueda leer todo lo que tiene la app para entenderla de un golpe de leida.» Archivo: `docs/flujo-del-sistema.svg`. Debe mostrar, legible de un vistazo: el kiosco y la API HTTP (rutas y middleware: request_id, access log, recover), validaciones y chequeo previo de máquinas, la creación del pedido con `client_key`, el cobro con Mercado Pago (QR, webhook, barrido de pagos), el despacho a batidos y snacks con sus estados de línea (`pending`, `sent`, `brewing`, `delivered`, `failed`, `in_doubt`) y estados de orden (incluido `partial`), callbacks de las máquinas, el barrido de reconciliación, los avisos a soporte por Telegram, Postgres, el logueo y el apagado ordenado.
- **S4** — Idioma: la documentación y el gráfico siguen en español, como hoy.

## Tasks

- [x] T1 — S1, S4 — writer `gentle-ai-worker` — commit: 70d32b4.
- [x] T2 — S2, S4 — writer `gentle-ai-worker` — commit: 7dcc10e.
- [x] T3 — S3, S4 — writer `gentle-ai-worker` (segundo intento; el primero se colgó sin producir nada) — commit: 40f5428.

## Log

- **L1** — Pedido original de la revisión: «Revisemos que mi doc esté actualizada, clara, y consisa. Que mi codigo sea usando buenas practicas Y que mi grafico visual tambien esté actualizado y sea claro y moderno.»
- **L2** — «Mergeá. Y antes de cerrar, updatea la documentacion, y el grafico completo de todo lo que tenemos. Que el gráfico quede super claro y pueda leer todo lo que tiene la app para entenderla de un golpe de leida.» (2026-11-22, después de publicar F9).
- **L3** — Evidencia: `go build` y `go vet` pasan; el SVG es XML válido (1621 tokens), sin recursos externos, con letra mínima de 13 px; el writer lo renderizó con Edge headless y no encontró solapamientos. No verificado: los comandos del README no se ejecutaron (se revisaron contra el código); en `comentarios-codigo.md` no se revisaron línea por línea las secciones de paquetes que no cambiaron en F1-F9 (`config`, `machine`, `linestate`, `payments`, `fakeapi`, `topology`, `migrate`, `migrations`, la mayor parte de `store`, `sign.go`).
