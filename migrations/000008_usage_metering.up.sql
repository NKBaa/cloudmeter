BEGIN;
CREATE TABLE usage_events (
    id bigserial PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    user_app_id uuid REFERENCES user_apps(id) ON DELETE SET NULL,
    usage_code text NOT NULL,
    quantity numeric(30,12) NOT NULL CHECK (quantity >= 0),
    unit text NOT NULL,
    window_start timestamptz NOT NULL,
    window_end timestamptz NOT NULL,
    price_version_id uuid REFERENCES pricing_versions(id),
    idempotency_key text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (window_end > window_start),
    UNIQUE(user_id,usage_code,window_start,window_end,idempotency_key)
);
CREATE INDEX usage_events_user_window_idx ON usage_events(user_id,window_start DESC);
CREATE TABLE usage_aggregates (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    usage_code text NOT NULL,
    unit text NOT NULL,
    window_start timestamptz NOT NULL,
    window_end timestamptz NOT NULL,
    quantity numeric(30,12) NOT NULL CHECK (quantity >= 0),
    sealed_at timestamptz,
    PRIMARY KEY(user_id,usage_code,window_start,window_end)
);
COMMIT;
