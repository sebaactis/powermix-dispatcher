ALTER TABLE payment_intent
    ADD CONSTRAINT payment_intent_order_fk
        FOREIGN KEY (order_id) REFERENCES orders(id) ON DELETE CASCADE,
    ADD COLUMN expires_at        timestamptz,
    ADD COLUMN paid_amount_cents bigint CHECK (paid_amount_cents >= 0),
    ADD COLUMN version           bigint NOT NULL DEFAULT 0,
    DROP CONSTRAINT payment_intent_status_check,
    ADD CONSTRAINT payment_intent_status_check CHECK (status IN (
        'pending', 'approved', 'rejected', 'expired', 'cancelled', 'amount_mismatch'));

CREATE UNIQUE INDEX payment_intent_order_idx ON payment_intent (order_id);

CREATE INDEX payment_intent_pending_idx ON payment_intent (created_at) WHERE status = 'pending';
