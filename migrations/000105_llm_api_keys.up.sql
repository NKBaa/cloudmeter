BEGIN;

CREATE TABLE llm_api_keys (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    name text NOT NULL DEFAULT '默认大模型密钥',
    token_hash bytea NOT NULL UNIQUE,
    token_prefix text NOT NULL,
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    revoked_at timestamptz
);

COMMIT;
