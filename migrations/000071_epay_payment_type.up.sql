BEGIN;
ALTER TABLE payment_provider_configs ADD COLUMN payment_type text NOT NULL DEFAULT 'alipay';
ALTER TABLE payment_provider_configs ADD CONSTRAINT payment_provider_payment_type_check CHECK (payment_type IN ('alipay','wxpay','qqpay','bank'));
COMMIT;
