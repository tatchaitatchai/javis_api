-- Homes, their zones and the people who belong to them.

-- +goose Up
CREATE TABLE javis.homes (
    id         uuid PRIMARY KEY,
    name       text NOT NULL,
    created_by uuid NOT NULL REFERENCES javis.users (id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- A zone is either an app preset, translated by the client, or a typed name.
CREATE TABLE javis.zones (
    id         uuid PRIMARY KEY,
    home_id    uuid NOT NULL REFERENCES javis.homes (id) ON DELETE CASCADE,
    preset_key text,
    name       text,
    position   integer NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT zones_preset_or_name CHECK ((preset_key IS NULL) <> (name IS NULL))
);
CREATE INDEX zones_home_position_idx ON javis.zones (home_id, position);

-- role is text with a CHECK, not an enum, so adding a role is a plain migration.
CREATE TABLE javis.home_members (
    home_id    uuid NOT NULL REFERENCES javis.homes (id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES javis.users (id) ON DELETE CASCADE,
    role       text NOT NULL CHECK (role IN ('owner', 'admin', 'member')),
    can_unlock boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (home_id, user_id)
);
CREATE INDEX home_members_user_idx ON javis.home_members (user_id);
CREATE UNIQUE INDEX home_members_one_owner_idx ON javis.home_members (home_id) WHERE role = 'owner';

-- +goose Down
DROP TABLE javis.home_members;
DROP TABLE javis.zones;
DROP TABLE javis.homes;
