BEGIN;
CREATE TABLE sidebar_visibility (
    menu_key text PRIMARY KEY CHECK(menu_key ~ '^[a-z][a-z0-9_]{0,63}$'),
    visible boolean NOT NULL DEFAULT true,
    updated_by uuid REFERENCES users(id),
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO sidebar_visibility(menu_key) VALUES
('overview'),('deploy'),('apps'),('releases'),('backups'),('billing'),('recharge'),('checkin'),('usage'),('tickets'),('faq');
COMMIT;
