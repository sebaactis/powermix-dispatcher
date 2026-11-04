BEGIN;

ALTER TABLE order_line ADD COLUMN IF NOT EXISTS reconcile_until timestamptz;

COMMIT;
