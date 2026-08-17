BEGIN;
ALTER TABLE app_egress_samples DROP CONSTRAINT IF EXISTS app_egress_samples_user_app_id_cumulative_bytes_key;
COMMIT;
