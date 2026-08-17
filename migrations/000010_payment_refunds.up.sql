BEGIN;
ALTER TABLE payment_orders DROP CONSTRAINT payment_orders_status_check;
ALTER TABLE payment_orders ADD CONSTRAINT payment_orders_status_check CHECK (status IN ('pending','paid','closed','failed','refunding','refunded'));
COMMIT;
