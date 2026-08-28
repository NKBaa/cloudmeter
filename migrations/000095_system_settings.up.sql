BEGIN;
CREATE TABLE IF NOT EXISTS system_settings (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    system_name text NOT NULL DEFAULT 'CloudMeter',
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by uuid REFERENCES users(id)
);
INSERT INTO system_settings(singleton, system_name) VALUES (true, 'CloudMeter') ON CONFLICT DO NOTHING;
COMMIT;
