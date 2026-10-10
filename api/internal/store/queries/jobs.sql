-- name: CountJobsByStatus :many
SELECT status, COUNT(*) AS n
FROM jobs
GROUP BY status;

-- name: EnqueueJob :one
INSERT INTO jobs (kind, payload, status, run_after, created_at, updated_at)
VALUES (?, ?, 'queued', ?, ?, ?)
RETURNING *;

-- name: ClaimJob :one
UPDATE jobs
SET status = 'running', attempts = attempts + 1, updated_at = sqlc.arg(now)
WHERE id = (
  SELECT id FROM jobs
  WHERE status = 'queued' AND run_after <= sqlc.arg(now)
  ORDER BY id
  LIMIT 1
)
RETURNING *;

-- name: FinishJob :exec
UPDATE jobs SET status = 'done', last_error = NULL, updated_at = ? WHERE id = ?;

-- name: RetryJob :exec
UPDATE jobs SET status = 'queued', run_after = ?, last_error = ?, updated_at = ? WHERE id = ?;

-- name: FailJob :exec
UPDATE jobs SET status = 'failed', last_error = ?, updated_at = ? WHERE id = ?;

-- name: RequeueStaleJobs :execrows
UPDATE jobs SET status = 'queued', updated_at = sqlc.arg(now)
WHERE status = 'running' AND updated_at < sqlc.arg(stale_before);

-- name: PruneDoneJobs :execrows
DELETE FROM jobs WHERE status = 'done' AND updated_at < ?;
