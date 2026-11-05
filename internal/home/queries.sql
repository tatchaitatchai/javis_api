-- name: LockOwnerHomes :exec
-- Serializes home creation per owner so parallel requests respect the limit.
SELECT pg_advisory_xact_lock(hashtextextended('homes:' || sqlc.arg(user_id)::text, 0));

-- name: CountOwnedHomes :one
SELECT count(*) FROM javis.home_members WHERE user_id = $1 AND role = 'owner';

-- name: CreateHome :exec
INSERT INTO javis.homes (id, name, created_by) VALUES ($1, $2, $3);

-- name: AddOwner :exec
INSERT INTO javis.home_members (home_id, user_id, role, can_unlock)
VALUES ($1, $2, 'owner', true);

-- name: CreateZone :one
INSERT INTO javis.zones (id, home_id, preset_key, name, position)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, preset_key, name;

-- name: ListHomes :many
SELECT h.id, h.name, m.role,
    (SELECT count(*) FROM javis.zones z WHERE z.home_id = h.id) AS zone_count,
    (SELECT count(*) FROM javis.home_members o WHERE o.home_id = h.id) AS member_count
FROM javis.home_members m
JOIN javis.homes h ON h.id = m.home_id
WHERE m.user_id = $1
ORDER BY m.created_at, h.id;

-- name: GetHome :one
SELECT h.id, h.name, m.role
FROM javis.home_members m
JOIN javis.homes h ON h.id = m.home_id
WHERE m.user_id = $1 AND m.home_id = $2;

-- name: ListZones :many
SELECT id, preset_key, name FROM javis.zones
WHERE home_id = $1
ORDER BY position, id;

-- name: GetRole :one
SELECT role FROM javis.home_members WHERE home_id = $1 AND user_id = $2;

-- name: RenameHome :exec
UPDATE javis.homes SET name = $2, updated_at = now() WHERE id = $1;

-- name: DeleteHome :exec
DELETE FROM javis.homes WHERE id = $1;

-- name: LockHome :exec
-- Serializes zone creation per home so parallel requests respect the limit.
SELECT id FROM javis.homes WHERE id = $1 FOR UPDATE;

-- name: ZoneStats :one
SELECT count(*) AS zone_count, COALESCE(max(position) + 1, 0)::integer AS next_position
FROM javis.zones
WHERE home_id = $1;
