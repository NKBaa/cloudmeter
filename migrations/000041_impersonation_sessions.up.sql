BEGIN;
CREATE TABLE impersonation_sessions (
    session_id uuid PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    actor_user_id uuid NOT NULL REFERENCES users(id),
    subject_user_id uuid NOT NULL REFERENCES users(id),
    write_enabled boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (actor_user_id <> subject_user_id)
);
CREATE INDEX impersonation_sessions_actor_idx ON impersonation_sessions(actor_user_id, created_at DESC);
COMMIT;
