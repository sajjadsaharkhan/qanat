-- Usage reports for the dashboard (PRD §13.3 "Usage"), from the daily
-- aggregates. A NULL user_id means every user (admin views).

-- name: UsageDailyByModel :many
SELECT d.bucket_start, d.provider, d.upstream_model,
       sum(d.input_tokens)::bigint       AS input_tokens,
       sum(d.output_tokens)::bigint      AS output_tokens,
       sum(d.cache_read_tokens)::bigint  AS cache_read_tokens,
       sum(d.cache_write_tokens)::bigint AS cache_write_tokens,
       sum(d.cost_usd)::numeric          AS cost_usd,
       sum(d.request_count)::bigint      AS request_count,
       coalesce(sum(d.request_count) FILTER (WHERE d.aborted), 0)::bigint AS aborted_count
FROM usage_daily d
WHERE (sqlc.narg('user_id')::uuid IS NULL OR d.user_id = sqlc.narg('user_id')::uuid)
  AND d.bucket_start >= sqlc.arg('since')
GROUP BY d.bucket_start, d.provider, d.upstream_model
ORDER BY d.bucket_start, d.upstream_model;

-- name: UsageByKey :many
SELECT d.key_id, coalesce(k.name, '')::text AS key_name, coalesce(u.email, '')::text AS user_email,
       (k.revoked_at IS NOT NULL)::boolean AS revoked,
       sum(d.input_tokens + d.output_tokens + d.cache_read_tokens + d.cache_write_tokens)::bigint AS tokens,
       sum(d.cost_usd)::numeric     AS cost_usd,
       sum(d.request_count)::bigint AS request_count
FROM usage_daily d
LEFT JOIN api_keys k ON k.id = d.key_id
LEFT JOIN users u ON u.id = d.user_id
WHERE (sqlc.narg('user_id')::uuid IS NULL OR d.user_id = sqlc.narg('user_id')::uuid)
  AND d.bucket_start >= sqlc.arg('since')
GROUP BY d.key_id, k.name, u.email, k.revoked_at
ORDER BY sum(d.cost_usd) DESC;

-- Every user with their spend in the range (zero when idle), most spend first.
-- name: UsageByUser :many
SELECT u.id, u.email, u.name, u.username, u.role, u.plan_id, p.name AS plan_name, u.disabled_at,
       coalesce(sum(d.cost_usd), 0)::numeric      AS cost_usd,
       coalesce(sum(d.request_count), 0)::bigint  AS request_count,
       coalesce(sum(d.input_tokens + d.output_tokens + d.cache_read_tokens + d.cache_write_tokens), 0)::bigint AS tokens,
       coalesce(max(d.bucket_start), 'epoch')::timestamptz AS last_active_day -- epoch: no usage in range
FROM users u
JOIN plans p ON p.id = u.plan_id
LEFT JOIN usage_daily d ON d.user_id = u.id AND d.bucket_start >= sqlc.arg('since')
GROUP BY u.id, p.name
ORDER BY coalesce(sum(d.cost_usd), 0) DESC, u.email;
