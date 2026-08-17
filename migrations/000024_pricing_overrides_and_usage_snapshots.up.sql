BEGIN;

CREATE TABLE pricing_overrides (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    pricing_item_id uuid NOT NULL REFERENCES pricing_items(id) ON DELETE CASCADE,
    pricing_version_id uuid NOT NULL REFERENCES pricing_versions(id),
    user_id uuid REFERENCES users(id) ON DELETE CASCADE,
    product_id uuid REFERENCES app_products(id) ON DELETE CASCADE,
    plan_id uuid REFERENCES plans(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (num_nonnulls(user_id, product_id, plan_id) = 1)
);
CREATE UNIQUE INDEX pricing_overrides_user_uidx ON pricing_overrides(user_id,pricing_item_id) WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX pricing_overrides_product_uidx ON pricing_overrides(product_id,pricing_item_id) WHERE product_id IS NOT NULL;
CREATE UNIQUE INDEX pricing_overrides_plan_uidx ON pricing_overrides(plan_id,pricing_item_id) WHERE plan_id IS NOT NULL;

CREATE FUNCTION resolve_pricing_version(p_user_id uuid, p_app_id uuid, p_code text, p_unit text, p_at timestamptz)
RETURNS uuid LANGUAGE sql STABLE AS $$
    SELECT candidate.id
    FROM pricing_items pi
    JOIN LATERAL (
        SELECT pv.id, choice.priority
        FROM (
            SELECT po.pricing_version_id, 30 AS priority
            FROM pricing_overrides po
            WHERE po.pricing_item_id=pi.id AND po.user_id=p_user_id
            UNION ALL
            SELECT po.pricing_version_id, 20
            FROM pricing_overrides po JOIN user_apps a ON a.product_id=po.product_id
            WHERE po.pricing_item_id=pi.id AND a.id=p_app_id
            UNION ALL
            SELECT po.pricing_version_id, 10
            FROM pricing_overrides po
            JOIN user_subscriptions us ON us.user_id=p_user_id
            JOIN plan_versions planv ON planv.id=us.plan_version_id AND planv.plan_id=po.plan_id
            WHERE po.pricing_item_id=pi.id
              AND us.status IN ('active','grace_period')
            UNION ALL
            SELECT pv0.id, 0
            FROM pricing_versions pv0
            WHERE pv0.pricing_item_id=pi.id
        ) choice
        JOIN pricing_versions pv ON pv.id=choice.pricing_version_id AND pv.pricing_item_id=pi.id
        WHERE pv.effective_at<=p_at
        ORDER BY choice.priority DESC,pv.effective_at DESC,pv.version DESC
        LIMIT 1
    ) candidate ON true
    WHERE pi.code=p_code AND pi.unit=p_unit
$$;

ALTER TABLE usage_aggregates DROP CONSTRAINT usage_aggregates_pkey;
ALTER TABLE usage_aggregates
    ADD COLUMN id bigserial,
    ADD COLUMN user_app_id uuid REFERENCES user_apps(id) ON DELETE SET NULL,
    ADD COLUMN price_version_id uuid REFERENCES pricing_versions(id),
    ADD PRIMARY KEY (id);
CREATE UNIQUE INDEX usage_aggregates_snapshot_uidx
    ON usage_aggregates(user_id,user_app_id,usage_code,window_start,window_end,price_version_id) NULLS NOT DISTINCT;

ALTER TABLE usage_charges DROP CONSTRAINT usage_charges_user_id_usage_code_window_start_window_end_key;
ALTER TABLE usage_charges ADD COLUMN user_app_id uuid REFERENCES user_apps(id) ON DELETE SET NULL;
CREATE UNIQUE INDEX usage_charges_snapshot_uidx
    ON usage_charges(user_id,user_app_id,usage_code,window_start,window_end,pricing_version_id) NULLS NOT DISTINCT;

ALTER TABLE usage_billing_attempts DROP CONSTRAINT usage_billing_attempts_user_id_usage_code_window_start_wind_key;
ALTER TABLE usage_billing_attempts ADD COLUMN user_app_id uuid REFERENCES user_apps(id) ON DELETE SET NULL;
CREATE UNIQUE INDEX usage_billing_attempts_snapshot_uidx
    ON usage_billing_attempts(user_id,user_app_id,usage_code,window_start,window_end,pricing_version_id,status,balance_cents) NULLS NOT DISTINCT;

COMMIT;
