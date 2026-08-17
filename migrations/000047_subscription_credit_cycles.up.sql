BEGIN;

ALTER TABLE plan_versions DROP CONSTRAINT plan_versions_credit_grant_check;
ALTER TABLE plan_versions ADD CONSTRAINT plan_versions_credit_grant_check CHECK (
    jsonb_typeof(entitlements->'creditGrantCents') = 'number'
    AND (entitlements->>'creditGrantCents')::bigint BETWEEN 0 AND 1000000000000
);
ALTER TABLE user_subscriptions DROP CONSTRAINT user_subscriptions_credit_grant_check;
ALTER TABLE user_subscriptions ADD CONSTRAINT user_subscriptions_credit_grant_check CHECK (
    jsonb_typeof(entitlements_snapshot->'creditGrantCents') = 'number'
    AND (entitlements_snapshot->>'creditGrantCents')::bigint BETWEEN 0 AND 1000000000000
);

CREATE FUNCTION grant_subscription_credit(
    p_user_id uuid,
    p_target_cents bigint,
    p_created_by uuid DEFAULT NULL
) RETURNS TABLE(id uuid, amount_cents bigint, business_ref text)
LANGUAGE plpgsql AS $$
DECLARE
    v_period text;
    v_prefix text;
    v_granted bigint;
    v_delta bigint;
    v_id uuid;
    v_ref text;
BEGIN
    IF p_target_cents IS NULL OR p_target_cents <= 0 THEN
        RETURN;
    END IF;
    IF p_target_cents > 1000000000000 THEN
        RAISE EXCEPTION 'subscription credit target exceeds platform limit';
    END IF;

    v_period := to_char(now() AT TIME ZONE 'UTC','YYYY-MM');
    v_prefix := 'subscription-credit/' || p_user_id::text || '/' || v_period;
    PERFORM pg_advisory_xact_lock(hashtextextended(v_prefix,0));

    SELECT coalesce(sum(g.amount_cents),0)
    INTO v_granted
    FROM credit_grants g
    WHERE g.user_id=p_user_id
      AND (g.business_ref=v_prefix OR g.business_ref LIKE v_prefix || '/%');

    v_delta := greatest(p_target_cents-v_granted,0);
    IF v_delta=0 THEN
        RETURN;
    END IF;

    v_ref := v_prefix || '/to-' || p_target_cents::text;
    INSERT INTO credit_grants AS grant_row(
        user_id,amount_cents,remaining_cents,business_ref,note,expires_at,created_by
    ) VALUES (
        p_user_id,v_delta,v_delta,v_ref,'套餐月度赠送额度',
        (date_trunc('month',now() AT TIME ZONE 'UTC') + interval '1 month') AT TIME ZONE 'UTC',
        p_created_by
    )
    ON CONFLICT ON CONSTRAINT credit_grants_user_id_business_ref_key DO NOTHING
    RETURNING grant_row.id INTO v_id;

    IF v_id IS NULL THEN
        RETURN;
    END IF;
    id := v_id;
    amount_cents := v_delta;
    business_ref := v_ref;
    RETURN NEXT;
END $$;

COMMIT;
