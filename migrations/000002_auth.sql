-- Phone OTP sign-in: accounts, one-time codes and sessions.

-- +goose Up
CREATE TABLE javis.users (
    id         uuid PRIMARY KEY,
    phone      text NOT NULL UNIQUE,
    name       text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Rows are short-lived: the API purges them a day after creation, because
-- phone and client_ip are personal data and only needed for rate limits.
CREATE TABLE javis.otp_challenges (
    id          uuid PRIMARY KEY,
    phone       text NOT NULL,
    client_ip   text NOT NULL,
    code_hash   bytea NOT NULL,
    attempts    integer NOT NULL DEFAULT 0,
    expires_at  timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX otp_challenges_phone_created_idx ON javis.otp_challenges (phone, created_at DESC);
CREATE INDEX otp_challenges_client_ip_created_idx ON javis.otp_challenges (client_ip, created_at DESC);

CREATE TABLE javis.sessions (
    id           uuid PRIMARY KEY,
    user_id      uuid NOT NULL REFERENCES javis.users (id) ON DELETE CASCADE,
    token_hash   bytea NOT NULL UNIQUE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    revoked_at   timestamptz
);
CREATE INDEX sessions_user_id_idx ON javis.sessions (user_id);

-- +goose Down
DROP TABLE javis.sessions;
DROP TABLE javis.otp_challenges;
DROP TABLE javis.users;
