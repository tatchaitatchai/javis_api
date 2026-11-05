-- name: ListScenes :many
SELECT id, icon, preset_key, name, actions, last_run_at
FROM javis.scenes
WHERE home_id = $1
ORDER BY created_at, id;

-- name: LockHome :exec
-- Serializes scene creation per home so parallel requests respect the limit.
SELECT id FROM javis.homes WHERE id = $1 FOR UPDATE;

-- name: CountScenes :one
SELECT count(*) FROM javis.scenes WHERE home_id = $1;

-- name: CreateScene :one
INSERT INTO javis.scenes (id, home_id, icon, preset_key, name, actions)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, icon, preset_key, name, actions, last_run_at;

-- name: UpdateScene :one
UPDATE javis.scenes
SET icon = $3, preset_key = $4, name = $5, actions = $6, updated_at = now()
WHERE id = $1 AND home_id = $2
RETURNING id, icon, preset_key, name, actions, last_run_at;

-- name: DeleteScene :execrows
DELETE FROM javis.scenes WHERE id = $1 AND home_id = $2;

-- name: MarkSceneRun :one
UPDATE javis.scenes SET last_run_at = now()
WHERE id = $1 AND home_id = $2
RETURNING id, icon, preset_key, name, actions, last_run_at;
