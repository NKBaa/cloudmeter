BEGIN;

ALTER TABLE usage_aggregates
    ADD COLUMN billing_disposition text NOT NULL DEFAULT 'pending'
    CHECK (billing_disposition IN ('pending','charged','waived_legacy'));

UPDATE usage_aggregates a
SET billing_disposition = 'charged',
    sealed_at = coalesce(a.sealed_at, c.created_at),
    price_version_id = coalesce(a.price_version_id, c.pricing_version_id)
FROM usage_charges c
WHERE c.user_id = a.user_id
  AND c.user_app_id IS NOT DISTINCT FROM a.user_app_id
  AND c.usage_code = a.usage_code
  AND c.window_start = a.window_start
  AND c.window_end = a.window_end
  AND (a.user_app_id IS NULL OR c.pricing_version_id = a.price_version_id);

-- Legacy account-scoped aggregates predate application attribution and did
-- not freeze a price. Charging them at today's price would be retroactive.
WITH waived AS (
    UPDATE usage_aggregates a
    SET billing_disposition = 'waived_legacy',
        sealed_at = coalesce(a.sealed_at, now())
    WHERE a.user_app_id IS NULL
      AND a.billing_disposition = 'pending'
      AND NOT EXISTS (
          SELECT 1
          FROM usage_charges c
          WHERE c.user_id = a.user_id
            AND c.user_app_id IS NULL
            AND c.usage_code = a.usage_code
            AND c.window_start = a.window_start
            AND c.window_end = a.window_end
      )
    RETURNING a.user_id, a.quantity
), summary AS (
    SELECT user_id, count(*) AS aggregate_count, sum(quantity) AS quantity
    FROM waived
    GROUP BY user_id
)
INSERT INTO audit_logs(subject_user_id,action,resource_type,resource_id,request_id,metadata)
SELECT user_id,
       'usage.legacy_unpriced.waive',
       'usage_aggregate',
       user_id::text,
       'migration:000028',
       jsonb_build_object('aggregate_count',aggregate_count,'quantity',quantity)
FROM summary;

COMMIT;
