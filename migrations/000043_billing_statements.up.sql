BEGIN;

CREATE TABLE bills (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    period_start timestamptz NOT NULL,
    period_end timestamptz NOT NULL,
    currency text NOT NULL DEFAULT 'CNY' CHECK (currency = 'CNY'),
    total_cents bigint NOT NULL DEFAULT 0 CHECK (total_cents >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (period_end > period_start),
    UNIQUE (user_id, period_start, period_end)
);
CREATE INDEX bills_user_period_idx ON bills(user_id, period_start DESC);

CREATE TABLE bill_items (
    id bigserial PRIMARY KEY,
    bill_id uuid NOT NULL REFERENCES bills(id) ON DELETE CASCADE,
    usage_charge_id bigint NOT NULL UNIQUE REFERENCES usage_charges(id),
    user_app_id uuid REFERENCES user_apps(id) ON DELETE SET NULL,
    app_slug text,
    usage_code text NOT NULL,
    unit text NOT NULL,
    quantity numeric(30,12) NOT NULL CHECK (quantity >= 0),
    pricing_version_id uuid NOT NULL REFERENCES pricing_versions(id),
    unit_price_micros bigint NOT NULL CHECK (unit_price_micros >= 0),
    amount_cents bigint NOT NULL CHECK (amount_cents >= 0),
    window_start timestamptz NOT NULL,
    window_end timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (window_end > window_start)
);
CREATE INDEX bill_items_bill_window_idx ON bill_items(bill_id, window_start, id);

INSERT INTO bills(user_id,period_start,period_end,total_cents,created_at,updated_at)
SELECT user_id,
       date_trunc('month',window_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC',
       (date_trunc('month',window_start AT TIME ZONE 'UTC') + interval '1 month') AT TIME ZONE 'UTC',
       sum(amount_cents),min(created_at),max(created_at)
FROM usage_charges
GROUP BY user_id,date_trunc('month',window_start AT TIME ZONE 'UTC');

INSERT INTO bill_items(bill_id,usage_charge_id,user_app_id,app_slug,usage_code,unit,quantity,pricing_version_id,unit_price_micros,amount_cents,window_start,window_end,created_at)
SELECT b.id,c.id,c.user_app_id,a.slug,c.usage_code,pi.unit,c.quantity,c.pricing_version_id,pv.unit_price_micros,c.amount_cents,c.window_start,c.window_end,c.created_at
FROM usage_charges c
JOIN pricing_versions pv ON pv.id=c.pricing_version_id
JOIN pricing_items pi ON pi.id=pv.pricing_item_id
JOIN bills b ON b.user_id=c.user_id
 AND b.period_start=date_trunc('month',c.window_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'
LEFT JOIN user_apps a ON a.id=c.user_app_id;

CREATE FUNCTION deny_bill_item_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'bill items are immutable';
END $$;
CREATE TRIGGER bill_items_no_update_delete BEFORE UPDATE OR DELETE ON bill_items
FOR EACH ROW EXECUTE FUNCTION deny_bill_item_mutation();

CREATE FUNCTION restrict_bill_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'bills cannot be deleted';
    END IF;
    IF NEW.user_id <> OLD.user_id OR NEW.period_start <> OLD.period_start
       OR NEW.period_end <> OLD.period_end OR NEW.currency <> OLD.currency
       OR NEW.created_at <> OLD.created_at OR NEW.total_cents < OLD.total_cents THEN
        RAISE EXCEPTION 'bill identity is immutable and total cannot decrease';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER bills_restrict_update_delete BEFORE UPDATE OR DELETE ON bills
FOR EACH ROW EXECUTE FUNCTION restrict_bill_mutation();

COMMIT;
