-- How the gateway picks among a provider's accounts (after 9router's
-- fallbackStrategy). sticky-balanced: each user stays on one account, so its
-- prompt cache stays warm; new users, and users idle past the cache lifetime,
-- go to the least-loaded account weighted by quota left. round-robin: each
-- user keeps an account for sticky_limit requests, then moves to the least
-- recently used one. fill-first: pool order, the user's last good account
-- first. Under sticky-balanced and round-robin, accounts with less than
-- min_headroom_pct of their subscription quota left are tried last.
-- No row = sticky-balanced with the defaults.
CREATE TABLE provider_routing (
    provider         text PRIMARY KEY CHECK (provider IN ('claude', 'codex', 'antigravity')),
    strategy         text NOT NULL DEFAULT 'sticky-balanced' CHECK (strategy IN ('sticky-balanced', 'round-robin', 'fill-first')),
    sticky_limit     integer NOT NULL DEFAULT 3 CHECK (sticky_limit BETWEEN 1 AND 1000),
    min_headroom_pct integer NOT NULL DEFAULT 5 CHECK (min_headroom_pct BETWEEN 0 AND 100)
);

CREATE TRIGGER provider_routing_notify AFTER INSERT OR UPDATE OR DELETE OR TRUNCATE ON provider_routing
    FOR EACH STATEMENT EXECUTE FUNCTION notify_config();
