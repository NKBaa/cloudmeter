BEGIN;
DROP TRIGGER IF EXISTS credit_consumptions_no_update_delete ON credit_consumptions;
DROP TRIGGER IF EXISTS credit_grants_restrict_update_delete ON credit_grants;
DROP FUNCTION IF EXISTS deny_credit_consumption_mutation;
DROP FUNCTION IF EXISTS restrict_credit_grant_mutation;
DROP INDEX usage_billing_attempts_snapshot_uidx;
ALTER TABLE usage_billing_attempts DROP COLUMN credit_balance_cents;
CREATE UNIQUE INDEX usage_billing_attempts_snapshot_uidx
    ON usage_billing_attempts(user_id,user_app_id,usage_code,window_start,window_end,pricing_version_id,status,balance_cents) NULLS NOT DISTINCT;
DROP TABLE IF EXISTS credit_consumptions;
DROP TABLE IF EXISTS credit_grants;
COMMIT;
