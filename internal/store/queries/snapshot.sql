-- Queries the gateway runs to build its in-memory config snapshot (§11.6).
-- Each runs on load and on every qanat_config notification; never per request.

-- name: SnapshotKeys :many
SELECT k.id, k.user_id, k.hash, k.allowed_models, k.expires_at
FROM api_keys k
JOIN users u ON u.id = k.user_id
WHERE k.revoked_at IS NULL AND u.disabled_at IS NULL;

-- name: SnapshotUsers :many
SELECT id, plan_id, role
FROM users
WHERE disabled_at IS NULL;

-- name: SnapshotPlans :many
SELECT id, timezone, daily_anchor, weekly_anchor, monthly_anchor
FROM plans;

-- name: SnapshotPlanQuotas :many
SELECT plan_id, window_kind, model_scope, limit_usd
FROM plan_quotas;

-- name: SnapshotPlanLimits :many
SELECT plan_id, rpm, max_concurrent
FROM plan_limits;

-- name: SnapshotPlanModels :many
SELECT plan_id, model_name
FROM plan_models;

-- Active top-ups per user; they raise every window's limit (§10.3).
-- name: SnapshotGrants :many
SELECT user_id, sum(amount_usd)::numeric AS amount_usd, min(expires_at)::timestamptz AS next_expiry
FROM quota_grants
WHERE expires_at > now()
GROUP BY user_id;

-- name: SnapshotMappings :many
SELECT pattern, is_glob, target_kind, provider, upstream_model, combo_id, priority
FROM model_mappings
ORDER BY priority, pattern;

-- name: SnapshotCombos :many
SELECT c.id, c.name, m.position, m.provider, m.upstream_model
FROM combos c
JOIN combo_members m ON m.combo_id = c.id
ORDER BY c.id, m.position;

-- Latest confirmed price per model that is already in effect. A model whose
-- newest row is unconfirmed keeps its last confirmed price; a model with no
-- confirmed price at all is absent, which makes it unusable (D13).
-- name: SnapshotPrices :many
SELECT DISTINCT ON (upstream_model)
       id, upstream_model, p_in, p_out, p_cache_read, p_cache_write
FROM model_prices
WHERE confirmed_at IS NOT NULL AND effective_from <= now()
ORDER BY upstream_model, effective_from DESC, id DESC;

-- name: SnapshotAccounts :many
SELECT id, provider, base_url, credentials, state, cooldown_until, auth_kind, disabled_at, proxy_id, proxy_group_id, proxy_direct
FROM provider_accounts
ORDER BY position;

-- name: SnapshotStickyBindings :many
SELECT user_id, provider, account_id
FROM sticky_bindings;

-- name: SnapshotGeneration :one
SELECT gen FROM config_generation;

-- name: UpsertStickyBinding :exec
INSERT INTO sticky_bindings (user_id, provider, account_id) VALUES ($1, $2, $3)
ON CONFLICT (user_id, provider) DO UPDATE SET account_id = EXCLUDED.account_id, updated_at = now();

-- name: SnapshotProxies :many
SELECT id, type, host, port, username, password, enabled, is_default
FROM proxies;

-- name: SnapshotProxyGroups :many
SELECT g.id, g.policy, m.proxy_id
FROM proxy_groups g
JOIN proxy_group_members m ON m.group_id = g.id
ORDER BY g.id, m.priority, m.proxy_id;

-- name: SnapshotProviderProxies :many
SELECT provider, proxy_id, proxy_group_id, proxy_direct
FROM provider_proxies;
