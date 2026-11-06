BEGIN;

ALTER TABLE orders ADD COLUMN IF NOT EXISTS client_key text;

CREATE UNIQUE INDEX IF NOT EXISTS orders_client_key_idx ON orders (client_key) WHERE client_key IS NOT NULL;

ALTER TABLE order_line ADD COLUMN IF NOT EXISTS version bigint NOT NULL DEFAULT 0;

ALTER TABLE order_line
    ADD COLUMN IF NOT EXISTS machine_kiosk_id       text,
    ADD COLUMN IF NOT EXISTS machine_name           text,
    ADD COLUMN IF NOT EXISTS machine_device_id      text,
    ADD COLUMN IF NOT EXISTS machine_device_no      text,
    ADD COLUMN IF NOT EXISTS machine_device_type_id text,
    ADD COLUMN IF NOT EXISTS machine_mid            text;

COMMIT;
