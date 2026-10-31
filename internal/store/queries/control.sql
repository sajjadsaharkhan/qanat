-- Queries for the control plane's Admin API.

-- name: UpsertRootUser :one
INSERT INTO users (email, name, username, role, plan_id)
SELECT 'root@qanat.local', 'Root admin', 'root', 'admin', id FROM plans WHERE is_default
ON CONFLICT (username) DO UPDATE SET role = 'admin', disabled_at = NULL, banned_at = NULL, disabled_reason = ''
RETURNING id;

-- name: GetUserByOIDCSubject :one
SELECT u.id, u.email, u.name, u.role, u.plan_id, p.name AS plan_name, u.created_at, u.disabled_at
FROM users u
JOIN plans p ON p.id = u.plan_id
WHERE u.oidc_subject = $1;

-- name: GetUserByEmail :one
SELECT u.id, u.email, u.name, u.role, u.plan_id, p.name AS plan_name, u.created_at, u.disabled_at
FROM users u
JOIN plans p ON p.id = u.plan_id
WHERE lower(u.email) = lower($1);

-- name: CreateOIDCUser :one
INSERT INTO users (email, name, oidc_subject, role, plan_id)
SELECT $1, $2, $3, $4, id FROM plans WHERE is_default
RETURNING id, role;

-- name: UpdateOIDCUser :exec
UPDATE users
SET email = sqlc.arg(email),
    name = CASE WHEN sqlc.arg(name)::text <> '' THEN sqlc.arg(name)::text ELSE users.name END,
    oidc_subject = COALESCE(oidc_subject, sqlc.narg(oidc_subject))
WHERE id = sqlc.arg(id);

-- name: GetUser :one
SELECT u.id, u.email, u.name, u.username, u.role, u.plan_id, p.name AS plan_name, u.created_at, u.disabled_at
FROM users u
JOIN plans p ON p.id = u.plan_id
WHERE u.id = $1;

-- name: ListUsers :many
SELECT u.id, u.email, u.name, u.username, u.role, u.plan_id, p.name AS plan_name, u.created_at, u.disabled_at,
       u.banned_at, u.disabled_reason,
       (SELECT count(*) FROM api_keys k WHERE k.user_id = u.id AND k.revoked_at IS NULL)::bigint AS active_keys
FROM users u
JOIN plans p ON p.id = u.plan_id
ORDER BY u.created_at;

-- name: SetUserRole :execrows
UPDATE users SET role = $2 WHERE id = $1;

-- name: SetUserPlan :execrows
UPDATE users SET plan_id = $2 WHERE id = $1;

-- name: DisableUser :execrows
UPDATE users
SET disabled_at = COALESCE(disabled_at, now()),
    banned_at = CASE WHEN sqlc.arg(ban)::boolean THEN now() ELSE banned_at END,
    disabled_reason = sqlc.arg(reason)
WHERE id = sqlc.arg(id);

-- name: EnableUser :execrows
UPDATE users SET disabled_at = NULL, banned_at = NULL, disabled_reason = '' WHERE id = $1;

-- name: RevokeUserKeys :execrows
UPDATE api_keys SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL;

-- name: ListKeys :many
SELECT id, name, prefix, expires_at, last_used_at, last_ip, revoked_at, created_at
FROM api_keys
WHERE user_id = $1
ORDER BY created_at DESC;

-- name: CreateKey :one
INSERT INTO api_keys (user_id, name, hash, prefix, expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, name, prefix, expires_at, last_used_at, last_ip, revoked_at, created_at;

-- name: RevokeKey :one
UPDATE api_keys SET revoked_at = now()
WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL
RETURNING id, name, prefix;

-- name: ListPlans :many
SELECT p.id, p.name, p.timezone, p.daily_anchor, p.weekly_anchor, p.monthly_anchor, p.is_default, p.updated_at,
       (SELECT count(*) FROM users u WHERE u.plan_id = p.id)::bigint AS user_count
FROM plans p
ORDER BY p.is_default DESC, p.name;

-- name: GetPlan :one
SELECT id, name, timezone, daily_anchor, weekly_anchor, monthly_anchor, is_default, updated_at
FROM plans
WHERE id = $1;

-- name: CreatePlan :one
INSERT INTO plans (name, timezone, daily_anchor, weekly_anchor, monthly_anchor)
VALUES ($1, $2, $3, $4, $5)
RETURNING id;

-- name: UpdatePlan :execrows
UPDATE plans
SET name = $2, timezone = $3, daily_anchor = $4, weekly_anchor = $5, monthly_anchor = $6, updated_at = now()
WHERE id = $1;

-- name: DeletePlan :execrows
DELETE FROM plans p
WHERE p.id = $1 AND NOT p.is_default AND NOT EXISTS (SELECT 1 FROM users u WHERE u.plan_id = p.id);

-- name: ListPlanQuotas :many
SELECT window_kind, model_scope, limit_usd
FROM plan_quotas
WHERE plan_id = $1
ORDER BY CASE window_kind WHEN 'rolling_5h' THEN 0 WHEN 'daily' THEN 1 WHEN 'weekly' THEN 2 ELSE 3 END, model_scope NULLS FIRST;

-- name: DeletePlanQuotas :exec
DELETE FROM plan_quotas WHERE plan_id = $1;

-- name: InsertPlanQuota :exec
INSERT INTO plan_quotas (plan_id, window_kind, model_scope, limit_usd) VALUES ($1, $2, $3, $4);

-- name: GetPlanLimits :one
SELECT rpm, max_concurrent FROM plan_limits WHERE plan_id = $1;

-- name: UpsertPlanLimits :exec
INSERT INTO plan_limits (plan_id, rpm, max_concurrent) VALUES ($1, $2, $3)
ON CONFLICT (plan_id) DO UPDATE SET rpm = EXCLUDED.rpm, max_concurrent = EXCLUDED.max_concurrent;

-- name: ListPlanModels :many
SELECT model_name FROM plan_models WHERE plan_id = $1 ORDER BY model_name;

-- name: DeletePlanModels :exec
DELETE FROM plan_models WHERE plan_id = $1;

-- name: InsertPlanModel :exec
INSERT INTO plan_models (plan_id, model_name) VALUES ($1, $2);

-- name: InsertAudit :exec
INSERT INTO audit_log (actor_id, action, entity, entity_id, before, after)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ListAudit :many
SELECT a.id, a.actor_id, coalesce(u.email, '') AS actor_email, a.action, a.entity, a.entity_id, a.before, a.after, a.at
FROM audit_log a
LEFT JOIN users u ON u.id = a.actor_id
WHERE sqlc.narg('before_id')::bigint IS NULL OR a.id < sqlc.narg('before_id')::bigint
ORDER BY a.id DESC
LIMIT sqlc.arg('row_limit');
