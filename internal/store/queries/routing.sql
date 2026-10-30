-- Admin API: provider accounts, model mapping, prices (PRD §6, §10.1).

-- name: ListAccounts :many
SELECT id, provider, label, base_url, (credentials IS NOT NULL)::boolean AS has_credentials, state, cooldown_until, created_at,
       auth_kind, expires_at, disabled_at, last_check_at, last_check_ok, last_check_error, position,
       proxy_id, proxy_group_id, proxy_direct
FROM provider_accounts
ORDER BY position;

-- name: GetAccountAdmin :one
SELECT id, provider, label, base_url, credentials, state, cooldown_until,
       auth_kind, expires_at, disabled_at, proxy_id, proxy_group_id, proxy_direct
FROM provider_accounts
WHERE id = $1;

-- name: CreateAccount :exec
INSERT INTO provider_accounts (id, provider, label, base_url, credentials, auth_kind, expires_at, position, proxy_id, proxy_group_id, proxy_direct)
VALUES ($1, $2, $3, $4, $5, $6, $7, (SELECT coalesce(max(position), 0) + 1 FROM provider_accounts), $8, $9, $10);

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

-- name: ListMappings :many
SELECT pattern, is_glob, target_kind, provider, upstream_model, priority
FROM model_mappings
ORDER BY is_glob, priority, pattern;

-- name: DeleteMappings :exec
DELETE FROM model_mappings;

-- name: InsertMapping :exec
INSERT INTO model_mappings (pattern, is_glob, target_kind, provider, upstream_model, priority)
VALUES ($1, $2, 'model', $3, $4, $5);

-- Newest price row per model, confirmed or proposed.
-- name: ListPrices :many
SELECT DISTINCT ON (upstream_model)
       id, upstream_model, p_in, p_out, p_cache_read, p_cache_write, effective_from, confirmed_at
FROM model_prices
ORDER BY upstream_model, effective_from DESC, id DESC;

-- name: InsertConfirmedPrice :one
INSERT INTO model_prices (upstream_model, p_in, p_out, p_cache_read, p_cache_write, confirmed_by, confirmed_at)
VALUES ($1, $2, $3, $4, $5, $6, now())
RETURNING id;

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
