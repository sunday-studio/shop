-- name: CreateToken :one
INSERT INTO api_tokens (name, token_hash, created_at) VALUES (?, ?, ?)
RETURNING *;

-- name: GetTokenByHash :one
SELECT * FROM api_tokens WHERE token_hash = ?;

-- name: TouchToken :exec
UPDATE api_tokens SET last_used_at = ? WHERE id = ?;

-- name: ListTokens :many
SELECT * FROM api_tokens ORDER BY name;

-- name: DeleteToken :execrows
DELETE FROM api_tokens WHERE name = ?;
