# Phone OTP sign-in and sessions

Status: API implemented in debug mode; Flutter integration and a real SMS provider pending  
Updated: 2026-11-05

## Context

The app already has the approved sign-in UI on mock data: Welcome (01), Phone
(02), OTP (03, six digits, 45-second resend), Profile (04). `AccountStore`
(`javis_app/lib/features/account/account_store.dart`) is the seam this API
replaces. One number serves both sign-up and sign-in; there is no password.

## Goal

Real sign-in by Thai mobile number: request a code, verify it, receive a
revocable session, read and name the account, sign out.

## Non-goals

- Password, social or email sign-in; non-Thai numbers.
- Account deletion and changing phone number (separate spec before launch).
- Choosing an SMS provider in this spec (decision D1).
- Flutter integration (separate app spec).

## User flow

1. Phone screen → `POST /v1/auth/otp/request`; the OTP screen starts the resend
   countdown from `resend_after_seconds`.
2. Sixth digit typed → `POST /v1/auth/otp/verify`.
3. `user.name` is null → Profile screen → `PATCH /v1/me`. Otherwise dashboard.
4. Me → sign out → `POST /v1/auth/logout`.

## API contract

Phone numbers are E.164: `^\+66[689]\d{8}$`, matching the app's
`normalizeThaiMobile` rule (`^0[689]\d{8}$`). Authenticated routes use
`Authorization: Bearer <token>`.

| Operation | Success | Errors |
| --- | --- | --- |
| `POST /v1/auth/otp/request` `{phone}` | 202 `{resend_after_seconds, expires_in_seconds, debug_code?}` | 400 `invalid_phone`; 429 `rate_limited` + `Retry-After`; 503 `sms_unavailable` |
| `POST /v1/auth/otp/verify` `{phone, code}` | 200 `{token, expires_at, user}` | 400 `invalid_request`; 401 `invalid_code`; 429 `too_many_attempts` |
| `POST /v1/auth/logout` | 204 | 401 `unauthorized` |
| `GET /v1/me` | 200 `User` | 401 `unauthorized` |
| `PATCH /v1/me` `{name}` | 200 `User` | 400 `invalid_name`; 401 `unauthorized` |

`User` is `{id, phone, name}`; `name` is null until the profile step. `name` is
1–60 characters after trimming.

`request` answers the same whether or not the number has an account, so it
cannot be used to discover customers. `verify` answers `invalid_code` for a
wrong, expired, consumed or unknown code. The first successful verify creates
the user.

## Debug mode

For use until an SMS provider is connected (requested by the owner on
2026-11-05).

- `OTP_SENDER=debug` sends no SMS. `POST /v1/auth/otp/request` adds
  `debug_code` to its 202 response, and the OTP screen shows it in a clearly
  labelled debug banner. The field is optional in the contract and absent
  otherwise.
- Everything else stays real: the code is random, stored as an HMAC, expires,
  counts attempts and hits the same rate limits. Debug mode tests the true flow.
- Configuration fails at startup unless `APP_ENV=development`, and logs one
  warning when active. `OTP_SENDER` has no default: it must be chosen explicitly.
- The code is returned only in that response and is never logged.

With debug mode on, anyone who can reach the API can sign in as any number. Run
it only on a local machine or private network, with no real household data.

## Data model

```text
javis.users          id uuid PK, phone text UNIQUE, name text NULL, created_at, updated_at
javis.otp_challenges id uuid PK, phone text, code_hash bytea, attempts int,
                     expires_at, consumed_at NULL, created_at
                     INDEX (phone, created_at DESC)
javis.sessions       id uuid PK, user_id FK ON DELETE CASCADE, token_hash bytea UNIQUE,
                     created_at, last_used_at, expires_at, revoked_at NULL
```

## Security rules

| Rule | Value |
| --- | --- |
| Code | 6 digits from `crypto/rand` |
| Code storage | HMAC-SHA256 with server secret `OTP_PEPPER`; a plain hash of six digits is trivially reversed |
| Code lifetime | 5 minutes, single use; a new request invalidates earlier codes |
| Resend | 45 seconds per number (same as the app) |
| Attempts | 5 per code, counted under a row lock (`SELECT … FOR UPDATE`) so parallel guesses cannot exceed it |
| Sends | 5 per number and 20 per client IP per hour, counted in PostgreSQL under a per-number advisory lock; the hourly limits answer `Retry-After: 3600` |
| Comparison | Constant time (`hmac.Equal`) |
| Session token | 32 random bytes, base64url; only its SHA-256 is stored |
| Session lifetime | 90 days, extended on use; `last_used_at` written at most hourly |

