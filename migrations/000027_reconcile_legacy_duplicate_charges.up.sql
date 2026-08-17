BEGIN;

-- Migration 24 briefly allowed app-scoped charges to be created for windows
-- that already had an account-scoped legacy charge. Preserve both immutable
-- usage records and compensate only the duplicated app-scoped ledger entry.
DO $$
DECLARE
    target_user record;
    duplicate_charge record;
    wallet_id_value uuid;
    wallet_balance bigint;
    reversed_count bigint;
    reversed_cents bigint;
BEGIN
    FOR target_user IN
        SELECT DISTINCT c.user_id
        FROM usage_charges c
        WHERE c.user_app_id IS NOT NULL
          AND EXISTS (
              SELECT 1
              FROM usage_charges legacy
              WHERE legacy.user_id = c.user_id
                AND legacy.user_app_id IS NULL
                AND legacy.usage_code = c.usage_code
                AND legacy.window_start = c.window_start
                AND legacy.window_end = c.window_end
          )
        ORDER BY c.user_id
    LOOP
        SELECT w.id, w.balance_cents
        INTO STRICT wallet_id_value, wallet_balance
        FROM wallets w
        WHERE w.user_id = target_user.user_id
        FOR UPDATE;

        reversed_count := 0;
        reversed_cents := 0;

        FOR duplicate_charge IN
            SELECT c.id, c.amount_cents, c.wallet_ledger_entry_id,
                   c.user_app_id, c.usage_code, c.window_start, c.window_end
            FROM usage_charges c
            JOIN wallet_ledger_entries original
              ON original.id = c.wallet_ledger_entry_id
             AND original.wallet_id = wallet_id_value
             AND original.business_type = 'usage'
             AND original.amount_cents = -c.amount_cents
            WHERE c.user_id = target_user.user_id
              AND c.user_app_id IS NOT NULL
              AND c.amount_cents > 0
              AND EXISTS (
                  SELECT 1
                  FROM usage_charges legacy
                  WHERE legacy.user_id = c.user_id
                    AND legacy.user_app_id IS NULL
                    AND legacy.usage_code = c.usage_code
                    AND legacy.window_start = c.window_start
                    AND legacy.window_end = c.window_end
              )
              AND NOT EXISTS (
                  SELECT 1
                  FROM wallet_ledger_entries reversal
                  WHERE reversal.reversal_of = c.wallet_ledger_entry_id
              )
            ORDER BY c.id
        LOOP
            wallet_balance := wallet_balance + duplicate_charge.amount_cents;

            INSERT INTO wallet_ledger_entries(
                wallet_id, business_type, business_ref, amount_cents,
                balance_after_cents, reversal_of, metadata
            ) VALUES (
                wallet_id_value,
                'reversal',
                'usage-reversal/' || duplicate_charge.id::text,
                duplicate_charge.amount_cents,
                wallet_balance,
                duplicate_charge.wallet_ledger_entry_id,
                jsonb_build_object(
                    'reason', 'legacy_app_charge_compatibility',
                    'usage_charge_id', duplicate_charge.id,
                    'user_app_id', duplicate_charge.user_app_id,
                    'usage_code', duplicate_charge.usage_code,
                    'window_start', duplicate_charge.window_start,
                    'window_end', duplicate_charge.window_end
                )
            );

            reversed_count := reversed_count + 1;
            reversed_cents := reversed_cents + duplicate_charge.amount_cents;
        END LOOP;

        IF reversed_count > 0 THEN
            UPDATE wallets
            SET balance_cents = wallet_balance, version = version + reversed_count
            WHERE id = wallet_id_value;

            INSERT INTO audit_logs(
                subject_user_id, action, resource_type, resource_id, request_id, metadata
            ) VALUES (
                target_user.user_id,
                'usage.legacy_duplicate.reconcile',
                'wallet',
                wallet_id_value::text,
                'migration:000027',
                jsonb_build_object(
                    'reversal_count', reversed_count,
                    'reversal_amount_cents', reversed_cents
                )
            );
        END IF;
    END LOOP;
END $$;

COMMIT;
