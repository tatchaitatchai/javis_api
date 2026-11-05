-- name: ListDevices :many
SELECT id, provider, external_ref, kind, name, zone_id
FROM javis.devices
WHERE home_id = $1
ORDER BY created_at, id;

-- name: LockHome :exec
-- Serializes claims per home so parallel requests respect the limit.
SELECT id FROM javis.homes WHERE id = $1 FOR UPDATE;

-- name: CountDevices :one
SELECT count(*) FROM javis.devices WHERE home_id = $1;

-- name: ZoneInHome :one
SELECT EXISTS (SELECT 1 FROM javis.zones WHERE id = $1 AND home_id = $2);

-- name: ClaimDevice :one
INSERT INTO javis.devices (id, home_id, zone_id, provider, external_ref, kind, name, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (provider, external_ref) DO NOTHING
RETURNING id, provider, external_ref, kind, name, zone_id;

-- name: GetClaim :one
SELECT home_id, id, provider, external_ref, kind, name, zone_id
FROM javis.devices
WHERE provider = $1 AND external_ref = $2;

-- name: UpdateDevice :one
UPDATE javis.devices
SET name = COALESCE(sqlc.narg(name), name),
    zone_id = COALESCE(sqlc.narg(zone_id), zone_id),
    updated_at = now()
WHERE id = sqlc.arg(id) AND home_id = sqlc.arg(home_id)
RETURNING id, provider, external_ref, kind, name, zone_id;

-- name: DeleteDevice :execrows
DELETE FROM javis.devices WHERE id = $1 AND home_id = $2;
