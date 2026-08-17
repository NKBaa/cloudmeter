BEGIN; DROP TABLE IF EXISTS oauth_settings; ALTER TABLE system_state DROP COLUMN IF EXISTS email_domain_whitelist; ALTER TABLE system_state DROP COLUMN IF EXISTS block_email_aliases; COMMIT;
