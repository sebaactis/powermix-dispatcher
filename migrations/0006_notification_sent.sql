CREATE TABLE notification_sent (
    order_id text NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    kind     text NOT NULL,
    sent_at  timestamptz NOT NULL,
    PRIMARY KEY (order_id, kind)
);
