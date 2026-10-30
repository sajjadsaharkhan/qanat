-- Prices are keyed by the provider's model id (the id in provider_models and
-- what the gateway forwards), never by a Qanat name, prefixed name or alias.
-- The column name now says so.
ALTER TABLE model_prices RENAME COLUMN upstream_model TO model_id;
ALTER INDEX model_prices_lookup RENAME TO model_prices_model_id;
