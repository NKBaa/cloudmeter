BEGIN;

-- Financial ledger entries are append-only. A rollback must not erase the
-- compensating entries or rewrite wallet history.

COMMIT;
