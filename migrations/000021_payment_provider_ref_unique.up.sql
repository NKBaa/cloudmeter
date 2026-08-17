BEGIN;
CREATE UNIQUE INDEX payment_orders_epay_provider_ref_uidx
    ON payment_orders(provider_ref)
    WHERE provider = 'epay' AND provider_ref IS NOT NULL AND provider_ref <> '';
COMMIT;
