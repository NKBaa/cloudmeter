ALTER TABLE app_products DROP COLUMN IF EXISTS icon_url;
ALTER TABLE payment_orders DROP COLUMN IF EXISTS payment_type;
ALTER TABLE payment_provider_configs DROP COLUMN IF EXISTS amount_options, DROP COLUMN IF EXISTS payment_methods;
