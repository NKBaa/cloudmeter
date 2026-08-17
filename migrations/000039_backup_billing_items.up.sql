BEGIN;
INSERT INTO pricing_items(code,unit) VALUES
    ('backup.operation','operation'),
    ('backup.storage.gib_days','GiB_day')
ON CONFLICT (code) DO NOTHING;
COMMIT;
