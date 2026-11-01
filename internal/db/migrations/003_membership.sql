CREATE TABLE users (
    id            BIGSERIAL PRIMARY KEY,
    email         TEXT NOT NULL,
    password_hash TEXT,
    google_sub    TEXT UNIQUE,
    member_since  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX users_email_idx ON users (lower(email));

CREATE TABLE sessions (
    token_hash BYTEA PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX sessions_user_idx ON sessions (user_id);

CREATE TABLE orders (
    id             BIGSERIAL PRIMARY KEY,
    order_id       TEXT NOT NULL UNIQUE,
    user_id        BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount         BIGINT NOT NULL,
    status         TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'paid', 'expired', 'failed')),
    transaction_id TEXT NOT NULL DEFAULT '',
    qr_url         TEXT NOT NULL DEFAULT '',
    qr_string      TEXT NOT NULL DEFAULT '',
    expires_at     TIMESTAMPTZ NOT NULL,
    paid_at        TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX orders_user_idx ON orders (user_id, created_at DESC);

ALTER TABLE prompts ADD COLUMN is_premium BOOLEAN NOT NULL DEFAULT false;
