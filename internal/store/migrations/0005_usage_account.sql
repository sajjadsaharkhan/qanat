-- Usage by upstream account (dashboard "Usage by Account"). '' when no
-- account served the request (e.g. refused before routing).
ALTER TABLE usage_5m ADD COLUMN account_id text NOT NULL DEFAULT '';
ALTER TABLE usage_5m DROP CONSTRAINT usage_5m_pkey;
ALTER TABLE usage_5m ADD PRIMARY KEY (bucket_start, user_id, key_id, provider, upstream_model, account_id, aborted, price_version);
ALTER TABLE usage_hourly ADD COLUMN account_id text NOT NULL DEFAULT '';
ALTER TABLE usage_hourly DROP CONSTRAINT usage_hourly_pkey;
ALTER TABLE usage_hourly ADD PRIMARY KEY (bucket_start, user_id, key_id, provider, upstream_model, account_id, aborted, price_version);
ALTER TABLE usage_daily ADD COLUMN account_id text NOT NULL DEFAULT '';
ALTER TABLE usage_daily DROP CONSTRAINT usage_daily_pkey;
ALTER TABLE usage_daily ADD PRIMARY KEY (bucket_start, user_id, key_id, provider, upstream_model, account_id, aborted, price_version);
