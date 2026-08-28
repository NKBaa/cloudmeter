BEGIN;

ALTER TABLE support_ticket_messages DROP COLUMN ai_reply;
ALTER TABLE support_tickets DROP COLUMN escalated_to_human;
DROP TABLE ai_support_settings;

COMMIT;
