BEGIN;

CREATE OR REPLACE FUNCTION resolve_pricing_version(p_user_id uuid, p_app_id uuid, p_code text, p_unit text, p_at timestamptz)
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
            WHERE po.pricing_item_id=pi.id AND us.status IN ('active','grace_period')
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

UPDATE payment_provider_configs SET enabled=true WHERE provider='manual';

COMMIT;
