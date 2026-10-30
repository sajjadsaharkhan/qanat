-- Admin API: outbound proxies (PRD §8).

-- name: ListProxies :many
SELECT p.id, p.label, p.type, p.host, p.port, p.username, (p.password IS NOT NULL)::boolean AS has_password,
       p.enabled, p.is_default, p.created_at,
       (SELECT count(*) FROM provider_accounts a WHERE a.proxy_id = p.id)::bigint AS account_count
FROM proxies p
ORDER BY p.created_at, p.id;

-- name: GetProxy :one
SELECT id, label, type, host, port, username, password, enabled, is_default
FROM proxies
WHERE id = $1;

-- name: GetDefaultProxy :one
SELECT id, type, host, port, username, password
FROM proxies
WHERE is_default AND enabled
ORDER BY created_at, id
LIMIT 1;

-- name: CreateProxy :exec
INSERT INTO proxies (id, label, type, host, port, username, password, enabled, is_default)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: UpdateProxy :execrows
UPDATE proxies
SET label = $2, type = $3, host = $4, port = $5, username = $6, password = $7, enabled = $8, is_default = $9
WHERE id = $1;

-- name: ClearDefaultProxy :exec
UPDATE proxies SET is_default = false WHERE is_default AND id <> $1;

-- name: DeleteProxy :execrows
DELETE FROM proxies WHERE id = $1;

-- name: ProxyAccountLabels :many
SELECT label FROM provider_accounts WHERE proxy_id = $1 ORDER BY position;

-- name: ProxyGroupLabels :many
SELECT g.label FROM proxy_groups g JOIN proxy_group_members m ON m.group_id = g.id
WHERE m.proxy_id = $1 ORDER BY g.label;

-- name: ProviderProxyNames :many
SELECT provider FROM provider_proxies WHERE proxy_id = $1 ORDER BY provider;

-- name: ListProxyGroups :many
SELECT g.id, g.label, g.policy, g.created_at,
       (SELECT count(*) FROM provider_accounts a WHERE a.proxy_group_id = g.id)::bigint AS account_count
FROM proxy_groups g
ORDER BY g.created_at, g.id;

-- name: ListProxyGroupMembers :many
SELECT group_id, proxy_id FROM proxy_group_members ORDER BY group_id, priority, proxy_id;

-- name: GetProxyGroup :one
SELECT id, label, policy FROM proxy_groups WHERE id = $1;

-- name: CreateProxyGroup :exec
INSERT INTO proxy_groups (id, label, policy) VALUES ($1, $2, $3);

-- name: UpdateProxyGroup :execrows
UPDATE proxy_groups SET label = $2, policy = $3 WHERE id = $1;

-- name: DeleteProxyGroup :execrows
DELETE FROM proxy_groups WHERE id = $1;

-- name: ClearProxyGroupMembers :exec
DELETE FROM proxy_group_members WHERE group_id = $1;

-- name: AddProxyGroupMember :exec
INSERT INTO proxy_group_members (group_id, proxy_id, priority) VALUES ($1, $2, $3);

-- name: ProxyGroupAccountLabels :many
SELECT label FROM provider_accounts WHERE proxy_group_id = $1 ORDER BY position;

-- name: ProviderProxyGroupNames :many
SELECT provider FROM provider_proxies WHERE proxy_group_id = $1 ORDER BY provider;

-- name: ListProviderProxies :many
SELECT provider, proxy_id, proxy_group_id, proxy_direct FROM provider_proxies ORDER BY provider;

-- name: SetProviderProxy :exec
INSERT INTO provider_proxies (provider, proxy_id, proxy_group_id, proxy_direct)
VALUES ($1, $2, $3, $4)
ON CONFLICT (provider) DO UPDATE
SET proxy_id = EXCLUDED.proxy_id, proxy_group_id = EXCLUDED.proxy_group_id, proxy_direct = EXCLUDED.proxy_direct;

-- name: ClearProviderProxy :exec
DELETE FROM provider_proxies WHERE provider = $1;
