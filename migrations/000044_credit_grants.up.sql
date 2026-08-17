BEGIN;

CREATE TABLE credit_grants (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount_cents bigint NOT NULL CHECK (amount_cents > 0),
    remaining_cents bigint NOT NULL CHECK (remaining_cents >= 0 AND remaining_cents <= amount_cents),
    business_ref text NOT NULL,
    note text NOT NULL DEFAULT '',
    expires_at timestamptz,
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, business_ref)
);
CREATE INDEX credit_grants_available_idx ON credit_grants(user_id,expires_at,created_at) WHERE remaining_cents > 0;

CREATE TABLE credit_consumptions (
    id bigserial PRIMARY KEY,
    credit_grant_id uuid NOT NULL REFERENCES credit_grants(id),
    usage_charge_id bigint NOT NULL REFERENCES usage_charges(id),
    amount_cents bigint NOT NULL CHECK (amount_cents > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (credit_grant_id,usage_charge_id)
);
CREATE INDEX credit_consumptions_charge_idx ON credit_consumptions(usage_charge_id);

CREATE FUNCTION restrict_credit_grant_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN RAISE EXCEPTION 'credit grants cannot be deleted'; END IF;
    IF NEW.user_id <> OLD.user_id OR NEW.amount_cents <> OLD.amount_cents
       OR NEW.business_ref <> OLD.business_ref OR NEW.note <> OLD.note
       OR NEW.expires_at IS DISTINCT FROM OLD.expires_at OR NEW.created_by IS DISTINCT FROM OLD.created_by
       OR NEW.created_at <> OLD.created_at OR NEW.remaining_cents > OLD.remaining_cents THEN
        RAISE EXCEPTION 'credit grant identity is immutable and remaining credit cannot increase';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER credit_grants_restrict_update_delete BEFORE UPDATE OR DELETE ON credit_grants
FOR EACH ROW EXECUTE FUNCTION restrict_credit_grant_mutation();

CREATE FUNCTION deny_credit_consumption_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'credit consumptions are immutable'; END $$;
CREATE TRIGGER credit_consumptions_no_update_delete BEFORE UPDATE OR DELETE ON credit_consumptions
FOR EACH ROW EXECUTE FUNCTION deny_credit_consumption_mutation();

DROP INDEX usage_billing_attempts_snapshot_uidx;
ALTER TABLE usage_billing_attempts ADD COLUMN credit_balance_cents bigint NOT NULL DEFAULT 0 CHECK (credit_balance_cents >= 0);
CREATE UNIQUE INDEX usage_billing_attempts_snapshot_uidx
    ON usage_billing_attempts(user_id,user_app_id,usage_code,window_start,window_end,pricing_version_id,status,balance_cents,credit_balance_cents) NULLS NOT DISTINCT;

COMMIT;
