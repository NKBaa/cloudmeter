BEGIN;

-- Running data and retained backup archives share the application's selected
-- data-volume capacity. Keep historical price versions for old statements,
-- but retire their catalog entries so only storage.data.gib_days is priced.
UPDATE pricing_items
SET active = false
WHERE code IN ('storage.system.gib_days', 'backup.storage.gib_days')
  AND active;

COMMIT;
