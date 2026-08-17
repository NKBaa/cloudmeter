BEGIN;

CREATE TABLE usage_charges (
    id bigserial PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    usage_code text NOT NULL,
    window_start timestamptz NOT NULL,
    window_end timestamptz NOT NULL,
    pricing_version_id uuid NOT NULL REFERENCES pricing_versions(id),
    quantity numeric(30,12) NOT NULL CHECK (quantity >= 0),
    amount_cents bigint NOT NULL CHECK (amount_cents >= 0),
    wallet_ledger_entry_id bigint UNIQUE REFERENCES wallet_ledger_entries(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, usage_code, window_start, window_end)
);

CREATE TABLE usage_billing_attempts (
    id bigserial PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    usage_code text NOT NULL,
    window_start timestamptz NOT NULL,
    window_end timestamptz NOT NULL,
    pricing_version_id uuid NOT NULL REFERENCES pricing_versions(id),
    amount_cents bigint NOT NULL CHECK (amount_cents >= 0),
    status text NOT NULL CHECK (status IN ('charged','insufficient_funds')),
    balance_cents bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, usage_code, window_start, window_end, status, balance_cents)
);
CREATE INDEX usage_billing_attempts_user_created_idx ON usage_billing_attempts(user_id, created_at DESC);

ALTER TABLE user_apps ADD COLUMN suspension_reason text;
ALTER TABLE user_apps ADD CONSTRAINT user_apps_suspension_reason_check
    CHECK (suspension_reason IS NULL OR suspension_reason IN ('billing_insufficient'));

CREATE FUNCTION deny_pricing_version_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'pricing versions are immutable';
END $$;
CREATE TRIGGER pricing_versions_no_update_delete BEFORE UPDATE OR DELETE ON pricing_versions
FOR EACH ROW EXECUTE FUNCTION deny_pricing_version_mutation();

COMMIT;
