BEGIN;
DELETE FROM pricing_items WHERE code IN ('backup.operation','backup.storage.gib_days')
  AND NOT EXISTS (SELECT 1 FROM pricing_versions pv WHERE pv.pricing_item_id=pricing_items.id);
COMMIT;
