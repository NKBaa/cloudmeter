BEGIN;
ALTER TABLE payment_provider_configs DROP CONSTRAINT IF EXISTS payment_provider_payment_type_check;
ALTER TABLE payment_provider_configs DROP COLUMN IF EXISTS payment_type;
COMMIT;
