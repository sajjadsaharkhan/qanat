-- Qanat schema, P0. See docs/spec.md "Schema (PostgreSQL)".
--
-- Money is numeric (exact). IDs are UUIDs except append-only logs and price
-- versions, which use bigint identities so they sort by insertion.

-- Plans (REQ-PLAN-01..03). A request is admitted only when the Plan has at
-- least one quota window and every window has remaining > 0, so a Plan with
-- no quota rows is a zero-quota Plan.
CREATE TABLE plans (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name           text NOT NULL UNIQUE,
    timezone       text NOT NULL DEFAULT 'Asia/Tehran',
    daily_anchor   time NOT NULL DEFAULT '00:00',
    weekly_anchor  smallint NOT NULL DEFAULT 6 CHECK (weekly_anchor BETWEEN 0 AND 6), -- 0 = Sunday, 6 = Saturday
    monthly_anchor smallint NOT NULL DEFAULT 1 CHECK (monthly_anchor BETWEEN 1 AND 28),
    is_default     boolean NOT NULL DEFAULT false,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX plans_one_default ON plans (is_default) WHERE is_default;

CREATE TABLE plan_quotas (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id     uuid NOT NULL REFERENCES plans (id) ON DELETE CASCADE,
    window_kind text NOT NULL CHECK (window_kind IN ('rolling_5h', 'daily', 'weekly', 'monthly')),
    model_scope text, -- NULL: all models; otherwise one Qanat name (REQ-PLAN-02, §10.2)
    limit_usd   numeric(14, 4) NOT NULL CHECK (limit_usd >= 0)
);
CREATE UNIQUE INDEX plan_quotas_unique ON plan_quotas (plan_id, window_kind, coalesce(model_scope, ''));

CREATE TABLE plan_limits (
    plan_id        uuid PRIMARY KEY REFERENCES plans (id) ON DELETE CASCADE,
    rpm            integer NOT NULL DEFAULT 0 CHECK (rpm >= 0),            -- 0 = unlimited
    max_concurrent integer NOT NULL DEFAULT 0 CHECK (max_concurrent >= 0) -- 0 = unlimited
);

-- Empty allowlist means every model (REQ-ACCESS-01).
CREATE TABLE plan_models (
    plan_id    uuid NOT NULL REFERENCES plans (id) ON DELETE CASCADE,
    model_name text NOT NULL,
    PRIMARY KEY (plan_id, model_name)
);

-- The default Plan: all models, zero quota (REQ-PLAN-03).
INSERT INTO plans (name, is_default) VALUES ('Default', true);

CREATE TABLE users (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email        text NOT NULL,
    name         text NOT NULL DEFAULT '',
    oidc_subject text UNIQUE,
    role         text NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin')),
    plan_id      uuid NOT NULL REFERENCES plans (id),
    created_at   timestamptz NOT NULL DEFAULT now(),
    disabled_at  timestamptz
);
CREATE UNIQUE INDEX users_email ON users (lower(email));

-- Only the hash is stored; the full key is shown once (§13.2).
CREATE TABLE api_keys (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name           text NOT NULL,
    hash           bytea NOT NULL UNIQUE, -- sha256 of the full key
    prefix         text NOT NULL,         -- first characters, for display only
    allowed_models text[],                -- optional per-key restriction; NULL = Plan decides
    expires_at     timestamptz,
    last_used_at   timestamptz,
    last_ip        inet,
    revoked_at     timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX api_keys_user ON api_keys (user_id);

-- Manual top-ups with an expiry (§10.3).
CREATE TABLE quota_grants (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    amount_usd numeric(14, 4) NOT NULL CHECK (amount_usd > 0),
    expires_at timestamptz NOT NULL,
    granted_by uuid REFERENCES users (id),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE combos (
    id   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL UNIQUE
);

CREATE TABLE combo_members (
    combo_id       uuid NOT NULL REFERENCES combos (id) ON DELETE CASCADE,
    position       integer NOT NULL,
    provider       text NOT NULL CHECK (provider IN ('claude', 'codex', 'antigravity')),
    upstream_model text NOT NULL,
    PRIMARY KEY (combo_id, position)
);

-- Global mapping (REQ-MAP-01/02, D8). For target_kind = 'model' a NULL
-- upstream_model keeps the incoming name.
CREATE TABLE model_mappings (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    pattern        text NOT NULL UNIQUE,
    is_glob        boolean NOT NULL,
    target_kind    text NOT NULL CHECK (target_kind IN ('model', 'combo')),
    provider       text CHECK (provider IN ('claude', 'codex', 'antigravity')),
    upstream_model text,
    combo_id       uuid REFERENCES combos (id) ON DELETE CASCADE,
    priority       integer NOT NULL DEFAULT 0, -- lower first, among globs
    CHECK ((target_kind = 'model' AND provider IS NOT NULL AND combo_id IS NULL)
        OR (target_kind = 'combo' AND combo_id IS NOT NULL AND provider IS NULL))
);

-- Versioned prices per 1M tokens (REQ-PRICE-01). The row id is the price
-- version referenced by usage aggregates. A row with confirmed_at NULL is a
-- proposal and makes the model unusable (REQ-PRICE-03, D13).
CREATE TABLE model_prices (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    upstream_model text NOT NULL,
    p_in           numeric(14, 6) NOT NULL CHECK (p_in >= 0),
    p_out          numeric(14, 6) NOT NULL CHECK (p_out >= 0),
    p_cache_read   numeric(14, 6) NOT NULL CHECK (p_cache_read >= 0),
    p_cache_write  numeric(14, 6) NOT NULL CHECK (p_cache_write >= 0),
    effective_from timestamptz NOT NULL DEFAULT now(),
    confirmed_by   uuid REFERENCES users (id),
    confirmed_at   timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX model_prices_lookup ON model_prices (upstream_model, effective_from DESC);

-- Central pool (REQ-POOL-01/02). base_url overrides the provider's default
-- endpoint (self-hosted relays, tests). credentials is envelope-encrypted.
CREATE TABLE provider_accounts (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    provider       text NOT NULL CHECK (provider IN ('claude', 'codex', 'antigravity')),
    label          text NOT NULL,
    base_url       text,
    credentials    bytea,
    state          text NOT NULL DEFAULT 'healthy' CHECK (state IN ('healthy', 'cooldown', 'disconnected')),
    cooldown_until timestamptz,
    position       bigint GENERATED ALWAYS AS IDENTITY, -- registration order
    created_at     timestamptz NOT NULL DEFAULT now()
);

-- Sticky routing (REQ-POOL-03), mirrored in gateway memory.
CREATE TABLE sticky_bindings (
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    provider   text NOT NULL,
    account_id uuid NOT NULL REFERENCES provider_accounts (id) ON DELETE CASCADE,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, provider)
);

-- Usage aggregates (§10.6, D17, D18). No per-request rows.
CREATE TABLE usage_5m (
    bucket_start       timestamptz NOT NULL,
    user_id            uuid NOT NULL,
    key_id             uuid NOT NULL,
    provider           text NOT NULL,
    upstream_model     text NOT NULL,
    aborted            boolean NOT NULL,
    price_version      bigint NOT NULL,
    input_tokens       bigint NOT NULL DEFAULT 0,
    output_tokens      bigint NOT NULL DEFAULT 0,
    cache_read_tokens  bigint NOT NULL DEFAULT 0,
    cache_write_tokens bigint NOT NULL DEFAULT 0,
    cost_usd           numeric(18, 8) NOT NULL DEFAULT 0,
    request_count      bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (bucket_start, user_id, key_id, provider, upstream_model, aborted, price_version)
);
CREATE TABLE usage_hourly (LIKE usage_5m INCLUDING ALL);
CREATE TABLE usage_daily (LIKE usage_5m INCLUDING ALL);
CREATE INDEX usage_5m_user ON usage_5m (user_id, bucket_start);
CREATE INDEX usage_hourly_user ON usage_hourly (user_id, bucket_start);
CREATE INDEX usage_daily_user ON usage_daily (user_id, bucket_start);

CREATE TABLE audit_log (
    id        bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_id  uuid REFERENCES users (id) ON DELETE SET NULL,
    action    text NOT NULL,
    entity    text NOT NULL,
    entity_id text,
    before    jsonb,
    after     jsonb,
    at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_at ON audit_log (at DESC);

-- Config change notifications: the gateway reloads its snapshot on any of
-- these (§11.6). Statement-level, so a bulk change sends one notification.
CREATE FUNCTION notify_config() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify('qanat_config', TG_TABLE_NAME);
    RETURN NULL;
END $$;

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['plans', 'plan_quotas', 'plan_limits', 'plan_models', 'users',
                             'quota_grants', 'combos', 'combo_members', 'model_mappings', 'model_prices',
                             'provider_accounts']
    LOOP
        EXECUTE format('CREATE TRIGGER %I AFTER INSERT OR UPDATE OR DELETE OR TRUNCATE ON %I
                        FOR EACH STATEMENT EXECUTE FUNCTION notify_config()', t || '_notify', t);
    END LOOP;
END $$;

-- api_keys: last_used_at / last_ip are bumped by the usage worker and must not
-- trigger a reload. sticky_bindings is written by the gateway itself.
CREATE TRIGGER api_keys_notify
    AFTER INSERT OR DELETE OR TRUNCATE OR UPDATE OF user_id, hash, allowed_models, expires_at, revoked_at ON api_keys
    FOR EACH STATEMENT EXECUTE FUNCTION notify_config();
