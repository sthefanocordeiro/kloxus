-- KLOX-13: paid-section checkouts. `payments` records each checkout
-- (AbacatePay billing or fake-provider grant); subscriptions.paid_until
-- ends an active subscription when the 30-day window lapses (D56: PIX
-- has no recurring billing on AbacatePay — one-time checkout + expiry).

ALTER TABLE subscriptions ADD COLUMN paid_until TEXT NOT NULL DEFAULT '';

CREATE TABLE payments (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workspace_id  TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    publication_id BIGINT NOT NULL REFERENCES publications(id) ON DELETE CASCADE,
    reader_id     BIGINT NOT NULL REFERENCES readers(id) ON DELETE CASCADE,
    ref           TEXT NOT NULL,             -- pay_... local ref; sent as externalId
    provider      TEXT NOT NULL,             -- 'abacatepay' | 'fake'
    provider_ref  TEXT NOT NULL DEFAULT '',  -- bill_... once the billing exists
    status        TEXT NOT NULL DEFAULT 'pending', -- pending | paid | expired | cancelled
    amount_cents  INTEGER NOT NULL DEFAULT 0,
    checkout_url  TEXT NOT NULL DEFAULT '',
    paid_until    TEXT NOT NULL DEFAULT '',
    last_polled_at TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL DEFAULT (now_utc()),
    UNIQUE (workspace_id, ref)
);

-- Webhook lookup is by provider_ref (the bill_ id in the event); the
-- poll endpoint looks up our own ref. Both are workspace-safe to scan.
CREATE INDEX payments_provider_ref ON payments (provider, provider_ref);
CREATE INDEX payments_reader ON payments (reader_id);
