BEGIN;

ALTER TABLE plan_versions
    ADD CONSTRAINT plan_versions_cycle_price_limit
    CHECK (cycle_price_cents <= 1000000000000);
ALTER TABLE user_subscriptions
    ADD CONSTRAINT user_subscriptions_cycle_price_snapshot_limit
    CHECK (cycle_price_cents_snapshot <= 1000000000000);
ALTER TABLE plans
    ADD COLUMN purchase_enabled boolean NOT NULL DEFAULT false;
UPDATE plans SET purchase_enabled=true WHERE code='free';

ALTER TABLE wallet_ledger_entries
    DROP CONSTRAINT wallet_ledger_entries_business_type_check;
ALTER TABLE wallet_ledger_entries
    ADD CONSTRAINT wallet_ledger_entries_business_type_check
    CHECK (business_type IN ('topup','usage','subscription','refund','grant','adjustment','reversal'));

ALTER TABLE user_notifications
    DROP CONSTRAINT user_notifications_kind_check;
ALTER TABLE user_notifications
    ADD CONSTRAINT user_notifications_kind_check CHECK (kind IN (
        'low_balance','billing_suspended','billing_recovered',
        'subscription_purchased','subscription_purchase_failed',
        'subscription_expiring','subscription_grace','subscription_expired'
    ));

CREATE TABLE subscription_purchases (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    plan_version_id uuid NOT NULL REFERENCES plan_versions(id),
    previous_plan_version_id uuid REFERENCES plan_versions(id),
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 128),
    action text NOT NULL CHECK (action IN ('purchase','renewal','upgrade','downgrade','change')),
    status text NOT NULL CHECK (status IN ('succeeded','insufficient_funds')),
    amount_cents bigint NOT NULL CHECK (amount_cents BETWEEN 0 AND 1000000000000),
    balance_after_cents bigint NOT NULL CHECK (balance_after_cents >= 0),
    service_period_start timestamptz NOT NULL,
    service_period_end timestamptz NOT NULL,
    subscription_ends_at timestamptz,
    wallet_ledger_entry_id bigint UNIQUE REFERENCES wallet_ledger_entries(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (service_period_end > service_period_start),
    CHECK ((amount_cents = 0 AND wallet_ledger_entry_id IS NULL) OR
           (amount_cents > 0 AND status = 'succeeded' AND wallet_ledger_entry_id IS NOT NULL) OR
           (amount_cents > 0 AND status = 'insufficient_funds' AND wallet_ledger_entry_id IS NULL)),
    UNIQUE (user_id,idempotency_key)
);
CREATE INDEX subscription_purchases_user_created_idx
    ON subscription_purchases(user_id,created_at DESC);

CREATE FUNCTION deny_subscription_purchase_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'subscription purchases are immutable';
END $$;
CREATE TRIGGER subscription_purchases_no_update_delete
BEFORE UPDATE OR DELETE ON subscription_purchases
FOR EACH ROW EXECUTE FUNCTION deny_subscription_purchase_mutation();

CREATE TABLE subscription_bill_items (
    id bigserial PRIMARY KEY,
    bill_id uuid NOT NULL REFERENCES bills(id) ON DELETE CASCADE,
    subscription_purchase_id uuid NOT NULL UNIQUE REFERENCES subscription_purchases(id),
    plan_version_id uuid NOT NULL REFERENCES plan_versions(id),
    plan_code text NOT NULL,
    plan_name text NOT NULL,
    action text NOT NULL CHECK (action IN ('purchase','renewal','upgrade','downgrade','change')),
    amount_cents bigint NOT NULL CHECK (amount_cents >= 0),
    service_period_start timestamptz NOT NULL,
    service_period_end timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (service_period_end > service_period_start)
);
CREATE INDEX subscription_bill_items_bill_created_idx
    ON subscription_bill_items(bill_id,created_at,id);
CREATE TRIGGER subscription_bill_items_no_update_delete
BEFORE UPDATE OR DELETE ON subscription_bill_items
FOR EACH ROW EXECUTE FUNCTION deny_bill_item_mutation();

COMMIT;
