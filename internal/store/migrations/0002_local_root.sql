-- D23: a break-glass root admin that signs in with a password instead of
-- Keycloak. Local accounts have a username; OIDC accounts have oidc_subject.
ALTER TABLE users ADD COLUMN username text UNIQUE;
