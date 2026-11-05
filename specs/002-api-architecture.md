# API architecture and library baseline

Status: Foundation implemented — product modules not started  
Updated: 2026-11-02

## Context

`001-api-bootstrap.md` delivered a health-only service. MVP 1 needs phone/OTP
authentication, multiple homes, zones, members and roles, five Tuya bulbs, one
Tuya door lock, scenes and an activity log, with the API as the source of
truth and the only holder of Tuya Cloud credentials. JAVIS is intended to be
sold, so the structure and libraries must survive growth in features, team size
and traffic without a rewrite.

The owner chose a domain-first modular monolith ("option B") on 2026-11-02,
with domain-neutral code placed under `pkg/`.

## Goal

Define the package layout, dependency rules, API conventions and library set
that every later API feature spec builds on, then scaffold only the shared
foundation (no product endpoints).

## Non-goals

- Product endpoints (auth, homes, devices, scenes). Each gets its own spec.
- Microservices, message brokers, Kubernetes or production deployment.
- Choosing the SMS/OTP provider or the device command path (see Open decisions).

## Package layout

```text
javis_api/
  cmd/api/                 Process entry point and dependency wiring only
  api/openapi.yaml         HTTP contract (source of truth)
  api/oapi-codegen.yaml    Generator configuration
  internal/                JAVIS business code; not importable by other modules
    config/                Environment loading and validation (exists)
    httpapi/               Root router, middleware stack, composite contract server, health
      openapi/             Generated from api/openapi.yaml (do not edit)
    auth/                  Phone OTP, sessions
    home/                  Homes, zones, members, roles, access checks
    device/                Devices, commands, state; declares the Gateway interface
      tuyagateway/         Implements device.Gateway using pkg/tuya
    scene/                 Scenes and scene execution
    activity/              Activity / audit log
  pkg/                     Domain-neutral, reusable code
    httpx/                 JSON read/write, error envelope, request id, recover, access log
    postgres/              Pool construction, transaction helper
    tuya/                  Tuya Cloud OpenAPI client (signing, token, requests)
  migrations/              Versioned SQL migrations
  specs/
```

Business modules and `pkg/tuya` are created by their own feature specs; this
task created `pkg/httpx`, `pkg/postgres`, `internal/httpapi` routing and tooling.

Each business module uses the same small shape:

```text
internal/<module>/
  handler.go     Implements this module's generated strict-server operations
  service.go     Business rules and authorization
  queries.sql    SQL for sqlc
  <module>db/    Generated typed queries (add a store.go only if it adds behavior)
  <module>_test.go
```

Split a file by responsibility when it grows hard to follow; do not add layers
that carry no behavior.

## Contract-first workflow

1. Add or change the operation in `api/openapi.yaml` (operationId, tag,
   schemas, every response including errors).
2. Run `make generate`. oapi-codegen v2.8.2 regenerates
   `internal/httpapi/openapi/openapi.gen.go` (models, chi routes and the strict
   server interface). Generated code is committed and never edited by hand.
3. Implement the new methods in the module's handler. `internal/httpapi`
   embeds one handler per module in its `server` struct, through a type alias
   because every module's type is named `Handler`. The struct must satisfy
   `openapi.StrictServerInterface`; a missing operation fails compilation.
4. Map expected failures to the contract's typed responses. Errors returned
   from a handler are treated as bugs: logged by type only, answered with 500
   `internal_error`.

Routes exist only through the contract; there is no hand-registered `/v1`
group. Every route requires a session unless it is listed in `publicRoutes` in
`internal/httpapi/router.go` (see `003-auth-phone-otp.md`). Request input is
validated by the owning service, which returns the contract's specific error
codes; generic schema validation with kin-openapi was not needed.

## Dependency rules

1. `pkg/...` never imports `internal/...` and contains no JAVIS concepts
   (home, zone, scene, member). If it would make sense in another product, it
   may live in `pkg/`.
2. Within a module the direction is handler → service → generated queries.
   Handlers never touch the database; queries never decide authorization.
3. Modules do not import each other's generated queries. A module that needs
   another module declares a small interface on its own side (for example
   `scene.DeviceCommander`); `cmd/api` wires the concrete service in.
4. Every home-scoped service method checks membership/role through the `home`
   module's access interface. Route middleware authenticates; services authorize.
5. Vendor types (Tuya) appear only in `pkg/tuya` and
   `internal/device/tuyagateway`. Responses to clients use JAVIS types only.
