ALTER TABLE payment_provider_configs
    ADD COLUMN payment_methods jsonb NOT NULL DEFAULT '[{"name":"支付宝","type":"alipay","minAmountCents":100,"enabled":true}]'::jsonb,
    ADD COLUMN amount_options jsonb NOT NULL DEFAULT '[10,20,50,100,200,300,400,500]'::jsonb;

ALTER TABLE payment_orders ADD COLUMN payment_type text NOT NULL DEFAULT '';
ALTER TABLE app_products ADD COLUMN icon_url text NOT NULL DEFAULT '';
