BEGIN;
INSERT INTO pricing_items(code,unit) VALUES
    ('app.deployment','deployment'),
    ('product.authorization','authorization')
ON CONFLICT (code) DO NOTHING;
COMMIT;