6. `cmd/api` is the only place that constructs concrete dependencies.

## API conventions

- Versioned prefix `/v1`; health stays at `/health/live` and `/health/ready`.
- JSON only. Errors keep the existing envelope
  `{"error":{"code":"...","message":"..."}}`; codes are stable snake_case strings
  and messages never contain secrets, SQL or vendor errors.
- Identifiers are UUIDv7 (time-ordered, index-friendly), exposed as strings.
- Timestamps are RFC 3339 in UTC.
- List endpoints use cursor pagination (`cursor`, `limit`), not offsets.
- Mutating device/scene commands accept an `Idempotency-Key` header (defined in
  the device spec).
- Every request gets a request id, returned as `X-Request-Id` and attached to
  logs. A well-formed inbound id (1–64 of `[A-Za-z0-9._-]`) is kept.
- Every response is `Cache-Control: no-store`; JSON is served as
  `application/json` (the generated responses' media type).
- Access logs record the route template (`/v1/homes/{homeId}`), never the raw
  path, so identifiers stay out of logs.
- Request decoding/parameter errors return 400 `invalid_request` without echoing input.
- Structured logging with `log/slog`; never log tokens, OTP codes, phone numbers
  in full, DSNs or Tuya credentials.

## Library baseline

Latest versions and their minimum Go version were read from proxy.golang.org on
2026-11-02. Current Go releases at that date: go1.27.3 and go1.26.10.

### Adopt now (foundation)

| Need | Library | Version / Go min | Why |
| --- | --- | --- | --- |
| Router + middleware | `github.com/go-chi/chi/v5` | v5.5.0 / go 1.24 | Plain `net/http` handlers (no lock-in), route groups and per-group middleware that the stdlib mux lacks; mature and maintained |
| PostgreSQL driver | `github.com/jackc/pgx/v5` | v5.12.0 / go 1.25 | Already used; upgrade from v5.7.2 (same major) |
| Contract + server types | `github.com/oapi-codegen/oapi-codegen/v2` (CLI) | v2.8.2 / go 1.25 | Contract-first (D2). Dev tool pinned in the Makefile; generated chi + strict server |
| Typed SQL | `sqlc` (CLI, `github.com/sqlc-dev/sqlc`) | v1.31.1 / go 1.26 | Generates type-safe Go from plain SQL; no runtime ORM; scales with query count. Dev tool, not a runtime dependency |
| Migrations | `github.com/pressly/goose/v3` | v3.29.0 / go 1.26 | Versioned, recorded in the database, SQL or Go migrations, simple CLI (D3) |
| UUIDv7 | `github.com/google/uuid` | v1.6.0 | De-facto standard, includes `NewV7` |
| Logging | `log/slog` (stdlib) | — | Already used; structured, no dependency |
| Config | existing `internal/config` (stdlib) | — | Explicit validation without leaking values; revisit only if variables multiply |
| Integration tests | `github.com/testcontainers/testcontainers-go` | v0.44.0 / go 1.25 | Real PostgreSQL in tests instead of mocks for stores |

Since `003-auth-phone-otp.md` all of these are in use. Testcontainers uses the
module `testcontainers-go/modules/postgres` v0.44.0 and is reached only from
tests, so it is not linked into the API binary.

Contract-first keeps `api/openapi.yaml` as the agreed contract, generates Go
server types for chi, and lets the Flutter app generate its Dart client from
the same file. Code-first Huma v2.40.0 and hand-written handlers were the
rejected alternatives.

### Adopt when the feature that needs it arrives

| Need | Library | Version | Trigger |
| --- | --- | --- | --- |
| Background jobs / schedules | `github.com/riverqueue/river` | v0.51.0 / go 1.26 | Scheduled scenes, retrying device commands; PostgreSQL-backed, no new infrastructure |
| Metrics | `github.com/prometheus/client_golang` | v1.25.0 | First deployed environment |
| Tracing | `go.opentelemetry.io/otel` | v1.47.0 | First deployed environment |

### Rejected

- `github.com/tuya/tuya-connector-go`: last release v1.0.5 on 2021-08-30;
  treated as unmaintained. `pkg/tuya` will be a small in-house client built on
  `net/http`, following Tuya's current official signing documentation (to be
  read and cited in the device spec before implementation).
- ORMs (GORM, ent): hide SQL and query cost; sqlc keeps SQL explicit.
- Web frameworks with their own handler types (Gin, Echo, Fiber): unnecessary
  lock-in compared with chi on `net/http`.

