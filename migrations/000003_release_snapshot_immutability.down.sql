BEGIN;
CREATE OR REPLACE FUNCTION deny_release_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'application releases are immutable';
END $$;
COMMIT;
