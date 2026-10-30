-- Proxy groups and provider-level proxies (PRD §8.1, §8.2). An account or a
-- provider points at one proxy, one group, or direct; at most one of the
-- three. Resolution: account, else provider, else the global default proxy,
-- else direct.
CREATE TABLE proxy_groups (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    label      text NOT NULL,
    -- failover: members in priority order, unhealthy ones skipped.
    -- sticky: each account always uses one member (stable hash of its ID).
    policy     text NOT NULL CHECK (policy IN ('failover', 'sticky')),
    created_at timestamptz NOT NULL DEFAULT now()
);

-- RESTRICT on the proxy for the same reason as provider_accounts.proxy_id:
-- removing a member silently moves accounts to another source IP.
CREATE TABLE proxy_group_members (
    group_id uuid NOT NULL REFERENCES proxy_groups (id) ON DELETE CASCADE,
    proxy_id uuid NOT NULL REFERENCES proxies (id) ON DELETE RESTRICT,
    priority integer NOT NULL, -- lower first (failover order)
    PRIMARY KEY (group_id, proxy_id)
);

ALTER TABLE provider_accounts
    ADD COLUMN proxy_group_id uuid REFERENCES proxy_groups (id) ON DELETE RESTRICT,
    DROP CONSTRAINT provider_accounts_proxy_one,
    ADD CONSTRAINT provider_accounts_proxy_one
        CHECK (num_nonnulls(proxy_id, proxy_group_id, NULLIF(proxy_direct, false)) <= 1);

-- One row per provider that overrides the global default. No row = inherit.
CREATE TABLE provider_proxies (
    provider       text PRIMARY KEY CHECK (provider IN ('claude', 'codex', 'antigravity')),
    proxy_id       uuid REFERENCES proxies (id) ON DELETE RESTRICT,
    proxy_group_id uuid REFERENCES proxy_groups (id) ON DELETE RESTRICT,
    proxy_direct   boolean NOT NULL DEFAULT false,
    CHECK (num_nonnulls(proxy_id, proxy_group_id, NULLIF(proxy_direct, false)) = 1)
);

CREATE TRIGGER proxy_groups_notify AFTER INSERT OR UPDATE OR DELETE OR TRUNCATE ON proxy_groups
    FOR EACH STATEMENT EXECUTE FUNCTION notify_config();
CREATE TRIGGER proxy_group_members_notify AFTER INSERT OR UPDATE OR DELETE OR TRUNCATE ON proxy_group_members
    FOR EACH STATEMENT EXECUTE FUNCTION notify_config();
CREATE TRIGGER provider_proxies_notify AFTER INSERT OR UPDATE OR DELETE OR TRUNCATE ON provider_proxies
    FOR EACH STATEMENT EXECUTE FUNCTION notify_config();
