-- Provider OAuth accounts (PRD §6.6, REQ-OAUTH-01/02): an account signs in
-- through the provider's own OAuth flow, 9router-style. Access and refresh
-- tokens are sealed together in credentials; the email and the last refresh
-- outcome stay as plain columns so the dashboard can show them unsealed.

ALTER TABLE provider_accounts
    ADD COLUMN email              text,
    ADD COLUMN last_refresh_at    timestamptz,
    ADD COLUMN last_refresh_error text;

ALTER TABLE provider_accounts DROP CONSTRAINT provider_accounts_auth_kind_check;
ALTER TABLE provider_accounts ADD CONSTRAINT provider_accounts_auth_kind_check
    CHECK (auth_kind IN ('token', 'api_key', 'oauth'));
