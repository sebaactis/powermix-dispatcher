ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_status_check;

ALTER TABLE orders ADD CONSTRAINT orders_status_check CHECK (status IN (
    'pending_payment', 'paid', 'dispatching', 'delivered',
    'partial', 'failed', 'in_doubt', 'refunded', 'cancelled'));
