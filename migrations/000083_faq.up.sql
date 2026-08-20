BEGIN;
CREATE TABLE faqs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    question text NOT NULL CHECK (length(question) BETWEEN 1 AND 300),
    answer text NOT NULL CHECK (length(answer) BETWEEN 1 AND 10000),
    enabled boolean NOT NULL DEFAULT true,
    sort_order integer NOT NULL DEFAULT 0 CHECK (sort_order BETWEEN -1000000 AND 1000000),
    created_by uuid REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX faqs_public_order_idx ON faqs(sort_order,created_at,id) WHERE enabled;
COMMIT;
