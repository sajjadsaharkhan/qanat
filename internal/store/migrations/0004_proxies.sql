-- Outbound proxies (PRD §8). An account uses its own proxy, else the global
-- default, else a direct connection; proxy_direct pins an account to direct
-- even when a default exists (§8.2). Groups and provider-level proxies come
-- later (P3).
CREATE TABLE proxies (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    label      text NOT NULL,
    type       text NOT NULL CHECK (type IN ('http', 'https', 'socks5', 'socks5h')),
    host       text NOT NULL,
    port       integer NOT NULL CHECK (port BETWEEN 1 AND 65535),
    username   text,
    password   bytea, -- sealed with the master key, aad = proxy id
    enabled    boolean NOT NULL DEFAULT true,
    is_default boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX proxies_one_default ON proxies (is_default) WHERE is_default;

-- RESTRICT: deleting a proxy in use would silently move its accounts to
-- another source IP (REQ-PROXY-01). The API reports which accounts use it.
ALTER TABLE provider_accounts
    ADD COLUMN proxy_id uuid REFERENCES proxies (id) ON DELETE RESTRICT,
    ADD COLUMN proxy_direct boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT provider_accounts_proxy_one CHECK (NOT (proxy_direct AND proxy_id IS NOT NULL));

CREATE TRIGGER proxies_notify AFTER INSERT OR UPDATE OR DELETE OR TRUNCATE ON proxies
    FOR EACH STATEMENT EXECUTE FUNCTION notify_config();
