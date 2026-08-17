BEGIN;
CREATE TABLE user_notifications (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_key text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('low_balance','billing_suspended','billing_recovered')),
    severity text NOT NULL CHECK (severity IN ('info','warning','critical')),
    title text NOT NULL,
    content text NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    read_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id,event_key)
);
CREATE INDEX user_notifications_user_created_idx ON user_notifications(user_id,created_at DESC);
COMMIT;
