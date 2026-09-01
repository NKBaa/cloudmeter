BEGIN;

CREATE OR REPLACE FUNCTION resolve_pricing_version(p_user_id uuid, p_app_id uuid, p_code text, p_unit text, p_at timestamptz)
RETURNS uuid LANGUAGE sql STABLE AS $$
    SELECT pv.id
    FROM pricing_items pi
    JOIN pricing_versions pv ON pv.pricing_item_id=pi.id
    WHERE pi.code=p_code AND pi.unit=p_unit AND pv.effective_at<=p_at
    ORDER BY pv.effective_at DESC,pv.version DESC
    LIMIT 1
$$;

DROP TABLE pricing_overrides;

COMMIT;
