-- Baseline: the empty javis namespace. Future tables use schema-qualified
-- names such as javis.homes. goose runs each migration in a transaction.

-- +goose Up
CREATE SCHEMA javis;

-- +goose Down
-- No CASCADE: this fails safely once any object exists in the schema.
DROP SCHEMA javis;
