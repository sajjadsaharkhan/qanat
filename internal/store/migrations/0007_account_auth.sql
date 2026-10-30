-- Account credentials get a kind and a lifecycle (not-done G1, clean
-- onboarding): api_key accounts authenticate with x-api-key, token accounts
-- (e.g. a token from `claude setup-token`) with Bearer. Pasted tokens can
-- carry the expiry their tool reported; admins can disable an account without
-- deleting it, and the dashboard's Test button records its latest result.

ALTER TABLE provider_accounts
    ADD COLUMN auth_kind        text NOT NULL DEFAULT 'token' CHECK (auth_kind IN ('token', 'api_key')),
    ADD COLUMN expires_at       timestamptz,
    ADD COLUMN disabled_at      timestamptz,
    ADD COLUMN last_check_at    timestamptz,
    ADD COLUMN last_check_ok    boolean,
    ADD COLUMN last_check_error text;

-- 9router-style reordering: admins can move an account up or down in the
-- pool, so position becomes an ordinary column instead of an identity.
ALTER TABLE provider_accounts ALTER COLUMN position DROP IDENTITY;
ALTER TABLE provider_accounts ADD CONSTRAINT provider_accounts_position UNIQUE (position) DEFERRABLE INITIALLY IMMEDIATE;
