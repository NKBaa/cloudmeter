BEGIN;

CREATE TABLE oauth_flows (
    state_hash bytea PRIMARY KEY,
    provider text NOT NULL REFERENCES oauth_settings(provider),
    redirect_uri text NOT NULL,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX oauth_flows_expiry_idx ON oauth_flows(expires_at);

CREATE TABLE oauth_bindings (
    provider text NOT NULL REFERENCES oauth_settings(provider),
    provider_user_id text NOT NULL,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider_username text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(provider, provider_user_id),
    UNIQUE(provider, user_id)
);

CREATE TABLE oauth_login_results (
    code_hash bytea PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX oauth_login_results_expiry_idx ON oauth_login_results(expires_at);

COMMIT;
