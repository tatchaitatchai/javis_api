-- Invitations to join a home, matched to the invitee by verified phone number.

-- +goose Up
CREATE TABLE javis.home_invites (
    id           uuid PRIMARY KEY,
    home_id      uuid NOT NULL REFERENCES javis.homes (id) ON DELETE CASCADE,
    phone        text NOT NULL,
    role         text NOT NULL CHECK (role IN ('admin', 'member')),
    can_unlock   boolean NOT NULL,
    invited_by   uuid NOT NULL REFERENCES javis.users (id),
    status       text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'accepted', 'declined', 'revoked', 'expired')),
    expires_at   timestamptz NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    responded_at timestamptz
);
CREATE UNIQUE INDEX home_invites_one_pending_idx ON javis.home_invites (home_id, phone) WHERE status = 'pending';
CREATE INDEX home_invites_pending_phone_idx ON javis.home_invites (phone) WHERE status = 'pending';

-- +goose Down
DROP TABLE javis.home_invites;
