-- Saved scenes. A scene is either an app preset, translated by the client, or a
-- typed name. Actions are validated by the API before they are stored.

-- +goose Up
CREATE TABLE javis.scenes (
    id          uuid PRIMARY KEY,
    home_id     uuid NOT NULL REFERENCES javis.homes (id) ON DELETE CASCADE,
    icon        text NOT NULL,
    preset_key  text,
    name        text,
    actions     jsonb NOT NULL,
    last_run_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT scenes_preset_or_name CHECK ((preset_key IS NULL) <> (name IS NULL))
);
CREATE INDEX scenes_home_idx ON javis.scenes (home_id);

-- +goose Down
DROP TABLE javis.scenes;
