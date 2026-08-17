BEGIN;
DROP TRIGGER IF EXISTS users_default_subscription ON users;
DROP FUNCTION IF EXISTS assign_default_subscription;
DROP TABLE IF EXISTS user_subscriptions;
COMMIT;
