BEGIN;
DROP TRIGGER IF EXISTS payment_provider_operations_no_update_delete ON payment_provider_operations;
DROP FUNCTION IF EXISTS deny_payment_provider_operation_mutation;
DROP TABLE IF EXISTS payment_provider_operations;
ALTER TABLE payment_orders DROP COLUMN IF EXISTS last_queried_at;
ALTER TABLE payment_orders DROP COLUMN IF EXISTS closed_at;
COMMIT;
