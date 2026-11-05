# Devices and scenes

Status: Implemented — depends on `005-members-and-invites.md`  
Updated: 2026-11-05

## Context

The app keeps device names and zones in memory and seeds scenes on the device,
so they vanish on restart and never reach another member. Live device state and
commands stay with the Tuya iOS SDK: decision D4 in `002-api-architecture.md`
(the server-side command path) is still unverified, so the API must not claim to
control hardware.

## Goal

Persist which home a device belongs to, its name and zone, and each home's
scenes, with role checks on every operation.

## Non-goals

- Sending device commands or reading live state from the API (D4).
- Running scenes on the server. The app runs a scene through `LightingService`
  and reports it; the API only records `last_run_at`.
- Door lock control, which needs a verified hardware path.
- Reordering scenes or clearing a device's zone: the app has no UI for them.

## API contract

All routes require a session. A non-member gets 404 `home_not_found`.

| Operation | Minimum role | Success | Errors |
| --- | --- | --- | --- |
| `GET /v1/homes/{homeId}/devices` | member | 200 `{items: Device[]}` | 404 |
| `POST /v1/homes/{homeId}/devices` `DeviceClaim` | admin | 201 new, 200 already in this home | 400 `invalid_device`, 403, 404, 409 `device_claimed`, 409 `device_limit_reached` |
| `PATCH /v1/homes/{homeId}/devices/{deviceId}` `{name?, zone_id?}` | admin | 200 `Device` | 400 `invalid_device`, 403, 404 `device_not_found` |
| `DELETE /v1/homes/{homeId}/devices/{deviceId}` | admin | 204 | 403, 404 `device_not_found` |
| `GET /v1/homes/{homeId}/scenes` | member | 200 `{items: Scene[]}` | 404 |
| `POST /v1/homes/{homeId}/scenes` `SceneInput` | admin | 201 `Scene` | 400 `invalid_scene`, 403, 404, 409 `scene_limit_reached` |
| `PUT /v1/homes/{homeId}/scenes/{sceneId}` `SceneInput` | admin | 200 `Scene` | 400, 403, 404 `scene_not_found` |
| `DELETE /v1/homes/{homeId}/scenes/{sceneId}` | admin | 204 | 403, 404 |
| `POST /v1/homes/{homeId}/scenes/{sceneId}/runs` | member | 200 `Scene` | 404 |

```text
DeviceClaim  {provider, external_ref, kind, name?, zone_id?}
Device       {id, provider, external_ref, kind, name, zone_id}
SceneInput   {icon, preset_key, name, actions}   exactly one of preset_key, name
Scene        {id, icon, preset_key, name, actions, last_run_at}
SceneAction  {type: light, target: {kind, key?}, is_on, brightness?, temperature?}
             {type: lock_check}
```

- `provider` is `tuya` and `kind` is `light` today; both are additive enums.
- `external_ref` is the provider's device id, 1–128 characters. A device belongs
  to one home at a time, so claiming a device held by another home is 409 and
  does not reveal which home holds it.
- `zone_id` must be a zone of the same home. Names follow the home name rule.
- Scene `icon`: `moon`, `home`, `leave`, `sunrise`, `sparkle`. `preset_key`:
  `Sleep`, `Welcome home`, `Leave home`, `Wake up`.
- Target `kind`: `device` and `zone` need `key`; `others` and `all` must not
  have one. `brightness` is .01–1, `temperature` 1000–10000 kelvin.
- Limits: 100 devices and 50 scenes per home, 50 actions per scene.
- A `lock_check` action only checks. No scene action can unlock a door.

Changes to earlier contracts, all additive:

- `DELETE /v1/homes/{homeId}` gains 409 `home_has_devices` while devices remain,
  the refusal `004-homes-and-zones.md` asked this spec to add.
- `Home` gains `can_unlock`, the caller's own unlock right.
- `POST /v1/homes` also creates the four default scenes in its transaction.

## Data model

```text
javis.devices  id uuid PK, home_id FK RESTRICT, zone_id FK SET NULL, provider, external_ref,
               kind, name NULL, created_by FK users, created_at, updated_at
               UNIQUE (provider, external_ref)
javis.scenes   id uuid PK, home_id FK CASCADE, icon, preset_key NULL, name NULL,
               actions jsonb, last_run_at NULL, created_at, updated_at
               CHECK (exactly one of preset_key, name)
```

`devices.home_id` restricts deletion, so a home with devices cannot be deleted
even if application code is wrong.

## Implementation notes

- Modules `internal/device` and `internal/scene`, each with the standard shape.
  Both call `home.Service.RequireRole` through a small `Access` interface.
- `internal/home` must not import `internal/scene`, so `home.NewService` accepts
  `Seeder`s that run inside the create-home transaction; `cmd/api` wires them.
- `external_ref` is a real device identifier: it is never logged and travels only
  in JSON bodies, never in a URL.

## Acceptance criteria

- Every route is tested as non-member, member, admin and owner where roles differ.
- A device cannot be in two homes; claiming twice in one home is idempotent.
- A zone from another home is rejected.
- Invalid scene shapes are rejected and valid ones round-trip unchanged.
- A home with a device cannot be deleted; without one it deletes with its scenes.
- A new home starts with the four default scenes.

## Risks

- `external_ref` proves nothing about ownership: a signed-in admin who knows a
  device id could claim it first. Ownership proof belongs with the Tuya server
  integration (D4).
- Devices appear in the app only through the Tuya SDK session on that phone.
  Other members need Tuya device sharing, which has not been verified.

## Verification

Run on 2026-11-05:

- `gofmt -l .` no output; `go vet ./...` clean; `go test -race -count=1 ./...`
  all packages pass against PostgreSQL 17 in testcontainers.
- Mutation checks, each failing a test: claim role lowered, foreign claim
  allowed, device limit raised, home lock removed from claim, zone-in-home check
  removed, empty patch allowed, run role raised to admin, scene limit raised,
  home lock removed from create, action validation removed, preset/name
  exclusivity removed, `lock_check` extras allowed.
- Migrations 1-6 up, down to 0 and up again on a throwaway PostgreSQL 17.
- Not run: a scene against a physical bulb; the API only stores scenes.

## Current status

Implemented in `internal/device` and `internal/scene`.
