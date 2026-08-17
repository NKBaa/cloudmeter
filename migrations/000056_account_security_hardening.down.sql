BEGIN;

DROP TRIGGER IF EXISTS smtp_settings_email_verification_guard ON smtp_settings;
CREATE OR REPLACE FUNCTION enforce_email_verification_smtp_invariant() RETURNS trigger
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
CREATE TRIGGER smtp_settings_email_verification_guard
BEFORE INSERT OR UPDATE OF enabled, host, port, from_email, tls_mode ON smtp_settings
FOR EACH ROW EXECUTE FUNCTION enforce_email_verification_smtp_invariant();

DROP INDEX IF EXISTS email_verification_codes_ip_rate_idx;
ALTER TABLE email_verification_codes
    DROP COLUMN IF EXISTS attempt_count,
    DROP COLUMN IF EXISTS request_ip;
ALTER TABLE oauth_settings DROP COLUMN IF EXISTS minimum_trust_level;

COMMIT;
