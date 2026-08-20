BEGIN;
ALTER TABLE system_state ADD COLUMN IF NOT EXISTS initial_grant_cents bigint NOT NULL DEFAULT 0 CHECK (initial_grant_cents >= 0);
ALTER TABLE system_state ADD COLUMN IF NOT EXISTS invite_reward_cents bigint NOT NULL DEFAULT 0 CHECK (invite_reward_cents >= 0);
CREATE TABLE IF NOT EXISTS balance_alert_settings (
 user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 enabled boolean NOT NULL DEFAULT false,
 threshold_cents bigint NOT NULL DEFAULT 100 CHECK (threshold_cents >= 0),
 cooldown_hours integer NOT NULL DEFAULT 24 CHECK (cooldown_hours BETWEEN 1 AND 720),
 last_notified_at timestamptz,
 below_threshold boolean NOT NULL DEFAULT false,
 updated_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE user_notifications ADD COLUMN IF NOT EXISTS email_status text NOT NULL DEFAULT 'not_requested' CHECK (email_status IN ('not_requested','queued','sending','sent','failed'));
ALTER TABLE user_notifications ADD COLUMN IF NOT EXISTS email_attempts integer NOT NULL DEFAULT 0 CHECK (email_attempts >= 0);
ALTER TABLE user_notifications ADD COLUMN IF NOT EXISTS email_last_error text NOT NULL DEFAULT '';
ALTER TABLE user_notifications ADD COLUMN IF NOT EXISTS email_sent_at timestamptz;
ALTER TABLE user_notifications ADD COLUMN IF NOT EXISTS email_next_attempt_at timestamptz NOT NULL DEFAULT now();
CREATE TABLE IF NOT EXISTS user_invites (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), inviter_user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 code text NOT NULL UNIQUE, invited_user_id uuid UNIQUE REFERENCES users(id), reward_cents bigint NOT NULL DEFAULT 0, rewarded_at timestamptz, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE OR REPLACE FUNCTION grant_initial_wallet_credit() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE amount bigint; entry_id bigint;
BEGIN
 SELECT initial_grant_cents INTO amount FROM system_state WHERE singleton;
 IF coalesce(amount,0)>0 THEN
   INSERT INTO wallet_ledger_entries(wallet_id,business_type,business_ref,amount_cents,balance_after_cents,metadata)
   VALUES(NEW.id,'grant','initial/'||NEW.user_id::text,amount,amount,jsonb_build_object('source','initial_grant'))
   ON CONFLICT DO NOTHING RETURNING id INTO entry_id;
   IF entry_id IS NOT NULL THEN UPDATE wallets SET balance_cents=amount,version=version+1 WHERE id=NEW.id; END IF;
   IF entry_id IS NOT NULL THEN INSERT INTO audit_logs(subject_user_id,action,resource_type,resource_id,request_id,metadata) VALUES(NEW.user_id,'quota.initial-grant','wallet',NEW.id::text,'system/initial-grant/'||NEW.user_id::text,jsonb_build_object('amount_cents',amount,'ledger_entry_id',entry_id)); END IF;
 END IF; RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS wallets_initial_grant ON wallets;
CREATE TRIGGER wallets_initial_grant AFTER INSERT ON wallets FOR EACH ROW EXECUTE FUNCTION grant_initial_wallet_credit();
CREATE OR REPLACE FUNCTION reset_balance_alert_cycle() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 UPDATE balance_alert_settings SET below_threshold=false,updated_at=now() WHERE user_id=NEW.user_id AND below_threshold AND NEW.balance_cents>threshold_cents;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS wallets_balance_alert_recovery ON wallets;
CREATE TRIGGER wallets_balance_alert_recovery AFTER UPDATE OF balance_cents ON wallets FOR EACH ROW EXECUTE FUNCTION reset_balance_alert_cycle();
COMMIT;
CREATE OR REPLACE FUNCTION reset_balance_alert_cycle() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 UPDATE balance_alert_settings SET below_threshold=false,updated_at=now() WHERE user_id=NEW.user_id AND below_threshold AND NEW.balance_cents>threshold_cents;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS wallets_balance_alert_recovery ON wallets;
CREATE TRIGGER wallets_balance_alert_recovery AFTER UPDATE OF balance_cents ON wallets FOR EACH ROW EXECUTE FUNCTION reset_balance_alert_cycle();
COMMIT;
