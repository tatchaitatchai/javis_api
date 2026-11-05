# JAVIS API

Go/PostgreSQL API for JAVIS Home. The Flutter app uses it when built with
`JAVIS_API_URL`, and stays on local demo data otherwise. Health, phone OTP
sign-in, homes, zones, members, invitations, the device registry and scenes are
implemented. The package layout, dependency rules and library choices are defined
in `specs/002-api-architecture.md`.

## Local development

Requirements: any installed Go with `GOTOOLCHAIN=auto` (the default; `go.mod`
pins `toolchain go1.27.3` and the go command downloads it), Docker with Compose.
The goose and oapi-codegen CLIs are run through pinned `go run` commands in the
Makefile; nothing else needs installing.

```sh
cp .env.example .env
# Edit .env: replace both password placeholders with the same local password.
docker compose up -d db
set -a
source .env
set +a
make migrate-up
go run ./cmd/api
```

Compose publishes PostgreSQL on `127.0.0.1:5433`, so `DATABASE_URL` must use that
port. `.env` is local and ignored. Compose reads it automatically; the Go process reads
the environment only, so export it as shown. URL-encode special characters in
the password inside `DATABASE_URL`. The example uses disabled TLS for loopback
development; use TLS and managed secrets for a production database.

If the container is still starting, wait for `docker compose ps` to report it
healthy before applying migrations. Migrations never run during API startup;
see `migrations/README.md`. The API still starts when PostgreSQL is offline so
liveness remains available.

```sh
curl http://127.0.0.1:8080/health/live
curl -i http://127.0.0.1:8080/health/ready
```

| Variable | Default | Meaning |
| --- | --- | --- |
| `DATABASE_URL` | Required | PostgreSQL URL including host and database |
| `HTTP_ADDR` | `127.0.0.1:8080` | HTTP listen address |
| `DB_HEALTH_TIMEOUT` | `2s` | Positive readiness timeout, maximum `5s` |
| `APP_ENV` | `production` | `development` or `production` |
| `OTP_SENDER` | Required | `debug` is the only value until an SMS provider is connected |
| `OTP_PEPPER` | Required | At least 32 characters; create with `openssl rand -hex 32` |
| `POSTGRES_PASSWORD` | Required by Compose | Password for the local container |

The HTTP contract is `api/openapi.yaml`. `GET /health/live` returns HTTP 200
and `{"status":"ok"}`. `GET /health/ready` returns HTTP 200 and
`{"status":"ready"}` when the database responds, otherwise HTTP 503 with
`{"error":{"code":"database_unavailable","message":"Database is unavailable"}}`.
Readiness checks connectivity only, not migration version. Unknown endpoints
and unsupported methods return the same JSON error envelope with 404/405.
Every response carries `X-Request-Id` and `Cache-Control: no-store`.

To change the API, edit `api/openapi.yaml`, run `make generate`, then implement
the generated strict-server method in the owning module.

## OTP debug mode

With `APP_ENV=development` and `OTP_SENDER=debug` no SMS is sent, and
`POST /v1/auth/otp/request` returns the code as `debug_code`. The API refuses to
start in this mode outside development. Anyone who can reach the API can sign in
as any number, so run it only on your machine or private network, with no real
household data. Send limits stay on: 5 codes per number per hour, so use another
valid number if you hit the limit while testing.

## Structure and checks

```text
cmd/api/                  Process wiring and graceful shutdown
api/                      OpenAPI contract and generator config
internal/config/          Environment validation
internal/httpapi/         Router, middleware, contract server, health
internal/httpapi/openapi/ Generated code (make generate; do not edit)
internal/auth/            Phone OTP, sessions, the signed-in account
internal/home/            Homes, zones, members, invitations and role checks
internal/testdb/          Real PostgreSQL for database tests
pkg/httpx/                JSON/error envelope and HTTP middleware
pkg/postgres/             Pool construction and transactions
migrations/               goose SQL migrations
specs/                    Task contracts and verification history
```

```sh
gofmt -l .
go test ./...        # Needs Docker; go test -short skips database tests
go vet ./...
# Or: make check
```

| Make target | Purpose |
| --- | --- |
| `make generate` | Regenerate Go code from `api/openapi.yaml` and `queries.sql` files |
| `make migrate-up` / `migrate-down` / `migrate-status` | goose against `$DATABASE_URL` |
| `make migrate-create NAME=create_homes` | New sequential SQL migration |

The earlier Go 1.22.1 `missing LC_UUID load command` test failure does not
occur with the pinned go1.27.3 toolchain; no linker workaround is needed.

Stop PostgreSQL with `docker compose stop db`; the named volume retains data.
Changing `POSTGRES_PASSWORD` after the first start does not change an existing
database role password. Keep the existing local value or change the role
password explicitly. Never delete the database volume to fix credentials
without first checking whether its data is needed.

See `specs/001-api-bootstrap.md` for scope, acceptance criteria, and actual checks.
