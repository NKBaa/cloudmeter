BEGIN;

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE system_state (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    initialized_at timestamptz,
    initialized_by uuid,
    installation_id uuid UNIQUE,
    platform_name text NOT NULL DEFAULT 'CloudMeter',
    registration_enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((initialized_at IS NULL) = (initialized_by IS NULL))
);
INSERT INTO system_state (singleton) VALUES (true);

CREATE TABLE users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email text NOT NULL,
    password_hash text NOT NULL,
    display_name text NOT NULL,
    slug text NOT NULL,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended','closed')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_email_unique ON users (lower(email));
CREATE UNIQUE INDEX users_slug_unique ON users (lower(slug));

CREATE TABLE roles (
    id smallserial PRIMARY KEY,
    code text NOT NULL UNIQUE CHECK (code IN ('super_admin','admin','user'))
);
INSERT INTO roles (code) VALUES ('super_admin'), ('admin'), ('user');

CREATE TABLE user_roles (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id smallint NOT NULL REFERENCES roles(id),
    PRIMARY KEY (user_id, role_id)
);

ALTER TABLE system_state
    ADD CONSTRAINT system_state_initializer_fk FOREIGN KEY (initialized_by) REFERENCES users(id);

CREATE TABLE sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz
);

CREATE TABLE audit_logs (
    id bigserial PRIMARY KEY,
    actor_user_id uuid REFERENCES users(id),
    subject_user_id uuid REFERENCES users(id),
    action text NOT NULL,
    resource_type text NOT NULL,
    resource_id text,
    request_id text NOT NULL,
    ip inet,
    metadata jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE plans (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code text NOT NULL UNIQUE,
    name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE plan_versions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id uuid NOT NULL REFERENCES plans(id),
    version integer NOT NULL,
    cycle_price_cents bigint NOT NULL CHECK (cycle_price_cents >= 0),
    entitlements jsonb NOT NULL,
    effective_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (plan_id, version)
);

CREATE TABLE pricing_items (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code text NOT NULL UNIQUE,
    unit text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE pricing_versions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    pricing_item_id uuid NOT NULL REFERENCES pricing_items(id),
    version integer NOT NULL,
    unit_price_micros bigint NOT NULL CHECK (unit_price_micros >= 0),
    precision_scale smallint NOT NULL DEFAULT 6 CHECK (precision_scale BETWEEN 0 AND 12),
    rounding_mode text NOT NULL DEFAULT 'half_up' CHECK (rounding_mode IN ('up','down','half_up')),
    minimum_quantity numeric(30,12) NOT NULL DEFAULT 0,
    free_quantity numeric(30,12) NOT NULL DEFAULT 0,
    effective_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (pricing_item_id, version)
);

CREATE TABLE wallets (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL UNIQUE REFERENCES users(id),
    balance_cents bigint NOT NULL DEFAULT 0,
    version bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE wallet_ledger_entries (
    id bigserial PRIMARY KEY,
    wallet_id uuid NOT NULL REFERENCES wallets(id),
    business_type text NOT NULL CHECK (business_type IN ('topup','usage','refund','grant','adjustment','reversal')),
    business_ref text NOT NULL,
    amount_cents bigint NOT NULL CHECK (amount_cents <> 0),
    balance_after_cents bigint NOT NULL,
    reversal_of bigint UNIQUE REFERENCES wallet_ledger_entries(id),
    metadata jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (wallet_id, business_type, business_ref)
);

CREATE FUNCTION deny_ledger_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'wallet ledger is append-only';
END $$;
CREATE TRIGGER wallet_ledger_no_update_delete BEFORE UPDATE OR DELETE ON wallet_ledger_entries
FOR EACH ROW EXECUTE FUNCTION deny_ledger_mutation();

CREATE FUNCTION deny_release_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'application releases are immutable';
END $$;

CREATE TABLE app_products (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug text NOT NULL UNIQUE,
    name text NOT NULL,
    status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','testing','published','retired')),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE app_product_versions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id uuid NOT NULL REFERENCES app_products(id),
    version integer NOT NULL,
    image_digest text NOT NULL CHECK (image_digest ~ '^.+@sha256:[0-9a-f]{64}$'),
    runtime_spec jsonb NOT NULL,
    route_spec jsonb NOT NULL,
    health_spec jsonb NOT NULL,
    published_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (product_id, version)
);
CREATE TABLE user_apps (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id),
    product_id uuid NOT NULL REFERENCES app_products(id),
    slug text NOT NULL,
    service_slug text NOT NULL,
    status text NOT NULL DEFAULT 'stopped' CHECK (status IN ('stopped','deploying','running','updating','suspended','failed')),
    last_successful_release_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, slug), UNIQUE (user_id, service_slug)
);
CREATE TABLE app_releases (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_app_id uuid NOT NULL REFERENCES user_apps(id),
    product_version_id uuid NOT NULL REFERENCES app_product_versions(id),
    release_number integer NOT NULL,
    immutable_snapshot jsonb NOT NULL,
    state text NOT NULL CHECK (state IN ('created','active','superseded','failed','rolled_back')),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_app_id, release_number)
);
ALTER TABLE user_apps ADD CONSTRAINT user_apps_last_release_fk
    FOREIGN KEY (last_successful_release_id) REFERENCES app_releases(id);
CREATE TRIGGER app_releases_no_update_delete BEFORE UPDATE OR DELETE ON app_releases
FOR EACH ROW EXECUTE FUNCTION deny_release_mutation();

CREATE TABLE deployment_jobs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_app_id uuid NOT NULL REFERENCES user_apps(id),
    release_id uuid NOT NULL REFERENCES app_releases(id),
    idempotency_key text NOT NULL UNIQUE,
    state text NOT NULL DEFAULT 'queued' CHECK (state IN ('queued','pulling','starting','health_checking','switching_route','succeeded','rolling_back','failed')),
    attempts integer NOT NULL DEFAULT 0,
    available_at timestamptz NOT NULL DEFAULT now(),
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

COMMIT;
