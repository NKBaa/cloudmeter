BEGIN;
DROP TRIGGER IF EXISTS pricing_versions_no_update_delete ON pricing_versions;
DROP FUNCTION IF EXISTS deny_pricing_version_mutation();
ALTER TABLE user_apps DROP CONSTRAINT IF EXISTS user_apps_suspension_reason_check;
ALTER TABLE user_apps DROP COLUMN IF EXISTS suspension_reason;
DROP TABLE IF EXISTS usage_billing_attempts;
DROP TABLE IF EXISTS usage_charges;
COMMIT;
