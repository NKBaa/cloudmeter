BEGIN;
DROP TRIGGER IF EXISTS bill_items_no_update_delete ON bill_items;
DROP TRIGGER IF EXISTS bills_restrict_update_delete ON bills;
DROP FUNCTION IF EXISTS deny_bill_item_mutation;
DROP FUNCTION IF EXISTS restrict_bill_mutation;
DROP TABLE IF EXISTS bill_items;
DROP TABLE IF EXISTS bills;
COMMIT;
