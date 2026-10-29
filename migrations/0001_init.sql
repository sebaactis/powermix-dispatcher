-- Esquema inicial del sistema de combos en muebles expendedores.
--
-- PostgreSQL. Se aplica con: psql "$DATABASE_URL" -f migrations/0001_init.sql
-- Las claves primarias son text porque las genera la aplicación.
-- El dinero se guarda como centavos enteros, nunca como punto flotante.

BEGIN;

CREATE TABLE kiosk (
    id         text PRIMARY KEY,
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Una máquina de cada tipo por mueble: un combo siempre empareja una bebida con
-- un snack, así que el par es la clave natural.
CREATE TABLE machine (
    kiosk_id          text NOT NULL REFERENCES kiosk(id) ON DELETE CASCADE,
    type              text NOT NULL CHECK (type IN ('batidos', 'snacks')),
    name              text NOT NULL,
    batidos_device_id      text,
    batidos_device_no      text,
    batidos_device_type_id text,
    snacks_mid       text,
    PRIMARY KEY (kiosk_id, type),
    CHECK (
        (type = 'batidos' AND batidos_device_id IS NOT NULL AND batidos_device_no IS NOT NULL AND batidos_device_type_id IS NOT NULL)
        OR (type = 'snacks' AND snacks_mid IS NOT NULL)
    )
);

CREATE TABLE combo (
    id          text PRIMARY KEY,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    price_cents bigint NOT NULL CHECK (price_cents >= 0),
    currency    char(3) NOT NULL DEFAULT 'ARS',
    active      boolean NOT NULL DEFAULT true,
    sort_order  int NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE combo_item (
    combo_id     text NOT NULL REFERENCES combo(id) ON DELETE CASCADE,
    position     int NOT NULL,
    machine_type text NOT NULL CHECK (machine_type IN ('batidos', 'snacks')),
    sku          text NOT NULL,
    sku_name     text NOT NULL DEFAULT '',
    PRIMARY KEY (combo_id, position)
);

CREATE TABLE payment_intent (
    id                 text PRIMARY KEY,
    order_id           text NOT NULL,
    provider           text NOT NULL,
    provider_intent_id text,
    amount_cents       bigint NOT NULL CHECK (amount_cents >= 0),
    status             text NOT NULL CHECK (status IN ('pending', 'approved', 'rejected', 'expired')),
    qr_payload         text,
    created_at         timestamptz NOT NULL DEFAULT now(),
    approved_at        timestamptz
);

-- El webhook del proveedor de pagos puede llegar más de una vez; la misma
-- intención nunca debe procesarse dos veces.
CREATE UNIQUE INDEX payment_intent_provider_intent_idx
    ON payment_intent (provider, provider_intent_id)
    WHERE provider_intent_id IS NOT NULL;

-- Se llama "orders" porque ORDER es una palabra reservada en SQL.
CREATE TABLE orders (
    id           text PRIMARY KEY,
    kiosk_id     text NOT NULL REFERENCES kiosk(id),
    combo_id     text NOT NULL REFERENCES combo(id),
    amount_cents bigint NOT NULL CHECK (amount_cents >= 0),
    currency     char(3) NOT NULL DEFAULT 'ARS',
    -- Estado global, derivado de las líneas. "partial" e "in_doubt" existen
    -- porque las dos máquinas fallan de forma independiente.
    status       text NOT NULL CHECK (status IN (
                     'pending_payment', 'paid', 'dispatching', 'delivered',
                     'partial', 'in_doubt', 'refunded', 'cancelled')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    paid_at      timestamptz,
    closed_at    timestamptz
);

-- La unidad de ejecución: un producto, una máquina, su propio ciclo de vida.
CREATE TABLE order_line (
    id            text PRIMARY KEY,
    order_id      text NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    position      int NOT NULL,
    machine_type  text NOT NULL CHECK (machine_type IN ('batidos', 'snacks')),
    sku           text NOT NULL,
    amount_cents  bigint NOT NULL CHECK (amount_cents >= 0),
    state         text NOT NULL CHECK (state IN (
                      'pending', 'sent', 'brewing', 'delivered',
                      'failed', 'in_doubt', 'refunded')),
    external_ref  text,  -- orderNo de Batidos o subOrderId de Snacks
    exchange_code text,  -- código de canje usado por esta línea, solo batidos
    attempts      int NOT NULL DEFAULT 0,
    -- Vencimiento de la línea. Una línea despachada que no tiene respuesta para
    -- esa hora no queda fallida sino in_doubt: hay que reconciliarla antes de
    -- cualquier otra cosa.
    deadline_at   timestamptz,
    last_error    text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (order_id, position)
);

-- Sirve al worker de reconciliación: buscar líneas vencidas.
CREATE INDEX order_line_state_deadline_idx ON order_line (state, deadline_at);

-- Auditoría de cada llamada hecha a una máquina. Sin esto, un timeout nos deja
-- sin poder saber si el producto salió.
CREATE TABLE dispatch_attempt (
    id               text PRIMARY KEY,
    line_id          text NOT NULL REFERENCES order_line(id) ON DELETE CASCADE,
    attempt_no       int NOT NULL,
    idempotency_key  text NOT NULL,
    request_payload  jsonb NOT NULL,
    response_payload jsonb,
    http_status      int,
    outcome          text NOT NULL CHECK (outcome IN ('accepted', 'rejected', 'ambiguous', 'error')),
    error_detail     text,
    started_at       timestamptz NOT NULL DEFAULT now(),
    finished_at      timestamptz,
    UNIQUE (line_id, attempt_no)
);

CREATE UNIQUE INDEX dispatch_attempt_idempotency_idx ON dispatch_attempt (idempotency_key);

-- Webhooks entrantes en crudo, guardados antes de procesarse y deduplicados por
-- la clave de evento del propio proveedor. Una repetición debe ser inofensiva,
-- no una segunda bebida.
CREATE TABLE webhook_event (
    id           text PRIMARY KEY,
    source       text NOT NULL CHECK (source IN ('payment', 'batidos', 'snacks')),
    dedupe_key   text NOT NULL,
    payload      jsonb NOT NULL,
    received_at  timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz,
    error_detail text
);

CREATE UNIQUE INDEX webhook_event_dedupe_idx ON webhook_event (source, dedupe_key);

-- Última foto conocida de lo que cada máquina puede vender; la usa el chequeo
-- previo que habilita el QR. machine_key es "<kiosk_id>:<machine_type>".
CREATE TABLE catalog_snapshot (
    machine_key text NOT NULL,
    sku         text NOT NULL,
    name        text NOT NULL DEFAULT '',
    available   boolean NOT NULL,
    -- Los snacks informan stock real por canal; los batidos solo un indicador de
    -- agotado, así que para ellos esta columna queda NULL.
    stock       numeric,
    price_cents bigint,
    captured_at timestamptz NOT NULL DEFAULT now(),
    raw         jsonb,
    PRIMARY KEY (machine_key, sku)
);

COMMIT;
