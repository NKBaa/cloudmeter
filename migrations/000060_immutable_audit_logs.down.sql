BEGIN;
DROP TRIGGER IF EXISTS audit_logs_no_mutation ON audit_logs;
DROP FUNCTION IF EXISTS deny_audit_log_mutation;
COMMIT;
