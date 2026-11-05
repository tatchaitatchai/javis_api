# JAVIS API engineering

Read `../AGENTS.md`, `../PROJECT_SPEC.md`, and the relevant `specs/` file first.
Create/update a task spec before every material change and record actual checks.

- Go 1.26+ (toolchain go1.27.3 pinned in `go.mod`), chi on `net/http`, pgx v5,
  PostgreSQL, goose migrations, contract-first OpenAPI with oapi-codegen.
  Layout, dependency rules and library choices: `specs/002-api-architecture.md`.
- Entry points belong in `cmd/`, JAVIS business code in `internal/<module>/`,
  domain-neutral reusable code in `pkg/` (never importing `internal/`), explicit
  SQL changes in `migrations/`. Do not introduce a framework without a real need.
- Change HTTP behavior by editing `api/openapi.yaml` and running
  `make generate`; never edit `internal/httpapi/openapi/*.gen.go` by hand.
- Pass contexts to database work, bound timeouts, and close process resources.
- Validate configuration and request input without exposing supplied secrets.
- Never log DSNs, passwords, raw database errors, or real home/device identifiers.
- Liveness is process-only; readiness probes PostgreSQL with a timeout.
- Product routes need their own authorization, ownership, validation, and failure
  contracts first. Routes require a session unless listed in `publicRoutes`.
- Do not claim integration or migration checks ran unless they actually ran.
- Before handoff run `gofmt -l .`, `go test ./...`, and `go vet ./...`.
- No production deployments, commits, or pushes without an explicit request.
