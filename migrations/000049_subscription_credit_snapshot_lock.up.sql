BEGIN;

CREATE FUNCTION grant_subscription_credit(
    p_user_id uuid,
    p_created_by uuid DEFAULT NULL
) RETURNS TABLE(id uuid, amount_cents bigint, business_ref text)
LANGUAGE plpgsql AS $$
DECLARE
    v_period text;
    v_prefix text;
    v_target bigint;
    v_granted bigint;
    v_delta bigint;
    v_id uuid;
    v_ref text;
BEGIN
    PERFORM 1 FROM users WHERE users.id=p_user_id AND users.status='active' FOR UPDATE;
    IF NOT FOUND THEN RETURN; END IF;

    SELECT coalesce((us.entitlements_snapshot->>'creditGrantCents')::bigint,0)
    INTO v_target
    FROM user_subscriptions us
    WHERE us.user_id=p_user_id
      AND ((us.status='active' AND (us.ends_at IS NULL OR us.ends_at>now()))
           OR (us.status='grace_period' AND us.grace_ends_at IS NOT NULL AND us.grace_ends_at>now()))
    FOR UPDATE;
    IF NOT FOUND OR v_target<=0 THEN RETURN; END IF;
    IF v_target>1000000000000 THEN
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

    v_delta := greatest(v_target-v_granted,0);
    IF v_delta=0 THEN RETURN; END IF;

    v_ref := v_prefix || '/to-' || v_target::text;
    INSERT INTO credit_grants AS grant_row(
        user_id,amount_cents,remaining_cents,business_ref,note,expires_at,created_by
    ) VALUES (
        p_user_id,v_delta,v_delta,v_ref,'套餐月度赠送额度',
        (date_trunc('month',now() AT TIME ZONE 'UTC') + interval '1 month') AT TIME ZONE 'UTC',
        p_created_by
    )
    ON CONFLICT ON CONSTRAINT credit_grants_user_id_business_ref_key DO NOTHING
    RETURNING grant_row.id INTO v_id;

    IF v_id IS NULL THEN RETURN; END IF;
    id := v_id;
    amount_cents := v_delta;
    business_ref := v_ref;
    RETURN NEXT;
END $$;

CREATE OR REPLACE FUNCTION grant_subscription_credit(
    p_user_id uuid,
    p_target_cents bigint,
    p_created_by uuid DEFAULT NULL
) RETURNS TABLE(id uuid, amount_cents bigint, business_ref text)
LANGUAGE sql AS $$
    SELECT * FROM grant_subscription_credit(p_user_id,p_created_by)
$$;

COMMIT;
