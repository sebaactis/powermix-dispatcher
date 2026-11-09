# Endurecimiento tras la revisión

Correcciones surgidas de la revisión de buenas prácticas, en el orden recomendado, un paso por vez.
Rama: `fix/review-hardening`. Cada paso espera la decisión del usuario cuando es estructural.

## Specs

- **S1** (paso 1a, opción A aceptada): «Cambiar a `Success *bool` y rechazar solo si viene presente y es `false`.» Aplica a `Ship` y a `get` del cliente de snacks. Una respuesta sin `success` y con `code` 0 o 200 sigue aceptándose, porque la sección general de la documentación (`ys-vending.txt:351-363`) no trae ese campo.
- **S2** (paso 1b): el callback de snacks no se firma según la documentación (`ys-vending.txt:2023-2075`); corregir `docs/comentarios-codigo.md`, que afirma que nuestros callbacks usan `VerifySignature`, y anotar el riesgo.
- **S3** (paso 2): transiciones monótonas de línea y orden guardada con versión. Pendiente de decisión.
- **S4** (paso 3): recuperar líneas `pending` con intento ya registrado. Pendiente de decisión.
- **S5** (paso 4): timeouts del servidor, despacho con `context.WithoutCancel`, chequeo previo en paralelo. Pendiente de decisión.
- **S6** (paso 5): versionado de migraciones y tests de Postgres y `-race`. Pendiente de decisión.

## Tasks

| ID | Specs | Ruta | Commit |
| --- | --- | --- | --- |
| T1 | S1 | inline | `415fdb1` hecho |
| T2 | S2 | inline | ver commit `docs(snacks)` hecho |
| T3 | S3 | pendiente | pendiente |
| T4 | S4 | pendiente | pendiente |
| T5 | S5 | pendiente | pendiente |
| T6 | S6 | pendiente | pendiente |

## Log

- **L1**: «Vayamos por tu orden recomendado, paso a paso. Ya sabes como comportarte segun mi agent.md»
- **L2**: la documentación del proveedor muestra dos formas de respuesta: con `success` (endpoints concretos) y sin él (sección general). El callback no lleva firma documentada. Dos hallazgos «alta» de la revisión bajan: `success` a media, la firma del callback pasa a corrección de documentación.
- **L3**: «Ok. Implementa la opcion A.»
