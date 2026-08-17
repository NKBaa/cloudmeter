BEGIN;

UPDATE system_state
SET registration_enabled = false, email_verification_required = false
WHERE singleton
  AND email_verification_required
  AND NOT EXISTS (
      SELECT 1
      FROM smtp_settings
      WHERE singleton
        AND enabled
        AND btrim(host) <> ''
        AND port BETWEEN 1 AND 65535
        AND from_email ~ '^[^[:space:]@]+@[^[:space:]@]+$'
        AND tls_mode IN ('none', 'starttls', 'tls')
  );

CREATE FUNCTION enforce_email_verification_smtp_invariant() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    verification_required boolean;
    smtp_ready boolean;
BEGIN
    IF TG_TABLE_NAME = 'system_state' THEN
        IF NEW.email_verification_required THEN
            SELECT enabled
                   AND btrim(host) <> ''
                   AND port BETWEEN 1 AND 65535
                   AND from_email ~ '^[^[:space:]@]+@[^[:space:]@]+$'
                   AND tls_mode IN ('none', 'starttls', 'tls')
            INTO smtp_ready
            FROM smtp_settings
            WHERE singleton
            FOR UPDATE;
            IF NOT coalesce(smtp_ready, false) THEN
                RAISE EXCEPTION 'email verification requires an enabled and valid SMTP configuration'
                    USING ERRCODE = 'check_violation';
            END IF;
        END IF;
    ELSE
        SELECT email_verification_required
        INTO verification_required
        FROM system_state
        WHERE singleton
        FOR UPDATE;
        smtp_ready := NEW.enabled
            AND btrim(NEW.host) <> ''
            AND NEW.port BETWEEN 1 AND 65535
            AND NEW.from_email ~ '^[^[:space:]@]+@[^[:space:]@]+$'
            AND NEW.tls_mode IN ('none', 'starttls', 'tls');
        IF coalesce(verification_required, false) AND NOT smtp_ready THEN
            RAISE EXCEPTION 'SMTP cannot be disabled or invalidated while email verification is required'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION lock_email_verification_settings() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(729104503);
    RETURN NULL;
END;
$$;

CREATE TRIGGER system_state_email_verification_settings_lock
BEFORE INSERT OR UPDATE ON system_state
FOR EACH STATEMENT EXECUTE FUNCTION lock_email_verification_settings();

CREATE TRIGGER smtp_settings_email_verification_settings_lock
BEFORE INSERT OR UPDATE ON smtp_settings
FOR EACH STATEMENT EXECUTE FUNCTION lock_email_verification_settings();

CREATE TRIGGER system_state_email_verification_smtp_guard
BEFORE INSERT OR UPDATE OF email_verification_required ON system_state
FOR EACH ROW EXECUTE FUNCTION enforce_email_verification_smtp_invariant();

CREATE TRIGGER smtp_settings_email_verification_guard
BEFORE INSERT OR UPDATE OF enabled, host, port, from_email, tls_mode ON smtp_settings
FOR EACH ROW EXECUTE FUNCTION enforce_email_verification_smtp_invariant();

COMMIT;
