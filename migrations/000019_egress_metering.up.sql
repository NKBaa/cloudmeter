BEGIN;
CREATE TABLE app_egress_cursors (
    user_app_id uuid PRIMARY KEY REFERENCES user_apps(id) ON DELETE CASCADE,
    cumulative_bytes bigint NOT NULL CHECK (cumulative_bytes >= 0),
    observed_at timestamptz NOT NULL,
    source text NOT NULL CHECK (source IN ('egress_gateway','egress_proxy')),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE app_egress_samples (
    id bigserial PRIMARY KEY,
    sample_id text NOT NULL UNIQUE,
    user_app_id uuid NOT NULL REFERENCES user_apps(id) ON DELETE CASCADE,
    cumulative_bytes bigint NOT NULL CHECK (cumulative_bytes >= 0),
    byte_delta bigint NOT NULL CHECK (byte_delta >= 0),
    observed_at timestamptz NOT NULL,
    source text NOT NULL CHECK (source IN ('egress_gateway','egress_proxy')),
    processed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(user_app_id,cumulative_bytes)
);
CREATE INDEX app_egress_samples_pending_idx ON app_egress_samples(observed_at) WHERE processed_at IS NULL;
CREATE TABLE app_egress_billing_cursors (
    user_app_id uuid PRIMARY KEY REFERENCES user_apps(id) ON DELETE CASCADE,
    billed_bytes bigint NOT NULL DEFAULT 0 CHECK (billed_bytes >= 0),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX app_egress_cursors_observed_idx ON app_egress_cursors(observed_at);
COMMIT;
