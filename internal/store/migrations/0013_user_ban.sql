-- An admin can disable a user (disabled_at, reversible, keys kept) or ban
-- them (banned_at too: every key was revoked and a reason is required).
-- Enabling clears both; revoked keys stay revoked. A Keycloak sign-in no
-- longer clears disabled_at, so a disabled user can't re-enable themselves.
ALTER TABLE users
    ADD COLUMN banned_at       timestamptz,
    ADD COLUMN disabled_reason text NOT NULL DEFAULT '',
    ADD CONSTRAINT users_banned_is_disabled CHECK (banned_at IS NULL OR disabled_at IS NOT NULL);
