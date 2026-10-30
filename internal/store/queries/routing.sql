-- Admin API: provider accounts, model mapping, prices (PRD §6, §10.1).

-- name: ListAccounts :many
SELECT id, provider, label, base_url, (credentials IS NOT NULL)::boolean AS has_credentials, state, cooldown_until, created_at,
       auth_kind, expires_at, disabled_at, last_check_at, last_check_ok, last_check_error, position,
       email, last_refresh_at, last_refresh_error,
       proxy_id, proxy_group_id, proxy_direct
FROM provider_accounts
ORDER BY position;

-- name: GetAccountAdmin :one
SELECT id, provider, label, base_url, credentials, state, cooldown_until,
       auth_kind, expires_at, disabled_at, proxy_id, proxy_group_id, proxy_direct,
       last_refresh_at
FROM provider_accounts
WHERE id = $1;

-- name: CreateAccount :exec
INSERT INTO provider_accounts (id, provider, label, base_url, credentials, auth_kind, expires_at, email, position, proxy_id, proxy_group_id, proxy_direct)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, (SELECT coalesce(max(position), 0) + 1 FROM provider_accounts), $9, $10, $11);

-- OAuth sign-in dedup (like 9router's connection merge): a re-sign-in with the
-- same provider identity refreshes the existing account instead of adding one.
-- name: FindOAuthAccount :many
SELECT id FROM provider_accounts
WHERE provider = $1 AND email = $2 AND email <> '' AND auth_kind = 'oauth'
ORDER BY position
LIMIT 1;

-- name: MergeOAuthAccount :execrows
UPDATE provider_accounts
SET credentials = $2, expires_at = $3, auth_kind = 'oauth', email = $4,
    state = 'healthy', cooldown_until = NULL, disabled_at = NULL
WHERE id = $1;

-- OAuth token refresh (REQ-OAUTH-03): accounts whose access token expires
-- before $1. Accounts a failed refresh disconnected wait for a new sign-in.
-- name: DueOAuthAccounts :many
SELECT id FROM provider_accounts
WHERE auth_kind = 'oauth' AND credentials IS NOT NULL AND disabled_at IS NULL
  AND state <> 'disconnected' AND expires_at IS NOT NULL AND expires_at < $1
ORDER BY expires_at;

-- name: SetOAuthRefreshed :execrows
UPDATE provider_accounts
SET credentials = $2, expires_at = $3, last_refresh_at = $4, last_refresh_error = NULL,
    state = CASE WHEN state = 'disconnected' THEN 'healthy' ELSE state END
WHERE id = $1 AND auth_kind = 'oauth';

-- name: SetOAuthRefreshFailed :exec
UPDATE provider_accounts
SET last_refresh_at = $2, last_refresh_error = $3,
    state = CASE WHEN sqlc.arg(disconnect)::boolean THEN 'disconnected' ELSE state END
WHERE id = $1;

-- name: SetAccountProxy :execrows
UPDATE provider_accounts SET proxy_id = $2, proxy_group_id = $3, proxy_direct = $4 WHERE id = $1;

-- name: UpdateAccountCore :execrows
UPDATE provider_accounts
SET label = $2, base_url = $3, auth_kind = $4, expires_at = $5, credentials = $6, disabled_at = $7
WHERE id = $1;

-- name: SetAccountCheckResult :exec
UPDATE provider_accounts SET last_check_at = $2, last_check_ok = $3, last_check_error = $4 WHERE id = $1;

-- name: AccountPositions :many
SELECT id, position FROM provider_accounts WHERE provider = $1 ORDER BY position;

-- name: SetAccountPosition :execrows
UPDATE provider_accounts SET position = $2 WHERE id = $1;

-- name: DeleteAccount :execrows
DELETE FROM provider_accounts WHERE id = $1;

-- Stored provider catalogs (migration 0009), refreshed wholesale per provider.
-- name: ListProviderModels :many
SELECT model FROM provider_models WHERE provider = $1 ORDER BY model;

-- name: ReplaceProviderModels :exec
DELETE FROM provider_models WHERE provider = $1;

-- name: InsertProviderModel :exec
INSERT INTO provider_models (provider, model) VALUES ($1, $2) ON CONFLICT DO NOTHING;

-- name: ListMappings :many
SELECT pattern, is_glob, target_kind, provider, upstream_model, priority
FROM model_mappings
ORDER BY is_glob, priority, pattern;

-- name: DeleteMappings :exec
DELETE FROM model_mappings;

-- name: InsertMapping :exec
INSERT INTO model_mappings (pattern, is_glob, target_kind, provider, upstream_model, priority)
VALUES ($1, $2, 'model', $3, $4, $5);

-- Newest price row per model, confirmed or proposed; admin rows first, as
-- in SnapshotPrices. has_default says whether deleting the admin rows falls
-- back to a built-in price.
-- name: ListPrices :many
SELECT DISTINCT ON (p.model_id)
       p.id, p.model_id, p.p_in, p.p_out, p.p_cache_read, p.p_cache_write, p.effective_from, p.confirmed_at, p.source,
       EXISTS (SELECT 1 FROM model_prices d WHERE d.model_id = p.model_id AND d.source = 'default') AS has_default
FROM model_prices p
ORDER BY p.model_id, (p.source = 'admin') DESC, p.effective_from DESC, p.id DESC;

-- name: InsertConfirmedPrice :one
INSERT INTO model_prices (model_id, p_in, p_out, p_cache_read, p_cache_write, confirmed_by, confirmed_at)
VALUES ($1, $2, $3, $4, $5, $6, now())
RETURNING id;

-- Removes every admin version of a model's price. Usage aggregates keep
-- their stored cost and price_version, so past cost is unchanged.
-- A price can be set only for a model a provider's catalog lists, or one
-- that already has a price (a built-in default or an earlier admin price).
-- name: PriceableModel :one
SELECT (EXISTS (SELECT 1 FROM provider_models WHERE model = $1)
    OR EXISTS (SELECT 1 FROM model_prices WHERE model_id = $1))::boolean AS ok;

-- name: DeleteAdminPrices :execrows
DELETE FROM model_prices WHERE model_id = $1 AND source = 'admin';

-- name: LatestDefaultPrices :many
SELECT DISTINCT ON (model_id)
       model_id, p_in, p_out, p_cache_read, p_cache_write
FROM model_prices
WHERE source = 'default'
ORDER BY model_id, effective_from DESC, id DESC;

-- name: InsertDefaultPrice :exec
INSERT INTO model_prices (model_id, p_in, p_out, p_cache_read, p_cache_write, source, confirmed_at)
VALUES ($1, $2, $3, $4, $5, 'default', now());

-- name: UserUsageTotals :one
SELECT coalesce(sum(input_tokens), 0)::bigint       AS input_tokens,
       coalesce(sum(output_tokens), 0)::bigint      AS output_tokens,
       coalesce(sum(cache_read_tokens), 0)::bigint  AS cache_read_tokens,
       coalesce(sum(cache_write_tokens), 0)::bigint AS cache_write_tokens,
       coalesce(sum(cost_usd), 0)::numeric          AS cost_usd,
       coalesce(sum(request_count), 0)::bigint      AS request_count
FROM usage_daily
WHERE user_id = $1;

-- Aggregation worker (D17, D18): one upsert per level.
-- name: UpsertUsage5m :exec
INSERT INTO usage_5m (bucket_start, user_id, key_id, provider, upstream_model, account_id, aborted, price_version,
                      input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd, request_count)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 1)
