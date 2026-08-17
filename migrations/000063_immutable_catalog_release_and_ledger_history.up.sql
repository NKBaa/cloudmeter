BEGIN;

CREATE FUNCTION deny_immutable_history_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'immutable history cannot be deleted: %', TG_TABLE_NAME;
END $$;

CREATE FUNCTION deny_immutable_history_truncate() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'immutable history cannot be truncated: %', TG_TABLE_NAME;
END $$;

CREATE FUNCTION deny_app_secret_version_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'application secret versions are immutable';
END $$;

CREATE TRIGGER app_products_no_delete
BEFORE DELETE ON app_products
FOR EACH ROW EXECUTE FUNCTION deny_immutable_history_delete();

CREATE TRIGGER app_product_versions_no_delete
BEFORE DELETE ON app_product_versions
FOR EACH ROW EXECUTE FUNCTION deny_immutable_history_delete();

CREATE TRIGGER app_product_version_tests_no_delete
BEFORE DELETE ON app_product_version_tests
FOR EACH ROW EXECUTE FUNCTION deny_immutable_history_delete();

CREATE TRIGGER app_secret_versions_no_update_delete
BEFORE UPDATE OR DELETE ON app_secret_versions
FOR EACH ROW EXECUTE FUNCTION deny_app_secret_version_mutation();

CREATE TRIGGER wallet_ledger_entries_no_truncate
BEFORE TRUNCATE ON wallet_ledger_entries
FOR EACH STATEMENT EXECUTE FUNCTION deny_immutable_history_truncate();

CREATE TRIGGER app_releases_no_truncate
BEFORE TRUNCATE ON app_releases
FOR EACH STATEMENT EXECUTE FUNCTION deny_immutable_history_truncate();

CREATE TRIGGER app_products_no_truncate
BEFORE TRUNCATE ON app_products
FOR EACH STATEMENT EXECUTE FUNCTION deny_immutable_history_truncate();

CREATE TRIGGER app_product_versions_no_truncate
BEFORE TRUNCATE ON app_product_versions
FOR EACH STATEMENT EXECUTE FUNCTION deny_immutable_history_truncate();

CREATE TRIGGER app_product_version_tests_no_truncate
BEFORE TRUNCATE ON app_product_version_tests
FOR EACH STATEMENT EXECUTE FUNCTION deny_immutable_history_truncate();

CREATE TRIGGER app_secret_versions_no_truncate
BEFORE TRUNCATE ON app_secret_versions
FOR EACH STATEMENT EXECUTE FUNCTION deny_immutable_history_truncate();

COMMIT;
