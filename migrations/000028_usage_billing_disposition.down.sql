BEGIN;

-- Preserve sealed legacy waivers so rolling back cannot retroactively bill
-- them. Only remove the explicit classification column.
ALTER TABLE usage_aggregates DROP COLUMN billing_disposition;

COMMIT;
