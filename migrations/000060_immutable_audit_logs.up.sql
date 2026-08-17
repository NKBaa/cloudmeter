BEGIN;

CREATE FUNCTION deny_audit_log_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit logs are append-only';
END $$;

CREATE TRIGGER audit_logs_no_mutation
BEFORE UPDATE OR DELETE OR TRUNCATE ON audit_logs
FOR EACH STATEMENT EXECUTE FUNCTION deny_audit_log_mutation();

COMMIT;
