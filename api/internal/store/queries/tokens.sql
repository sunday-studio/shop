-- name: CreateToken :one
INSERT INTO api_tokens (name, token_hash, kind, created_at) VALUES (?, ?, ?, ?)
RETURNING *;

-- name: TokenNameExists :one
SELECT COUNT(*) FROM api_tokens WHERE name = ?;

-- name: ListDeviceTokens :many
SELECT * FROM api_tokens WHERE kind = 'device' ORDER BY created_at;

-- name: GetToken :one
SELECT * FROM api_tokens WHERE id = ?;

-- name: DeleteTokenByID :execrows
DELETE FROM api_tokens WHERE id = ?;

-- name: DeleteDeviceTokens :execrows
DELETE FROM api_tokens WHERE kind = 'device';

-- name: GetTokenByHash :one
SELECT * FROM api_tokens WHERE token_hash = ?;

-- name: TouchToken :exec
UPDATE api_tokens SET last_used_at = ? WHERE id = ?;

-- name: ListTokens :many
SELECT * FROM api_tokens ORDER BY name;

-- name: DeleteToken :execrows
DELETE FROM api_tokens WHERE name = ?;
