BEGIN;

CREATE TABLE support_tickets (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    number bigserial UNIQUE NOT NULL,
    user_id uuid NOT NULL REFERENCES users(id),
    subject text NOT NULL CHECK (char_length(subject) BETWEEN 2 AND 160),
    category text NOT NULL CHECK (category IN ('deployment','billing','account','product','other')),
    priority text NOT NULL DEFAULT 'normal' CHECK (priority IN ('low','normal','high','urgent')),
    status text NOT NULL DEFAULT 'open' CHECK (status IN ('open','in_progress','waiting_user','resolved','closed')),
    last_message_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX support_tickets_user_idx ON support_tickets(user_id,last_message_at DESC);
CREATE INDEX support_tickets_admin_idx ON support_tickets(status,priority,last_message_at DESC);

CREATE TABLE support_ticket_messages (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    ticket_id uuid NOT NULL REFERENCES support_tickets(id) ON DELETE CASCADE,
    author_user_id uuid NOT NULL REFERENCES users(id),
    body text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 10000),
    staff_reply boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX support_ticket_messages_ticket_idx ON support_ticket_messages(ticket_id,created_at,id);

COMMIT;