Per-IP limiting trusts the socket address until a deployment spec defines a
trusted proxy; behind a proxy it needs that configuration to be meaningful.

Challenges hold phone numbers and IP addresses, so a background job deletes them
a day after creation, along with expired sessions.

All routes need a session unless listed in `publicRoutes`
(`internal/httpapi/router.go`), so a new operation is protected by default. If
the session check itself fails, the API answers 503 `service_unavailable`
rather than 401, so a database outage does not sign the app out.

Never log codes, tokens, `OTP_PEPPER` or full phone numbers.

## Implementation notes

- Module `internal/auth`: `handler.go`, `service.go`, `queries.sql`, generated
  `authdb/`. There is no separate `store.go`: the service uses the typed queries
  directly because a wrapper would add no behavior.
- This is the first sqlc user, so it added `sqlc.yaml`, `make generate` coverage,
  `google/uuid` and testcontainers. The per-IP limit uses PostgreSQL instead of
  `httprate`: it works across several API instances and adds no dependency.
- The service validates input, so `invalid_phone` and `invalid_name` come from the
  contract's own codes. Generic schema validation (kin-openapi) is not added.
- Tests run through the real HTTP router against real PostgreSQL
  (`internal/testdb`, image `postgres:17`, Docker required). `go test -short`
  skips them. Migrations are applied by reading the goose Up sections, which
  keeps goose's database drivers out of `go.mod`.
- `auth.SMSSender` interface: `Send(ctx, phone, code) error`. Providers live in
  `internal/auth/smssender/`.
- Debug sender: see "Debug mode". It implements `auth.SMSSender` and sends
  nothing, so the service flow is identical to the real one.
- OpenAPI declares the bearer security scheme for documentation. Enforcement is
  `requireSessionUnlessPublic`, which stores the caller's `auth.Principal` in the
  context. oapi-codegen's scopes-on-context option was not used because a missing
  `security:` line would leave a route open.
- `OTP_SENDER` currently accepts only `debug`, so the API cannot start in
  production until a provider is added. That is intentional.

## Acceptance criteria

- Contract in `api/openapi.yaml`; requests validated; errors use the envelope.
- Correct code signs in; wrong, expired, reused and superseded codes fail.
- The sixth wrong attempt fails even with the correct code, including when
  attempts arrive in parallel.
- Send limits return 429 with `Retry-After`.
- Logout revokes the token immediately; an expired or revoked token gets 401.
- Database tests run against real PostgreSQL through testcontainers.
- With `OTP_SENDER=debug`, `debug_code` is returned and that code verifies; with
  any other sender the field is absent from the response.
- The API refuses to start with `OTP_SENDER=debug` unless `APP_ENV=development`.
- The app shows the debug code only when the field is present.

## Risks

- SMS pumping (attackers triggering paid SMS) costs real money; the send limits
  are the first guard and the provider's account limit should be the second.
- Phone numbers are personal data; Thai PDPA obligations are unverified and need
  review before selling.
- Sessions live 90 days, so a lost phone relies on the device lock until a
  "sign out everywhere" feature exists.

## Open decisions

- **D1 SMS provider.** Not chosen. Work continues in debug mode; a real SMS
  cannot be sent until this is decided.

## Verification

Executed on 2026-11-05 with go1.27.3 on macOS arm64:

- `gofmt -l .`: no output. `go vet ./...`: passed.
- `go test -race -count=1 ./...`: passed, including `internal/auth` against a real
  `postgres:17` container with both migrations: sign-in flow, no account
  disclosure, debug code, wrong/expired/reused/superseded/other-number codes,
  five-attempt limit including 20 parallel guesses, send limits including parallel
  requests, failed SMS, input validation, oversized body, bad and expired tokens,
  session extension, database outage answering 503, hashed storage and cleanup.
- `make migrate-up`, `migrate-down`, `migrate-status`, `migrate-up` against a
  throwaway `postgres:17` container: 000002 applied, reverted and re-applied.
- `TestOTPDebugModeOnlyRunsInDevelopment`: config refuses debug mode with
  `APP_ENV` unset or `production`.

Not run:

- The compiled binary end to end with curl: the tool permission check denied
  those commands. The HTTP flow is covered through `httpapi.NewHandler` in tests,
  but not through `cmd/api` wiring, the startup warning or `RunCleanup` startup.
- The Compose `db` service, a real SMS, Flutter, and the debug banner.

## Current status

API implemented with debug mode. Next: app spec and Flutter integration, including
the debug banner on the OTP screen.
