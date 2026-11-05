# API bootstrap

Status: Complete (bootstrap; database integration not exercised)  
Updated: 2026-10-18

## Context and goal

JAVIS needs a small Go/PostgreSQL foundation alongside the iOS Flutter mockup.
Create a runnable HTTP process, reproducible local PostgreSQL, and explicit
health contracts without prematurely introducing product endpoints.

## Non-goals

Authentication, household/device persistence, Tuya commands, Flutter integration,
production deployment, and automatic migration execution are outside this task.

## API contract

- `GET /health/live`: HTTP 200, `{"status":"ok"}` while the process serves HTTP.
- `GET /health/ready`: ping PostgreSQL with a bounded context; HTTP 200 with
  `{"status":"ready"}` or HTTP 503 with a generic `database_unavailable` JSON
  error. Database errors and connection strings are never returned or logged.
- Unknown paths return JSON 404; unsupported methods return JSON 405 and `Allow`.
- All responses are JSON. Readiness does not inspect migrations or domain tables.

## Acceptance criteria

- Go 1.22-compatible service using `net/http` and a bounded pgx connection pool.
- Environment validation, server timeouts, SIGINT/SIGTERM graceful shutdown.
- Database outage affects readiness, not process liveness or startup.
- Local database binds only to loopback; credentials are supplied by local env.
- Explicit versioned schema baseline and documented manual migration workflow.
- Configuration and HTTP behavior have focused tests.
- `gofmt -l .`, `go test ./...`, and `go vet ./...` pass.

## Implementation notes

Use `cmd/api` for process wiring, `internal/config` for environment validation,
and `internal/httpapi` for transport contracts. Inject a ping function into the
HTTP handler so health checks are testable without PostgreSQL. Keep native Tuya
integration and the client mockup independent from this service. No auth or
product routes should be exposed until their own specs define authorization.

The initial SQL migration creates an empty `javis` schema. Future tables should
use explicit schema-qualified names. The down migration has no `CASCADE`, so it
cannot silently delete future tables. A real migration runner will be selected
when persistent domain features exist; this baseline is manually applied once.

## Risks

This bootstrap is local development infrastructure, not a production deployment.
`sslmode=disable` is documented only for the loopback development database.
There is no household or user data in the schema and no authenticated routes.
Dependency retrieval and local Docker/PostgreSQL availability may limit checks.

## Verification

Executed on 2026-10-18 with Go 1.22.1 on macOS arm64:

- `go mod tidy`: passed after allowing module download/cache access.
- `gofmt -l .`: passed with no output.
- `go vet ./...`: passed.
- `go test ./...`: compilation completed, but macOS dyld aborted both test
  executables with `missing LC_UUID load command` in the installed Go toolchain.
- `go test -ldflags=-linkmode=external ./...`: passed all configuration and
  handler tests using the system linker. This is the verified local workaround.
- `POSTGRES_PASSWORD=validation-only docker compose config --quiet`: passed.
- `go run -ldflags=-linkmode=external ./cmd/api` on temporary loopback port
  18080, with a deliberately unavailable database: real HTTP liveness returned
  200 and readiness returned the specified generic JSON 503. The temporary
  `go run` session was stopped with SIGINT after verification; its wrapper
  returned exit 1, so graceful-shutdown exit status was not independently
  verified on the compiled binary.

No PostgreSQL container was started and the SQL migrations were not applied.
Database success/failure behavior was verified with injected ping functions,
including context deadlines/cancellation and non-disclosure of raw errors.
Live PostgreSQL connectivity and migration execution remain a local setup check.

## Current status

Superseded in part by `002-api-architecture.md` (2026-11-02): the toolchain is
go1.27.3 without the linker workaround, routing uses chi and the generated
OpenAPI contract, `Content-Type` is `application/json` without a charset, and
the baseline migration is now the goose file `000001_bootstrap.sql`.

Bootstrap implemented with documented commands, focused tests, and no product
data routes. The Flutter mockup remains independent. Use a current supported Go
toolchain before production; the workstation's Go 1.22.1 requires the external
linker workaround described above to execute tests on its current macOS.
