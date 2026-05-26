-- name: CreateFeishuPendingRegistration :one
INSERT INTO feishu_pending_registration (
    session_token,
    open_id,
    union_id,
    tenant_key,
    name,
    avatar_url,
    raw_profile,
    expires_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
RETURNING *;

-- name: GetFeishuPendingRegistration :one
SELECT * FROM feishu_pending_registration
WHERE session_token = $1
  AND expires_at > now();

-- name: DeleteFeishuPendingRegistration :exec
DELETE FROM feishu_pending_registration
WHERE id = $1;

-- name: DeleteExpiredFeishuPendingRegistrations :exec
DELETE FROM feishu_pending_registration
WHERE expires_at < now();
