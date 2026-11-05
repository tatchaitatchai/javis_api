# Homes and zones

Status: Implemented — depends on `003-auth-phone-otp.md`  
Updated: 2026-11-05

## Context

The app has the approved UI on mock data: Create home (05), Zones (06), Home
switcher (07), Home settings (22). `HomeStore`
(`javis_app/lib/features/home/home_store.dart`) is the seam this API replaces.
Zones are either a translated preset (`Living room`, `Bedroom`, `Kitchen`,
`Front yard`, `Office`, `Bathroom`, `Kids room`) or a name the user typed.

## Goal

Persist a user's homes and zones, with role-based access on every operation.

## Non-goals

- Members and invitations (`005-members-and-invites.md`), except the owner row
  created with a home.
- Device counts and moving devices between zones (device spec).
- Renaming, reordering or deleting zones: the app has no UI for them yet.
- Transferring ownership.

## User flow

1. Onboarding: Create home plus Zones → `POST /v1/homes` creates both at once.
2. Sign-in → `GET /v1/homes`. An empty list leads to onboarding.
3. Home switcher reads the same list; the active home stays a client choice.
4. Settings → rename (`PATCH`), add zone (`POST …/zones`), delete home (`DELETE`).

## API contract

All routes require a session. A non-member receives 404 `home_not_found`, so
home ids cannot be probed.

| Operation | Minimum role | Success | Errors |
| --- | --- | --- | --- |
| `GET /v1/homes` | any member | 200 `{items: HomeSummary[]}` | — |
| `POST /v1/homes` `{name, zones[]}` | signed in | 201 `Home` | 400 `invalid_home`; 409 `home_limit_reached` |
| `GET /v1/homes/{homeId}` | member | 200 `Home` | 404 |
| `PATCH /v1/homes/{homeId}` `{name}` | admin | 200 `Home` | 400, 403 `forbidden`, 404 |
| `DELETE /v1/homes/{homeId}` | owner | 204 | 403, 404 |
| `POST /v1/homes/{homeId}/zones` `ZoneInput` | admin | 201 `Zone` | 400 `invalid_zone`, 403, 404, 409 `zone_limit_reached` |

```text
ZoneInput    exactly one of {preset_key} or {name}
Zone         {id, preset_key, name}   one of the two is null
HomeSummary  {id, name, role, zone_count, member_count}
Home         {id, name, role, zones: Zone[]}
```

`preset_key` must be one of the seven keys above; the client translates it.
Names are 1–60 characters after trimming. Limits: 20 homes per owner, 50 zones
per home (guards against abuse). `GET /v1/homes` has no pagination because of
that limit.

## Data model

```text
javis.homes         id uuid PK, name text, created_by FK users, created_at, updated_at
javis.zones         id uuid PK, home_id FK ON DELETE CASCADE, preset_key text NULL,
                    name text NULL, position int, created_at
                    CHECK (exactly one of preset_key, name)
javis.home_members  home_id FK ON DELETE CASCADE, user_id FK, role text, can_unlock bool,
                    created_at, PRIMARY KEY (home_id, user_id)
                    CHECK role IN ('owner','admin','member')
                    UNIQUE (home_id) WHERE role = 'owner'
```

`role` is text with a CHECK rather than a PostgreSQL enum, so adding a role is
a plain migration. The partial unique index guarantees one owner per home.

## Implementation notes

- Module `internal/home`, using the shape in `002-api-architecture.md`.
- `home.Service` exposes `RequireRole(ctx, userID, homeID, minimum)`; later
  modules declare their own small interface for it and `cmd/api` wires it.
- `internal/home/queries.sql` is used directly through generated `homedb`; there
  is no store wrapper because it would add no behavior.
- Creating a home takes a per-owner advisory lock and adding a zone locks the
  home row, so both limits hold under parallel requests.
- `zones: []` is accepted. The app's onboarding UI requires at least one zone;
  that remains a client rule.
- A home invalid because of a zone returns `invalid_home`, the operation's only
  400 code. `invalid_zone` belongs to `POST …/zones`.
- Permission checks run before input validation, so a member cannot learn
  whether a request would have been valid.
- `POST /v1/homes` creates home, zones and owner membership in one `WithTx`.
- The owner membership has `can_unlock = true`.
- `DELETE` removes the home and cascades to zones and members; the invitations
  table added by `005-members-and-invites.md` must cascade too. The
  device spec must add a refusal while real devices remain paired. The app's
  mock rule that the last home cannot be deleted stays client-side.

## Acceptance criteria

- A home appears only for its members; every operation is tested as non-member,
  member, admin and owner.
- A failed `POST /v1/homes` leaves no partial rows.
- A second owner row is impossible at database level.
- Presets validate; a zone with both or neither field is rejected.
- Limits return 409.

## Risks

- Deleting a home is irreversible; the app's confirmation dialog is the only
  guard, and devices are not covered until the device spec.

## Verification

Run on 2026-11-05 with Docker `postgres:17`:

- `gofmt -l .` no output; `go vet ./...` passed.
- `go test -race -count=1 ./...` passed. `internal/home/home_test.go` drives the
  real router and PostgreSQL: every operation as non-member, member, admin and
  owner; membership-scoped lists; session required on all six routes; malformed
  id; validation; cascade delete; zone order; both limits with 8 parallel
  requests; a failure injected by trigger during `POST /v1/homes` left no rows;
  second owner and invalid zone rows rejected by the database itself.
- `make migrate-up`, `migrate-down`, `migrate-up` on a throwaway container: 6
  javis tables, 3 after rolling back `000003_homes.sql`, 6 again.

Not run: the compiled binary over real HTTP, the Compose `db` service, and the
Flutter app, which does not call these routes yet.

## Current status

Implemented in `internal/home`. App integration is pending.