ON CONFLICT (bucket_start, user_id, key_id, provider, upstream_model, account_id, aborted, price_version) DO UPDATE SET
    input_tokens = usage_5m.input_tokens + EXCLUDED.input_tokens,
    output_tokens = usage_5m.output_tokens + EXCLUDED.output_tokens,
    cache_read_tokens = usage_5m.cache_read_tokens + EXCLUDED.cache_read_tokens,
    cache_write_tokens = usage_5m.cache_write_tokens + EXCLUDED.cache_write_tokens,
    cost_usd = usage_5m.cost_usd + EXCLUDED.cost_usd,
    request_count = usage_5m.request_count + 1;

-- name: UpsertUsageHourly :exec
INSERT INTO usage_hourly (bucket_start, user_id, key_id, provider, upstream_model, account_id, aborted, price_version,
                          input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd, request_count)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 1)
ON CONFLICT (bucket_start, user_id, key_id, provider, upstream_model, account_id, aborted, price_version) DO UPDATE SET
    input_tokens = usage_hourly.input_tokens + EXCLUDED.input_tokens,
    output_tokens = usage_hourly.output_tokens + EXCLUDED.output_tokens,
    cache_read_tokens = usage_hourly.cache_read_tokens + EXCLUDED.cache_read_tokens,
    cache_write_tokens = usage_hourly.cache_write_tokens + EXCLUDED.cache_write_tokens,
    cost_usd = usage_hourly.cost_usd + EXCLUDED.cost_usd,
    request_count = usage_hourly.request_count + 1;

-- name: UpsertUsageDaily :exec
INSERT INTO usage_daily (bucket_start, user_id, key_id, provider, upstream_model, account_id, aborted, price_version,
                         input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd, request_count)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 1)
ON CONFLICT (bucket_start, user_id, key_id, provider, upstream_model, account_id, aborted, price_version) DO UPDATE SET
    input_tokens = usage_daily.input_tokens + EXCLUDED.input_tokens,
    output_tokens = usage_daily.output_tokens + EXCLUDED.output_tokens,
    cache_read_tokens = usage_daily.cache_read_tokens + EXCLUDED.cache_read_tokens,
    cache_write_tokens = usage_daily.cache_write_tokens + EXCLUDED.cache_write_tokens,
    cost_usd = usage_daily.cost_usd + EXCLUDED.cost_usd,
    request_count = usage_daily.request_count + 1;

-- name: TouchKey :exec
UPDATE api_keys SET last_used_at = $2, last_ip = $3
WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < $2);

-- name: DeleteOldUsage5m :execrows
DELETE FROM usage_5m WHERE bucket_start < now() - interval '7 days';

-- name: ListProviderRouting :many
SELECT provider, strategy, sticky_limit, min_headroom_pct FROM provider_routing ORDER BY provider;

-- name: SetProviderRouting :exec
INSERT INTO provider_routing (provider, strategy, sticky_limit, min_headroom_pct)
VALUES ($1, $2, $3, $4)
ON CONFLICT (provider) DO UPDATE
SET strategy = EXCLUDED.strategy, sticky_limit = EXCLUDED.sticky_limit, min_headroom_pct = EXCLUDED.min_headroom_pct;
