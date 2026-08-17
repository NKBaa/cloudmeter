BEGIN;

CREATE FUNCTION deny_refund_history_truncate() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'refund history cannot be truncated';
END $$;

CREATE TRIGGER refunds_no_truncate
BEFORE TRUNCATE ON refunds
FOR EACH STATEMENT EXECUTE FUNCTION deny_refund_history_truncate();

CREATE TRIGGER refund_events_no_truncate
BEFORE TRUNCATE ON refund_events
FOR EACH STATEMENT EXECUTE FUNCTION deny_refund_history_truncate();

COMMIT;
