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

-- name: GetUserPhone :one
SELECT phone FROM javis.users WHERE id = $1;

-- name: ListMembers :many
SELECT m.user_id, u.name, m.role, m.can_unlock
FROM javis.home_members m
JOIN javis.users u ON u.id = m.user_id
WHERE m.home_id = $1
ORDER BY m.created_at, m.user_id;

-- name: GetMember :one
SELECT m.user_id, u.name, m.role, m.can_unlock
FROM javis.home_members m
JOIN javis.users u ON u.id = m.user_id
WHERE m.home_id = $1 AND m.user_id = $2;

-- name: AddMember :exec
-- An existing member keeps their role when accepting another invitation.
INSERT INTO javis.home_members (home_id, user_id, role, can_unlock)
VALUES ($1, $2, $3, $4)
ON CONFLICT (home_id, user_id) DO NOTHING;

-- name: UpdateMember :exec
UPDATE javis.home_members
SET role = COALESCE(sqlc.narg(role), role),
    can_unlock = COALESCE(sqlc.narg(can_unlock), can_unlock)
WHERE home_id = sqlc.arg(home_id) AND user_id = sqlc.arg(user_id);

-- name: RemoveMember :exec
DELETE FROM javis.home_members WHERE home_id = $1 AND user_id = $2;

-- name: PhoneIsMember :one
SELECT EXISTS (
    SELECT 1 FROM javis.home_members m
    JOIN javis.users u ON u.id = m.user_id
    WHERE m.home_id = $1 AND u.phone = $2
);

-- name: CountSeats :one
-- Members plus open invitations, which hold a place until they are answered.
SELECT (SELECT count(*) FROM javis.home_members m WHERE m.home_id = $1)
    + (SELECT count(*) FROM javis.home_invites i
       WHERE i.home_id = $1 AND i.status = 'pending' AND i.expires_at > now());

-- name: ExpirePendingInvite :exec
-- Frees the one-pending-invitation index for a number whose invitation lapsed.
UPDATE javis.home_invites
SET status = 'expired', responded_at = now()
WHERE home_id = $1 AND phone = $2 AND status = 'pending' AND expires_at <= now();

-- name: CreateInvite :one
INSERT INTO javis.home_invites (id, home_id, phone, role, can_unlock, invited_by, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, now() + make_interval(days => sqlc.arg(valid_days)::integer))
ON CONFLICT (home_id, phone) WHERE status = 'pending' DO NOTHING
RETURNING id, phone, role, can_unlock, expires_at;

-- name: ListOpenInvites :many
SELECT id, phone, role, can_unlock, expires_at
FROM javis.home_invites
WHERE home_id = $1 AND status = 'pending' AND expires_at > now()
ORDER BY created_at, id;

-- name: RevokeInvite :execrows
UPDATE javis.home_invites
SET status = 'revoked', responded_at = now()
WHERE id = $1 AND home_id = $2 AND status = 'pending' AND expires_at > now();

-- name: ListReceivedInvites :many
SELECT i.id, h.name AS home_name, u.name AS invited_by_name, i.role, i.expires_at,
    (SELECT count(*) FROM javis.zones z WHERE z.home_id = i.home_id) AS zone_count
FROM javis.home_invites i
JOIN javis.homes h ON h.id = i.home_id
JOIN javis.users u ON u.id = i.invited_by
WHERE i.phone = $1 AND i.status = 'pending' AND i.expires_at > now()
ORDER BY i.created_at, i.id;

-- name: GetOpenInviteHome :one
SELECT home_id FROM javis.home_invites
WHERE id = $1 AND phone = $2 AND status = 'pending' AND expires_at > now();

-- name: LockOpenInvite :one
SELECT role, can_unlock FROM javis.home_invites
WHERE id = $1 AND phone = $2 AND status = 'pending' AND expires_at > now()
FOR UPDATE;

-- name: MarkInviteAccepted :exec
UPDATE javis.home_invites SET status = 'accepted', responded_at = now() WHERE id = $1;

-- name: DeclineInvite :execrows
UPDATE javis.home_invites
SET status = 'declined', responded_at = now()
WHERE id = $1 AND phone = $2 AND status = 'pending' AND expires_at > now();
