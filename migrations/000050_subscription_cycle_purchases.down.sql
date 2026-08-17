BEGIN;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM subscription_purchases) THEN
        RAISE EXCEPTION 'cannot roll back subscription purchase migration while financial history exists';
    END IF;
    IF EXISTS (SELECT 1 FROM user_notifications WHERE kind LIKE 'subscription_%') THEN
        RAISE EXCEPTION 'cannot roll back subscription purchase migration while lifecycle notifications exist';
    END IF;
END $$;

DROP TRIGGER IF EXISTS subscription_bill_items_no_update_delete ON subscription_bill_items;
DROP TABLE IF EXISTS subscription_bill_items;
DROP TRIGGER IF EXISTS subscription_purchases_no_update_delete ON subscription_purchases;
DROP FUNCTION IF EXISTS deny_subscription_purchase_mutation;
DROP TABLE IF EXISTS subscription_purchases;
ALTER TABLE plans DROP COLUMN IF EXISTS purchase_enabled;
ALTER TABLE user_subscriptions
    DROP CONSTRAINT IF EXISTS user_subscriptions_cycle_price_snapshot_limit;
ALTER TABLE plan_versions
    DROP CONSTRAINT IF EXISTS plan_versions_cycle_price_limit;

ALTER TABLE user_notifications
    DROP CONSTRAINT user_notifications_kind_check;
ALTER TABLE user_notifications
    ADD CONSTRAINT user_notifications_kind_check
    CHECK (kind IN ('low_balance','billing_suspended','billing_recovered'));

ALTER TABLE wallet_ledger_entries
    DROP CONSTRAINT wallet_ledger_entries_business_type_check;
ALTER TABLE wallet_ledger_entries
    ADD CONSTRAINT wallet_ledger_entries_business_type_check
    CHECK (business_type IN ('topup','usage','refund','grant','adjustment','reversal'));

COMMIT;
