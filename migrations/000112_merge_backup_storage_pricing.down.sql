BEGIN;

UPDATE pricing_items
SET active = true
WHERE code IN ('storage.system.gib_days', 'backup.storage.gib_days');

COMMIT;
