-- Usage Overview (9router layout): one row per provider, model, key and
-- account, and chart buckets. Prompt tokens include cache reads and writes;
-- cached tokens are the cache reads among them.

-- name: UsageGroupsHourly :many
SELECT d.provider, d.upstream_model, d.key_id, d.account_id,
       coalesce(k.name, '')::text AS key_name, coalesce(k.prefix, '')::text AS key_prefix,
       coalesce(u.email, '')::text AS user_email, coalesce(a.label, '')::text AS account_label,
       sum(d.input_tokens + d.cache_read_tokens + d.cache_write_tokens)::bigint AS prompt_tokens,
       sum(d.cache_read_tokens)::bigint  AS cached_tokens,
       sum(d.output_tokens)::bigint      AS completion_tokens,
       sum(d.cost_usd)::numeric          AS cost_usd,
       sum(d.request_count)::bigint      AS requests,
       max(d.bucket_start)::timestamptz AS last_bucket
FROM usage_hourly d
LEFT JOIN api_keys k ON k.id = d.key_id
LEFT JOIN users u ON u.id = d.user_id
LEFT JOIN provider_accounts a ON a.id::text = d.account_id
WHERE (sqlc.narg('user_id')::uuid IS NULL OR d.user_id = sqlc.narg('user_id')::uuid)
  AND d.bucket_start >= sqlc.arg('since')
GROUP BY d.provider, d.upstream_model, d.key_id, d.account_id, k.name, k.prefix, u.email, a.label;

-- name: UsageChartHourly :many
SELECT d.bucket_start,
       sum(d.input_tokens + d.cache_read_tokens + d.cache_write_tokens + d.output_tokens)::bigint AS tokens,
       sum(d.cost_usd)::numeric     AS cost_usd,
       sum(d.request_count)::bigint AS requests
FROM usage_hourly d
WHERE (sqlc.narg('user_id')::uuid IS NULL OR d.user_id = sqlc.narg('user_id')::uuid)
  AND d.bucket_start >= sqlc.arg('since')
GROUP BY d.bucket_start
ORDER BY d.bucket_start;

-- name: UsageGroupsDaily :many
SELECT d.provider, d.upstream_model, d.key_id, d.account_id,
       coalesce(k.name, '')::text AS key_name, coalesce(k.prefix, '')::text AS key_prefix,
       coalesce(u.email, '')::text AS user_email, coalesce(a.label, '')::text AS account_label,
       sum(d.input_tokens + d.cache_read_tokens + d.cache_write_tokens)::bigint AS prompt_tokens,
       sum(d.cache_read_tokens)::bigint  AS cached_tokens,
       sum(d.output_tokens)::bigint      AS completion_tokens,
       sum(d.cost_usd)::numeric          AS cost_usd,
       sum(d.request_count)::bigint      AS requests,
       max(d.bucket_start)::timestamptz AS last_bucket
FROM usage_daily d
LEFT JOIN api_keys k ON k.id = d.key_id
LEFT JOIN users u ON u.id = d.user_id
LEFT JOIN provider_accounts a ON a.id::text = d.account_id
WHERE (sqlc.narg('user_id')::uuid IS NULL OR d.user_id = sqlc.narg('user_id')::uuid)
  AND d.bucket_start >= sqlc.arg('since')
GROUP BY d.provider, d.upstream_model, d.key_id, d.account_id, k.name, k.prefix, u.email, a.label;

-- name: UsageChartDaily :many
SELECT d.bucket_start,
       sum(d.input_tokens + d.cache_read_tokens + d.cache_write_tokens + d.output_tokens)::bigint AS tokens,
       sum(d.cost_usd)::numeric     AS cost_usd,
       sum(d.request_count)::bigint AS requests
FROM usage_daily d
WHERE (sqlc.narg('user_id')::uuid IS NULL OR d.user_id = sqlc.narg('user_id')::uuid)
  AND d.bucket_start >= sqlc.arg('since')
GROUP BY d.bucket_start
ORDER BY d.bucket_start;

-- Precise "last used" from the 5-minute buckets (kept 7 days).
-- name: UsageLastUsed5m :many
SELECT d.provider, d.upstream_model, d.key_id, d.account_id, max(d.bucket_start)::timestamptz AS last_bucket
FROM usage_5m d
WHERE (sqlc.narg('user_id')::uuid IS NULL OR d.user_id = sqlc.narg('user_id')::uuid)
  AND d.bucket_start >= sqlc.arg('since')
GROUP BY d.provider, d.upstream_model, d.key_id, d.account_id;

-- name: UsageFirstDay :one
SELECT coalesce(min(d.bucket_start), now())::timestamptz AS first_day
FROM usage_daily d
WHERE sqlc.narg('user_id')::uuid IS NULL OR d.user_id = sqlc.narg('user_id')::uuid;

-- name: ProviderAccountsInPool :many
SELECT DISTINCT provider FROM provider_accounts ORDER BY provider;
