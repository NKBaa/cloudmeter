BEGIN;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM payment_orders WHERE status='refunding') THEN
        RAISE EXCEPTION 'migration 59 cannot import an in-progress legacy refund';
    END IF;
    IF EXISTS (
        SELECT 1
        FROM payment_orders o
        WHERE o.status='refunded'
          AND NOT EXISTS (
              SELECT 1
              FROM wallet_ledger_entries le
              JOIN wallets w ON w.id=le.wallet_id
              WHERE w.user_id=o.user_id
                AND le.business_type='refund'
                AND le.business_ref=o.id::text
                AND le.amount_cents=-o.amount_cents
          )
    ) THEN
        RAISE EXCEPTION 'migration 59 cannot import a refunded order without its matching wallet ledger entry';
    END IF;
END $$;

CREATE TABLE refunds (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id uuid NOT NULL UNIQUE REFERENCES payment_orders(id),
    user_id uuid NOT NULL REFERENCES users(id),
    provider text NOT NULL REFERENCES payment_provider_configs(provider),
    amount_cents bigint NOT NULL CHECK (amount_cents > 0),
    status text NOT NULL CHECK (status IN ('processing','succeeded','failed')),
    reason text NOT NULL DEFAULT 'full payment refund' CHECK (length(reason) BETWEEN 1 AND 500),
    ledger_entry_id bigint UNIQUE REFERENCES wallet_ledger_entries(id),
    requested_by uuid REFERENCES users(id),
    request_id text NOT NULL CHECK (btrim(request_id) <> ''),
    failure_message text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    CHECK (
        (status='processing' AND completed_at IS NULL AND ledger_entry_id IS NULL AND failure_message='')
        OR (status='succeeded' AND completed_at IS NOT NULL AND ledger_entry_id IS NOT NULL AND failure_message='')
        OR (status='failed' AND completed_at IS NOT NULL AND ledger_entry_id IS NULL AND failure_message<>'')
    ),
    CHECK (completed_at IS NULL OR completed_at >= created_at)
);
CREATE INDEX refunds_user_created_idx ON refunds(user_id,created_at DESC);
CREATE INDEX refunds_status_created_idx ON refunds(status,created_at DESC);

CREATE TABLE refund_events (
    id bigserial PRIMARY KEY,
    refund_id uuid NOT NULL REFERENCES refunds(id),
    from_status text,
    to_status text NOT NULL,
    message text NOT NULL DEFAULT '',
    metadata jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(metadata)='object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (
        (from_status IS NULL AND to_status='processing')
        OR (from_status='processing' AND to_status IN ('succeeded','failed'))
    ),
    UNIQUE (refund_id,to_status)
);
CREATE INDEX refund_events_refund_created_idx ON refund_events(refund_id,created_at,id);

CREATE FUNCTION protect_refund_record() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'refund records cannot be deleted';
    END IF;

    IF TG_OP = 'INSERT' THEN
        IF NOT EXISTS (
            SELECT 1
            FROM payment_orders o
            WHERE o.id=NEW.order_id
              AND o.user_id=NEW.user_id
              AND o.provider=NEW.provider
              AND o.amount_cents=NEW.amount_cents
              AND o.status IN ('refunding','refunded')
        ) THEN
            RAISE EXCEPTION 'refund snapshot does not match a refundable payment order';
        END IF;
    ELSE
        IF NEW.order_id IS DISTINCT FROM OLD.order_id
           OR NEW.user_id IS DISTINCT FROM OLD.user_id
           OR NEW.provider IS DISTINCT FROM OLD.provider
           OR NEW.amount_cents IS DISTINCT FROM OLD.amount_cents
           OR NEW.reason IS DISTINCT FROM OLD.reason
           OR NEW.requested_by IS DISTINCT FROM OLD.requested_by
           OR NEW.request_id IS DISTINCT FROM OLD.request_id
           OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
            RAISE EXCEPTION 'refund identity is immutable';
        END IF;
        IF OLD.status <> 'processing'
           OR NEW.status NOT IN ('succeeded','failed') THEN
            RAISE EXCEPTION 'refund terminal state is immutable';
        END IF;
    END IF;

    IF NEW.status='succeeded' AND NOT EXISTS (
        SELECT 1
        FROM wallet_ledger_entries le
        JOIN wallets w ON w.id=le.wallet_id
        WHERE le.id=NEW.ledger_entry_id
          AND w.user_id=NEW.user_id
          AND le.business_type='refund'
          AND le.business_ref=NEW.order_id::text
          AND le.amount_cents=-NEW.amount_cents
    ) THEN
        RAISE EXCEPTION 'refund ledger entry does not match the refund snapshot';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER refunds_protect_update_delete
BEFORE INSERT OR UPDATE OR DELETE ON refunds
FOR EACH ROW EXECUTE FUNCTION protect_refund_record();

CREATE FUNCTION deny_refund_event_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'refund events are immutable';
END $$;
CREATE TRIGGER refund_events_no_update_delete
BEFORE UPDATE OR DELETE ON refund_events
FOR EACH ROW EXECUTE FUNCTION deny_refund_event_mutation();

CREATE FUNCTION enforce_refund_event_alignment() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    checked_refund_id uuid;
    refund_status text;
    processing_events bigint;
    succeeded_events bigint;
    failed_events bigint;
    processing_at timestamptz;
    terminal_at timestamptz;
