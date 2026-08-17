BEGIN;

CREATE FUNCTION protect_app_product_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id <> OLD.id
       OR NEW.slug <> OLD.slug
       OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'product identity is immutable';
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER app_products_identity_immutable
BEFORE UPDATE ON app_products
FOR EACH ROW EXECUTE FUNCTION protect_app_product_identity();

COMMIT;
