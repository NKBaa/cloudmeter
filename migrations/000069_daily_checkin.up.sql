BEGIN;

ALTER TABLE wallet_ledger_entries
    DROP CONSTRAINT wallet_ledger_entries_business_type_check;
ALTER TABLE wallet_ledger_entries
    ADD CONSTRAINT wallet_ledger_entries_business_type_check
    CHECK (business_type IN ('topup','usage','subscription','refund','grant','adjustment','reversal','checkin_reward'));

CREATE TABLE daily_checkin_settings (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    enabled boolean NOT NULL DEFAULT true,
    min_reward_cents bigint NOT NULL DEFAULT 1 CHECK (min_reward_cents BETWEEN 1 AND 10000),
    max_reward_cents bigint NOT NULL DEFAULT 10 CHECK (max_reward_cents BETWEEN min_reward_cents AND 10000),
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by uuid REFERENCES users(id)
);
INSERT INTO daily_checkin_settings(singleton) VALUES(true);

CREATE TABLE daily_checkins (
    id bigserial PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    checkin_date date NOT NULL,
    reward_cents bigint NOT NULL CHECK (reward_cents BETWEEN 0 AND 10000),
    wallet_ledger_entry_id bigint NOT NULL UNIQUE REFERENCES wallet_ledger_entries(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(user_id, checkin_date)
);
CREATE INDEX daily_checkins_user_date_idx ON daily_checkins(user_id, checkin_date DESC);

CREATE FUNCTION deny_daily_checkin_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'daily check-ins are immutable';
END $$;
CREATE TRIGGER daily_checkins_no_update_delete
BEFORE UPDATE OR DELETE ON daily_checkins
FOR EACH ROW EXECUTE FUNCTION deny_daily_checkin_mutation();
CREATE TRIGGER daily_checkins_no_truncate
BEFORE TRUNCATE ON daily_checkins
FOR EACH STATEMENT EXECUTE FUNCTION deny_daily_checkin_mutation();

COMMIT;
