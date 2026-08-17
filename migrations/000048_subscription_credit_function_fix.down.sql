-- Migration 47 owns the function. Rolling back this corrective migration keeps
-- the corrected implementation so financial idempotency is never weakened.
SELECT 1;
