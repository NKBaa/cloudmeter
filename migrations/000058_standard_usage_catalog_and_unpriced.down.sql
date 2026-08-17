BEGIN;

DROP INDEX IF EXISTS usage_aggregates_pending_billing_idx;
ALTER TABLE usage_aggregates DROP CONSTRAINT IF EXISTS usage_aggregates_pending_price_check;

-- Preserve the no-charge decision when rolling back to the older vocabulary.
UPDATE usage_aggregates
SET billing_disposition='waived_legacy'
WHERE billing_disposition='unpriced';

ALTER TABLE usage_aggregates
    DROP CONSTRAINT usage_aggregates_billing_disposition_check;
ALTER TABLE usage_aggregates
    ADD CONSTRAINT usage_aggregates_billing_disposition_check
    CHECK (billing_disposition IN ('pending','charged','waived_legacy'));

DELETE FROM pricing_items
WHERE code IN (
    'app.runtime.minutes',
    'cpu.core_hours',
    'memory.gib_hours',
    'storage.system.gib_days',
    'storage.data.gib_days',
    'network.egress_gib'
)
  AND NOT EXISTS (SELECT 1 FROM pricing_versions v WHERE v.pricing_item_id=pricing_items.id)
  AND NOT EXISTS (SELECT 1 FROM pricing_overrides o WHERE o.pricing_item_id=pricing_items.id);

COMMIT;
