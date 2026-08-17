BEGIN;
ALTER TABLE payment_orders ADD COLUMN closed_at timestamptz;
ALTER TABLE payment_orders ADD COLUMN last_queried_at timestamptz;

CREATE TABLE payment_provider_operations (
    id bigserial PRIMARY KEY,
    order_id uuid NOT NULL REFERENCES payment_orders(id),
    provider text NOT NULL REFERENCES payment_provider_configs(provider),
    operation text NOT NULL CHECK (operation IN ('query','close')),
    result text NOT NULL CHECK (result IN ('succeeded','unconfigured','failed')),
    provider_status text,
    message text NOT NULL DEFAULT '',
    request_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX payment_provider_operations_order_created_idx ON payment_provider_operations(order_id,created_at DESC);

CREATE FUNCTION deny_payment_provider_operation_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'payment provider operations are immutable'; END $$;
CREATE TRIGGER payment_provider_operations_no_update_delete BEFORE UPDATE OR DELETE ON payment_provider_operations
FOR EACH ROW EXECUTE FUNCTION deny_payment_provider_operation_mutation();
COMMIT;
