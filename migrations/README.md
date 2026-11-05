# Migrations

Migrations use goose v3.29.0 (pinned in the Makefile) with sequential SQL files
`NNNNNN_name.sql`, each containing `-- +goose Up` and `-- +goose Down` sections.
goose runs each file in a transaction and records applied versions in
`public.goose_db_version`.

`000001_bootstrap.sql` creates the empty `javis` namespace. Use
schema-qualified names such as `javis.homes` in later migrations.

```sh
set -a; source .env; set +a   # exports DATABASE_URL
make migrate-status
make migrate-up
make migrate-create NAME=create_homes
```

goose receives the connection string through `GOOSE_DBSTRING`, never on the
command line. The process never runs migrations during startup.

Down migrations avoid `CASCADE` so they fail instead of silently deleting data.
Run `make migrate-down` only deliberately, after checking data impact, and
never against a production database without explicit approval.
