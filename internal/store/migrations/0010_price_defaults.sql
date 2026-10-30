-- Built-in default prices (D13 follow-up, 2026-10-30): the control plane
-- writes a 'default' row per model from its built-in table (after 9router's
-- open-sse/providers/pricing.js), so known models are usable with no setup.
-- An admin row ('admin') always wins over a default, whatever its date;
-- deleting the admin rows falls back to the default.
ALTER TABLE model_prices
    ADD COLUMN source text NOT NULL DEFAULT 'admin' CHECK (source IN ('admin', 'default'));

-- Prices are keyed by the bare upstream model id. Rows confirmed under a
-- provider-prefixed name (cc/, cx/, ag/; D24) never matched and are renamed.
UPDATE model_prices
SET upstream_model = substring(upstream_model FROM 4)
WHERE upstream_model ~ '^(cc|cx|ag)/.';
