BEGIN;
DROP TRIGGER IF EXISTS refund_events_no_truncate ON refund_events;
DROP TRIGGER IF EXISTS refunds_no_truncate ON refunds;
DROP FUNCTION IF EXISTS deny_refund_history_truncate;
COMMIT;
