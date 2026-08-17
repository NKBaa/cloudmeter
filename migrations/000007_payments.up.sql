BEGIN;
CREATE TABLE payment_provider_configs (
    provider text PRIMARY KEY CHECK (provider IN ('manual','epay')),
    enabled boolean NOT NULL DEFAULT false,
    merchant_id text NOT NULL DEFAULT '',
    secret text NOT NULL DEFAULT '',
    endpoint text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO payment_provider_configs(provider,enabled) VALUES ('manual',true),('epay',false);
CREATE TABLE payment_orders (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id),
    provider text NOT NULL REFERENCES payment_provider_configs(provider),
    amount_cents bigint NOT NULL CHECK (amount_cents > 0),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','paid','closed','failed')),
    idempotency_key text NOT NULL,
    provider_ref text,
    created_at timestamptz NOT NULL DEFAULT now(),
    paid_at timestamptz,
    UNIQUE(user_id,idempotency_key)
);
CREATE TABLE payment_order_events (
    id bigserial PRIMARY KEY,
    order_id uuid NOT NULL REFERENCES payment_orders(id) ON DELETE CASCADE,
    from_status text,
    to_status text NOT NULL,
    message text,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX payment_orders_user_created_idx ON payment_orders(user_id,created_at DESC);
COMMIT;