## Toolchain

The workstation's system Go is 1.22.1, which is out of support and cannot build
the libraries above (they require Go 1.24–1.26). `go.mod` now declares
`go 1.26.0` and `toolchain go1.27.3`; with `GOTOOLCHAIN=auto` (the workstation
default) the go command downloaded go1.27.3 into the module cache. With it,
`go test ./...` runs without the old `-linkmode=external` workaround.

Pinned tools run with `go run module@version` outside the module, so the go
command selects the toolchain from the tool's own minimum (it switched to
go1.26.10 for goose and oapi-codegen). This does not affect the API build.

## Decisions

- **D1 Go toolchain upgrade** — Decided 2026-11-02: `go 1.26.0` minimum and a
  `toolchain go1.27.3` line in `go.mod`; the go command downloads the toolchain
  into the module cache (`GOTOOLCHAIN=auto`). `/usr/local/go` is left unchanged.
- **D2 Contract tooling** — Decided 2026-11-02: contract-first with
  oapi-codegen v2.8.2 (chi server + strict server, enum values always prefixed).
- **D3 Migration tool** — Decided 2026-11-02: goose v3.29.0 (go 1.26). The
  unapplied baseline `000001_bootstrap.up/down.sql` was converted to
  `000001_bootstrap.sql` with `-- +goose Up` / `-- +goose Down` sections. goose
  records versions in `public.goose_db_version`.
- **D4 Device command path** (blocks the device spec, not this one) — app → API
  → Tuya Cloud requires verifying that the Cloud project can control devices
  paired by users of the `com.javis.home` app. Unverified.
- **D5 SMS/OTP provider** (blocks the auth spec) — not chosen.

## Acceptance criteria (foundation task)

- Module builds with the agreed toolchain; tests run without the linker
  workaround, or the remaining blocker is recorded here.
- `pkg/httpx`, `pkg/postgres` exist with focused tests; `internal/httpapi`
  serves health from the generated contract through chi with request id,
  access-log, recover and no-store middleware; health status codes and bodies
  are unchanged.
- Migration tool chosen, baseline migration runnable through it, documented.
- `api/openapi.yaml` committed with a pinned generator and `make generate`.
  `sqlc.yaml` is added by the first module that has queries.
- `gofmt -l .`, `go test ./...`, `go vet ./...` pass; README updated.

## Risks

- Generators (sqlc, oapi-codegen) add a build step; mitigated by pinning
  versions and committing generated code.
- A shared `pkg/` can turn into a dumping ground; rule 1 above is the guard.
- Toolchain upgrade may surface new vet/test findings in existing code.
- Contract change from 001: `Content-Type` is now `application/json` without
  `; charset=utf-8` (JSON is UTF-8 by definition), and responses gain
  `X-Request-Id`. Clients must not compare the header string exactly.

## Verification

Executed on 2026-11-02 with go1.27.3 (via `toolchain`) on macOS arm64:

- Library versions and Go minimums checked against proxy.golang.org; Go
  releases against go.dev/dl.
- `gofmt -l .`: no output. `go vet ./...`: passed.
- `go test -count=1 ./...`: passed (config, httpapi, pkg/httpx, pkg/postgres)
  without the linker workaround.
- `make generate` twice: output byte-identical to the committed file.
- goose against a throwaway `postgres:17` container on 127.0.0.1:55432 with a
  random password (not the Compose database, not `.env`): `make migrate-up`
  applied version 1 and created schema `javis`; `migrate-status` listed it;
  `migrate-down` removed the schema; `migrate-up` re-applied it. `make -n
  migrate-up` does not contain the password. The container was removed.
- Compiled binary on 127.0.0.1:18080 against that container: `/health/ready`
  200 `{"status":"ready"}` with `Cache-Control: no-store` and `X-Request-Id`;
  `POST /health/live` 405 with `Allow: GET`; `/v1/homes/abc` 404; after
  stopping the database `/health/ready` 503 `database_unavailable`. The access
  log showed route templates only and did not contain the database password.
- Compose `db` first started on 2026-11-05 and reported healthy on PostgreSQL
  17.10. It publishes `127.0.0.1:5433` because 5432 was taken on the development
  machine, and uses `postgres:17` because the `postgres:17-alpine` pull did not
  finish.
- Not run: graceful-shutdown exit status.

## Current status

Foundation implemented. Next API specs: auth (blocked on D5), home, device
(blocked on D4), scene, activity.
