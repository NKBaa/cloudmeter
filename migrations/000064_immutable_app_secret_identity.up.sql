BEGIN;

CREATE FUNCTION protect_app_secret_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'application Secret records cannot be deleted';
    END IF;
    IF NEW.id <> OLD.id
       OR NEW.user_app_id <> OLD.user_app_id
       OR NEW.key <> OLD.key
       OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'application Secret identity is immutable';
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER app_secrets_identity_immutable
BEFORE UPDATE OR DELETE ON app_secrets
FOR EACH ROW EXECUTE FUNCTION protect_app_secret_identity();

CREATE TRIGGER app_secrets_no_truncate
BEFORE TRUNCATE ON app_secrets
FOR EACH STATEMENT EXECUTE FUNCTION deny_immutable_history_truncate();

COMMIT;