BEGIN
    IF TG_TABLE_NAME='refund_events' THEN
        checked_refund_id := NEW.refund_id;
    ELSE
        checked_refund_id := NEW.id;
    END IF;

    SELECT rf.status,
           count(e.id) FILTER (WHERE e.to_status='processing'),
           count(e.id) FILTER (WHERE e.to_status='succeeded'),
           count(e.id) FILTER (WHERE e.to_status='failed'),
           min(e.created_at) FILTER (WHERE e.to_status='processing'),
           min(e.created_at) FILTER (WHERE e.to_status IN ('succeeded','failed'))
    INTO refund_status,processing_events,succeeded_events,failed_events,processing_at,terminal_at
    FROM refunds rf
    LEFT JOIN refund_events e ON e.refund_id=rf.id
    WHERE rf.id=checked_refund_id
    GROUP BY rf.status;

    IF refund_status IS NULL THEN
        RETURN NULL;
    END IF;
    IF processing_events<>1
       OR (refund_status='processing' AND (succeeded_events<>0 OR failed_events<>0))
       OR (refund_status='succeeded' AND (succeeded_events<>1 OR failed_events<>0))
       OR (refund_status='failed' AND (succeeded_events<>0 OR failed_events<>1)) THEN
        RAISE EXCEPTION 'refund status and event timeline are inconsistent';
    END IF;
    IF terminal_at IS NOT NULL AND terminal_at < processing_at THEN
        RAISE EXCEPTION 'refund terminal event precedes its processing event';
    END IF;
    RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER refunds_event_alignment
AFTER INSERT OR UPDATE ON refunds DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION enforce_refund_event_alignment();
CREATE CONSTRAINT TRIGGER refund_events_refund_alignment
AFTER INSERT ON refund_events DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION enforce_refund_event_alignment();

INSERT INTO refunds(
    order_id,user_id,provider,amount_cents,status,reason,ledger_entry_id,
    requested_by,request_id,created_at,completed_at
)
SELECT o.id,o.user_id,o.provider,o.amount_cents,'succeeded','historical full refund',ledger.id,
       audit.actor_user_id,coalesce(nullif(audit.request_id,''),'migration-59'),
       coalesce(audit.created_at,ledger.created_at,o.paid_at,o.created_at),
       coalesce(audit.created_at,ledger.created_at,o.paid_at,o.created_at)
FROM payment_orders o
LEFT JOIN LATERAL (
    SELECT le.id,le.created_at
    FROM wallet_ledger_entries le
    JOIN wallets w ON w.id=le.wallet_id
    WHERE w.user_id=o.user_id
      AND le.business_type='refund'
      AND le.business_ref=o.id::text
    ORDER BY le.id DESC
    LIMIT 1
) ledger ON true
LEFT JOIN LATERAL (
    SELECT a.actor_user_id,a.request_id,a.created_at
    FROM audit_logs a
    WHERE a.action='payment.refund'
      AND a.resource_type='payment_order'
      AND a.resource_id=o.id::text
    ORDER BY a.id DESC
    LIMIT 1
) audit ON true
WHERE o.status='refunded';

INSERT INTO refund_events(refund_id,from_status,to_status,message,created_at)
SELECT id,NULL,'processing','historical refund imported',created_at FROM refunds;
INSERT INTO refund_events(refund_id,from_status,to_status,message,metadata,created_at)
SELECT id,'processing','succeeded','historical refund completed',
       jsonb_build_object('ledger_entry_id',ledger_entry_id),completed_at FROM refunds;

CREATE FUNCTION enforce_refund_order_alignment() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    checked_order_id uuid;
    order_status text;
    refund_status text;
    order_user_id uuid;
    refund_user_id uuid;
    order_provider text;
    refund_provider text;
    order_amount_cents bigint;
    refund_amount_cents bigint;
BEGIN
    IF TG_TABLE_NAME='refunds' THEN
        checked_order_id := NEW.order_id;
    ELSE
        checked_order_id := NEW.id;
    END IF;

    SELECT o.status,o.user_id,o.provider,o.amount_cents,
           rf.status,rf.user_id,rf.provider,rf.amount_cents
    INTO order_status,order_user_id,order_provider,order_amount_cents,
         refund_status,refund_user_id,refund_provider,refund_amount_cents
    FROM payment_orders o LEFT JOIN refunds rf ON rf.order_id=o.id
    WHERE o.id=checked_order_id;

    IF refund_status IS NULL THEN
        IF order_status IN ('refunding','refunded') THEN
            RAISE EXCEPTION 'payment refund state requires a refund record';
        END IF;
        RETURN NULL;
    END IF;
    IF (refund_status='processing' AND order_status<>'refunding')
       OR (refund_status='succeeded' AND order_status<>'refunded')
       OR (refund_status='failed' AND order_status<>'paid') THEN
        RAISE EXCEPTION 'payment order and refund states are inconsistent';
    END IF;
    IF order_user_id IS DISTINCT FROM refund_user_id
       OR order_provider IS DISTINCT FROM refund_provider
       OR order_amount_cents IS DISTINCT FROM refund_amount_cents THEN
        RAISE EXCEPTION 'payment order identity no longer matches the refund snapshot';
    END IF;
    RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER refunds_order_alignment
AFTER INSERT OR UPDATE ON refunds DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION enforce_refund_order_alignment();
CREATE CONSTRAINT TRIGGER payment_orders_refund_alignment
AFTER INSERT OR UPDATE ON payment_orders DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION enforce_refund_order_alignment();

COMMIT;
