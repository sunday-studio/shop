-- name: CountJobsByStatus :many
SELECT status, COUNT(*) AS n
FROM jobs
GROUP BY status;
