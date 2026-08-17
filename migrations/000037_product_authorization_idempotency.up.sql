BEGIN;
CREATE UNIQUE INDEX usage_events_product_authorization_app_uidx
    ON usage_events(user_app_id)
    WHERE usage_code='product.authorization' AND user_app_id IS NOT NULL;
COMMIT;
