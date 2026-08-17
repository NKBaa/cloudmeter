BEGIN;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM daily_checkins) THEN
        RAISE EXCEPTION 'cannot roll back daily check-in migration while reward history exists';
    END IF;
END $$;

DROP TRIGGER IF EXISTS daily_checkins_no_update_delete ON daily_checkins;
DROP TRIGGER IF EXISTS daily_checkins_no_truncate ON daily_checkins;
DROP FUNCTION IF EXISTS deny_daily_checkin_mutation;
DROP TABLE IF EXISTS daily_checkins;
DROP TABLE IF EXISTS daily_checkin_settings;
ALTER TABLE wallet_ledger_entries DROP CONSTRAINT wallet_ledger_entries_business_type_check;
ALTER TABLE wallet_ledger_entries ADD CONSTRAINT wallet_ledger_entries_business_type_check
    CHECK (business_type IN ('topup','usage','subscription','refund','grant','adjustment','reversal'));

COMMIT;
