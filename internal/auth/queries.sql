-- name: LockPhone :exec
-- Serializes code requests for one number until the transaction ends.
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(phone)::text, 0));

-- name: SecondsSinceLastChallenge :one
-- A number with no challenge yet reports a day, longer than any resend window.
SELECT COALESCE(EXTRACT(EPOCH FROM now() - max(created_at)), 86400)::double precision
FROM javis.otp_challenges
WHERE phone = $1;

-- name: CountRecentChallengesByPhone :one
SELECT count(*) FROM javis.otp_challenges
WHERE phone = $1 AND created_at > now() - interval '1 hour';

-- name: CountRecentChallengesByIP :one
SELECT count(*) FROM javis.otp_challenges
WHERE client_ip = $1 AND created_at > now() - interval '1 hour';

-- name: ConsumeOpenChallenges :exec
UPDATE javis.otp_challenges SET consumed_at = now()
WHERE phone = $1 AND consumed_at IS NULL;

-- name: CreateChallenge :exec
INSERT INTO javis.otp_challenges (id, phone, client_ip, code_hash, expires_at)
VALUES ($1, $2, $3, $4, now() + make_interval(secs => sqlc.arg(ttl_seconds)::double precision));

-- name: DeleteChallenge :exec
DELETE FROM javis.otp_challenges WHERE id = $1;

-- name: LockActiveChallenge :one
-- The row lock makes parallel guesses count one at a time.
SELECT id, code_hash, attempts FROM javis.otp_challenges
WHERE phone = $1 AND consumed_at IS NULL AND expires_at > now()
ORDER BY created_at DESC
LIMIT 1
FOR UPDATE;

-- name: RecordFailedAttempt :exec
UPDATE javis.otp_challenges SET attempts = attempts + 1 WHERE id = $1;

-- name: ConsumeChallenge :exec
UPDATE javis.otp_challenges SET attempts = attempts + 1, consumed_at = now() WHERE id = $1;

-- name: UpsertUserByPhone :one
INSERT INTO javis.users (id, phone) VALUES ($1, $2)
ON CONFLICT (phone) DO UPDATE SET phone = EXCLUDED.phone
RETURNING id, phone, name;

-- name: GetUser :one
SELECT id, phone, name FROM javis.users WHERE id = $1;

-- name: UpdateUserName :one
UPDATE javis.users SET name = $2, updated_at = now() WHERE id = $1
RETURNING id, phone, name;

-- name: CreateSession :one
INSERT INTO javis.sessions (id, user_id, token_hash, expires_at)
VALUES ($1, $2, $3, now() + make_interval(secs => sqlc.arg(ttl_seconds)::double precision))
RETURNING expires_at;

-- name: GetActiveSession :one
SELECT id, user_id, EXTRACT(EPOCH FROM now() - last_used_at)::double precision AS idle_seconds
FROM javis.sessions
WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now();

-- name: ExtendSession :exec
UPDATE javis.sessions
SET last_used_at = now(),
    expires_at = now() + make_interval(secs => sqlc.arg(ttl_seconds)::double precision)
WHERE id = $1;

-- name: RevokeSession :exec
UPDATE javis.sessions SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL;

-- name: PurgeOldChallenges :exec
DELETE FROM javis.otp_challenges WHERE created_at < now() - interval '1 day';

-- name: PurgeDeadSessions :exec
DELETE FROM javis.sessions
WHERE expires_at < now() OR revoked_at < now() - interval '1 day';
