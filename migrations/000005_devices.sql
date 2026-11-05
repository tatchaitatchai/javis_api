-- Which home a device belongs to, with the name and zone people gave it.
-- Live state and commands stay with the device provider.

-- +goose Up
CREATE TABLE javis.devices (
    id           uuid PRIMARY KEY,
    home_id      uuid NOT NULL REFERENCES javis.homes (id) ON DELETE RESTRICT,
    zone_id      uuid REFERENCES javis.zones (id) ON DELETE SET NULL,
    provider     text NOT NULL CHECK (provider IN ('tuya')),
    external_ref text NOT NULL,
    kind         text NOT NULL CHECK (kind IN ('light')),
    name         text,
    created_by   uuid NOT NULL REFERENCES javis.users (id),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT devices_one_home UNIQUE (provider, external_ref)
);
CREATE INDEX devices_home_idx ON javis.devices (home_id);

-- +goose Down
DROP TABLE javis.devices;
