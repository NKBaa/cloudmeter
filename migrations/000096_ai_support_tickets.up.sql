BEGIN;

CREATE TABLE ai_support_settings (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    enabled boolean NOT NULL DEFAULT false,
    provider text NOT NULL DEFAULT 'openai' CHECK (provider IN ('openai', 'deepseek', 'anthropic', 'custom')),
    base_url text NOT NULL DEFAULT '',
    api_key text NOT NULL DEFAULT '',
    model_name text NOT NULL DEFAULT 'gpt-4o',
    system_prompt text NOT NULL DEFAULT 'You are a helpful customer support AI. Use the provided knowledge base to answer user questions about our platform. If the user asks something not in the knowledge base, answer politely and suggest they escalate to human support if necessary.',
    knowledge_base text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO ai_support_settings(singleton) VALUES(true);

ALTER TABLE support_tickets ADD COLUMN escalated_to_human boolean NOT NULL DEFAULT false;

ALTER TABLE support_ticket_messages ADD COLUMN ai_reply boolean NOT NULL DEFAULT false;

COMMIT;
