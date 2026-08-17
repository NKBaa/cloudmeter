BEGIN;
CREATE OR REPLACE FUNCTION deny_release_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'application releases cannot be deleted';
    END IF;
    IF NEW.user_app_id <> OLD.user_app_id
       OR NEW.product_version_id <> OLD.product_version_id
       OR NEW.release_number <> OLD.release_number
       OR NEW.immutable_snapshot <> OLD.immutable_snapshot
       OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'application release snapshot is immutable';
    END IF;
    RETURN NEW;
END $$;
COMMIT;
