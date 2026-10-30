-- Provider model catalogs (PRD §6.6 follow-up): the models each provider
-- really serves, fetched from the provider itself with a stored account's
-- credential and persisted so /v1/models and the Models page read the same
-- up-to-date list without hitting the provider per view.

CREATE TABLE provider_models (
    provider   text NOT NULL CHECK (provider IN ('claude', 'codex', 'antigravity')),
    model      text NOT NULL,
    fetched_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, model)
);
