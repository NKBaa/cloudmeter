BEGIN;

-- These are the usage codes emitted by the platform itself. Pre-creating the
-- catalog lets a fresh installation publish prices without guessing codes or
-- units, while still leaving every item unpriced by default.
INSERT INTO pricing_items(code,unit) VALUES
    ('app.runtime.minutes','minute'),
    ('cpu.core_hours','core_hour'),
    ('memory.gib_hours','GiB_hour'),
    ('storage.system.gib_days','GiB_day'),
    ('storage.data.gib_days','GiB_day'),
    ('network.egress_gib','GiB'),
    ('app.deployment','deployment'),
    ('product.authorization','authorization'),
    ('network.public_ingress','ingress'),
    ('backup.operation','operation'),
    ('backup.storage.gib_days','GiB_day')
ON CONFLICT (code) DO NOTHING;

WITH expected(code,unit) AS (VALUES
    ('app.runtime.minutes','minute'),
    ('cpu.core_hours','core_hour'),
    ('memory.gib_hours','GiB_hour'),
    ('storage.system.gib_days','GiB_day'),
    ('storage.data.gib_days','GiB_day'),
    ('network.egress_gib','GiB'),
    ('app.deployment','deployment'),
    ('product.authorization','authorization'),
    ('network.public_ingress','ingress'),
    ('backup.operation','operation'),
    ('backup.storage.gib_days','GiB_day')
)
UPDATE pricing_items item
SET unit=expected.unit
FROM expected
WHERE item.code=expected.code
  AND item.unit<>expected.unit
  AND lower(item.unit)=lower(expected.unit);

DO $$
DECLARE
    mismatch text;
BEGIN
    WITH expected(code,unit) AS (VALUES
        ('app.runtime.minutes','minute'),
        ('cpu.core_hours','core_hour'),
        ('memory.gib_hours','GiB_hour'),
        ('storage.system.gib_days','GiB_day'),
        ('storage.data.gib_days','GiB_day'),
        ('network.egress_gib','GiB'),
        ('app.deployment','deployment'),
        ('product.authorization','authorization'),
        ('network.public_ingress','ingress'),
        ('backup.operation','operation'),
        ('backup.storage.gib_days','GiB_day')
    )
    SELECT string_agg(format('%s expects %s but has %s',expected.code,expected.unit,item.unit),'; ' ORDER BY expected.code)
    INTO mismatch
    FROM expected
    JOIN pricing_items item ON item.code=expected.code
    WHERE item.unit<>expected.unit;

    IF mismatch IS NOT NULL THEN
        RAISE EXCEPTION 'standard pricing item unit mismatch: %', mismatch;
    END IF;
END $$;

ALTER TABLE usage_aggregates
    DROP CONSTRAINT usage_aggregates_billing_disposition_check;
ALTER TABLE usage_aggregates
    ADD CONSTRAINT usage_aggregates_billing_disposition_check
    CHECK (billing_disposition IN ('pending','charged','unpriced','waived_legacy'));

-- A NULL snapshot means no price existed when the usage event was recorded.
-- Seal it permanently without charging so a later backdated price cannot turn
-- historical free usage into an unexpected debit.
WITH sealed AS (
    UPDATE usage_aggregates
    SET sealed_at=coalesce(sealed_at,now()),
        billing_disposition='unpriced'
    WHERE sealed_at IS NULL
      AND billing_disposition='pending'
      AND price_version_id IS NULL
    RETURNING user_id,quantity
), summary AS (
    SELECT user_id,count(*) AS aggregate_count,sum(quantity) AS quantity
    FROM sealed
    GROUP BY user_id
)
INSERT INTO audit_logs(subject_user_id,action,resource_type,resource_id,request_id,metadata)
SELECT user_id,'usage.unpriced.seal','usage_aggregate',user_id::text,'migration:000058',
       jsonb_build_object('aggregate_count',aggregate_count,'quantity',quantity,'reason','price_snapshot_missing')
FROM summary;

ALTER TABLE usage_aggregates
    ADD CONSTRAINT usage_aggregates_pending_price_check
    CHECK (billing_disposition<>'pending' OR price_version_id IS NOT NULL);

CREATE INDEX usage_aggregates_pending_billing_idx
    ON usage_aggregates(window_start,id)
    WHERE sealed_at IS NULL AND billing_disposition='pending';

COMMIT;
