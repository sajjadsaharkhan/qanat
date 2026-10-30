# Spec — Qanat (AI Agent Gateway)

- **Source:** [prd.md](prd.md) (requirements `REQ-…`, decisions `D1…D22`)
- **Testing:** [testing.md](testing.md)
- **UI:** [../design.md](../design.md)
- **Status:** ready for agent

## Problem Statement

I run AI coding agents (Claude Code) for myself and a small group of up to 20 people. We have subscription accounts for Claude, ChatGPT (Codex) and Google (Antigravity), and I want to share them through one endpoint. Today I use 9router for this, and it falls short in three ways:

- **It is single-user.** There is no notion of people: no sign-in, no personal keys, no per-person limits, and no way for someone to see what they have used.
- **It is slow.** It adds noticeable delay before the first token. When it translates between formats, the stream reaches the client late instead of as it arrives.
- **It has no usage control.** I can't cap how much one person spends, define daily, weekly or rolling windows, or reset them. Nobody can see their usage in tokens or dollars.

Without these, sharing accounts means one heavy user can exhaust everyone's capacity, nobody knows where usage went, and every request pays a latency tax.

## Solution

Qanat is a self-hosted gateway that Claude Code talks to as if it were the Anthropic API. A person points Claude Code at it by setting two environment variables, `ANTHROPIC_BASE_URL` and `ANTHROPIC_AUTH_TOKEN`. Behind it, Qanat pools the admin's subscription accounts, routes each request to the right provider and account, and streams every byte back the moment it arrives.

- **For Claude requests,** it changes nothing: the request and response pass through untouched.
- **For Codex and Antigravity,** it translates event by event without holding the stream.

People sign in with the existing Keycloak, create personal API keys, and see on a dashboard how much they have used and when their quota refills. Usage is shown in tokens and in API-equivalent dollars.

The admin assigns each person a Plan with dollar quotas (5-hour rolling, daily, weekly, monthly, per model), rate limits and allowed models. The admin also connects provider accounts, routes each provider or account through a chosen outbound proxy, and gets alerted through their existing Prometheus when an account, proxy or the pool needs attention.

## User Stories

### Connecting Claude Code

1. As a developer, I want to point Claude Code at Qanat by setting `ANTHROPIC_BASE_URL` and `ANTHROPIC_AUTH_TOKEN`, so that I don't need any plugin or wrapper.
2. As a developer, I want the dashboard to show a ready-to-copy `settings.json` snippet with my key, so that setup takes one paste.
3. As a developer, I want a one-line command to test my connection, so that I know setup worked before I start working.
4. As a developer, I want every Claude Code feature to work through Qanat (streaming, tool use, thinking, prompt caching, images), so that I don't lose capabilities by using the gateway.
5. As a developer, I want the first token to arrive as fast as if I were talking to the provider directly, so that the gateway is imperceptible.
6. As a developer, I want each streamed chunk delivered the instant it is produced, so that long answers and tool calls feel live.
7. As a developer, I want Claude Code's `/model` picker to list exactly the models my Plan allows, so that I only pick models that will work.
8. As a developer, I want to use short names like `opus`, `sonnet` and `haiku`, or the real Claude model names, so that I don't need to learn gateway-specific naming.
9. As a developer, I want to use provider-prefixed names like `cx/gpt-5.5` or `ag/gemini-3-pro`, so that I can deliberately choose a non-Claude model.
10. As a developer, I want errors to appear in Claude Code as readable messages ("Daily quota exhausted; resets at 00:00"), so that I know what happened and when I can continue.
11. As a developer, I want a request for a model I'm not allowed to use to fail clearly with a permission error, so that I'm never silently given a different model.
12. As a developer, I want closing Claude Code or pressing Escape to stop the upstream generation immediately, so that I'm not charged for output nobody reads.
13. As a developer, I want my prompt cache to keep working across turns, so that long sessions stay fast and cheap.

### Keys and sign-in

14. As a developer, I want to sign in to the dashboard with my company Keycloak account, so that I don't manage another password.
15. As a developer, I want to create several named API keys with optional expiry, so that I can use separate keys per machine.
16. As a developer, I want to see each key only once at creation, so that keys are never retrievable later by anyone.
17. As a developer, I want to see when and from which IP each key was last used and how much it has spent, so that I can spot a leaked key.
18. As a developer, I want to revoke a key instantly, so that a leaked key stops working at once.
19. As a developer, I want my keys to stop working within a minute of my Keycloak account being disabled, so that offboarding is safe.
20. As a person outside the allowed Keycloak group, I want a clear "you don't have access" page, so that I know to ask an admin instead of retrying.

### Seeing usage

21. As a developer, I want the overview to tell me in one sentence how much I have left and when it refills, so that I know at a glance whether I can keep working.
22. As a developer, I want a bar per quota window (5-hour, daily, weekly, monthly, per-model) with remaining dollars and a countdown, so that I can see which limit is closest.
23. As a developer, I want warnings at 80% and 95% of a quota, so that a cut-off never surprises me.
24. As a developer, I want usage charts in dollars and tokens over time, broken down by model, key and token type, so that I understand where my usage goes.
25. As a developer, I want to see my cache-hit rate, so that I know whether my sessions use caching well.
26. As a developer, I want a list of my last 200 requests with model, provider, time to first token, duration, tokens, cost and status, so that I can see why a request was slow or failed.
27. As a developer, I want requests interrupted mid-stream to be marked as aborted, so that I understand charges for retries.
28. As a developer, I want to be sure the dashboard never shows anyone my prompts or responses, so that my work stays private.
29. As a developer, I want a response header with my remaining quota, so that my terminal status line can show it.
30. As a developer, I want the dashboard to look right in light and dark mode and follow my system setting, so that it fits my environment.

### Administering users and Plans

31. As an admin, I want new people to be created automatically on first sign-in as regular users with a zero-quota Plan, so that nobody consumes the pool until I assign a Plan.
32. As an admin, I want to promote a user to admin, or demote one, with every change logged, so that access changes are traceable.
33. As an admin, I want the first admin to be set by an environment variable, so that a fresh install is usable.
34. As an admin, I want to define Plans with dollar quotas per window, rate limits and an allowed-model list, so that I can give different people different budgets.
35. As an admin, I want per-model dollar quotas inside a Plan (for example Opus capped at $5 per day), so that one expensive model doesn't consume the whole budget.
36. As an admin, I want to set the timezone and anchor for daily, weekly and monthly resets (default 00:00 Asia/Tehran, Saturday, 1st), so that resets match our working week.
37. As an admin, I want to reset a user's quota or grant a temporary dollar top-up with an expiry, so that I can unblock someone for an urgent task.
38. As an admin, I want to see every user's usage and who uses the most, so that I can adjust Plans fairly.
39. As an admin, I want an audit log of every change to Plans, quotas, keys, roles, accounts, mapping and prices, so that I can answer "who changed this?"

### Administering accounts, routing and models

40. As an admin, I want to connect a provider subscription by signing in in my own browser and pasting the redirect URL into the dashboard, so that I can add accounts to a server without a local callback.
41. As an admin, I want to upload a credential file from a CLI login as a fallback, so that I can still connect an account when the browser flow breaks.
42. As an admin, I want tokens refreshed automatically, so that accounts don't silently expire.
43. As an admin, I want to see each account's health (healthy, cooldown, disconnected), its usage against its own 5-hour and weekly windows, and how many users are pinned to it, so that I can balance the pool.
44. As an admin, I want users pinned to one account per provider, so that their prompt cache stays warm.
45. As an admin, I want a user moved to the account with the most remaining capacity when their account runs out, so that work continues.
46. As an admin, I want a failed or rate-limited account to be skipped before the first byte is sent, so that users rarely see upstream errors.
47. As an admin, I want to see total pool headroom against the total quota I have handed out, so that I know when I've oversubscribed.
48. As an admin, I want one global table that maps incoming model names (exact or glob) to a provider model or a Combo, so that I control where every request goes.
49. As an admin, I want to define Combos (ordered fallbacks across providers), so that a request survives one provider being out of capacity.
50. As an admin, I want a model price table seeded with official prices, editable and versioned by date, so that dollar figures are right and past costs never change.
51. As an admin, I want models without a confirmed price to be unusable until I price them, with a suggested price from the nearest public model, so that dollar quotas always apply.
52. As an admin, I want hints a provider can't use (like cache-control) dropped silently and counted, so that requests don't fail over details.

### Proxies

53. As an admin, I want to define outbound proxies (HTTP, HTTPS, SOCKS5, SOCKS5h) with credentials stored encrypted, so that provider traffic leaves from the network location I choose.
54. As an admin, I want to group proxies with a failover or sticky policy, so that one proxy failing doesn't stop traffic.
55. As an admin, I want to assign a proxy or group per provider and override it per account, with "direct" as an explicit option, so that each account keeps a stable source IP.
56. As an admin, I want token refresh for an account to use that account's proxy too, so that the provider never sees the account from two places.
57. As an admin, I want a "Test" button showing a proxy's egress IP, latency to each provider and TLS result, so that I can verify a proxy before assigning it.
58. As an admin, I want proxy changes applied without a restart while in-flight streams finish undisturbed, so that I can change routing during work hours.

### Operations

59. As the operator, I want Qanat to start with one `docker compose up`, so that running it is simple.
60. As the operator, I want a documented Nginx configuration that never buffers streams, so that the edge doesn't undo the gateway's speed.
61. As the operator, I want a `/metrics` endpoint for my existing Prometheus, with account, proxy and pool-headroom health metrics and a sample alert-rules file, so that I'm alerted through tools I already run.
62. As the operator, I want usage to survive restarts and a Redis wipe, rebuilt from durable aggregates, so that quotas stay accurate.
63. As the operator, I want a restart to let in-flight streams finish (up to 10 minutes), so that upgrades don't cut people off mid-answer.
64. As the operator, I want a temporary Redis outage to leave running streams untouched, so that a cache blip isn't an outage.
65. As the operator, I want to add gateway replicas later without changing the architecture, so that growth is just configuration.
66. As the operator, I want a CI gate that fails when gateway latency regresses, so that speed doesn't erode over time.

## Implementation Decisions

### Shape of the system

- **One Go module, two binaries from one image.**
  - `gateway` is the data plane. It is stateless and holds an in-memory configuration snapshot.
  - `control` is the Admin API and the usage-aggregation worker.
  - A separate Next.js dashboard consumes only the Admin API and never talks to the data plane.
- **Deployment.** docker compose with one replica of each, behind Nginx; PostgreSQL and Redis in compose; Keycloak, Prometheus, Grafana and Alertmanager from existing infrastructure (D4, D19, D20).
- **Hub format.** Anthropic Messages is the canonical format (REQ-HUB-01). New client surfaces translate in; non-Claude providers translate out. Only Claude Code is supported now (D6).

### Modules and their interfaces

- **Protocol.** Anthropic Messages types and a streaming SSE codec that forwards raw bytes and parses only what accounting needs (usage in `message_start` and `message_delta`).
- **ProviderAdapter** (one per provider) exposes:
  - prepare the upstream request for an account (auth, URL, body: unchanged for Claude, translated otherwise);
  - a streaming translator that turns upstream events into Anthropic events one at a time;
  - extract usage;
  - classify upstream errors as retryable before first byte, or terminal.

  OAuth login, token refresh and upstream formats come from pinned CLIProxyAPI packages behind this interface (D3, REQ-CLIP-01…04). Any adapter that buffers or misses the latency budget is rewritten inside Qanat.
- **Translator.** A pure state machine without I/O, satisfying REQ-STREAM-01…07: flush per complete event, emit `message_start` on the first upstream byte, never accumulate tool arguments, map reasoning to `thinking`, maintain block indexes, emit usage at the end, ping every 15 s.
- **Router** maps an incoming name to a target, in this order:
  1. the global mapping table: exact name first, then glob (D8);
  2. the Plan allowlist check, on Qanat names;
  3. Combo expansion;
  4. account selection: sticky per user and provider, skipping accounts in cooldown or disconnected, and moving to the account with most headroom when the sticky one is exhausted (REQ-POOL-03/04);
  5. pre-first-byte failover only (REQ-POOL-06).
- **CounterStore.** An interface over hot quota counters, implemented in Redis:
  - fixed windows are keys with a TTL until the reset instant (timezone and anchor from the Plan);
  - the 5-hour rolling window uses 5-minute buckets;
  - concurrency and RPM counters live here too.

  The admission algorithm is admit-then-settle (PRD §10.4): admit if every window has remaining > 0, record input usage at `message_start`, settle output at the end. Overdraft is bounded to one in-flight request per concurrent stream.
- **Pricing.** Cost is `(in·p_in + out·p_out + cache_read·p_cr + cache_write·p_cw) / 1e6`, using a versioned price table with effective dates. Usage aggregates reference the price version (REQ-PRICE-01). Unpriced models are rejected for every Plan (D13).
- **Usage pipeline (D17, D18).**
  - After each stream, the gateway publishes a usage event to Redis Streams without blocking.
  - A single consumer in `control` buffers events and upserts aggregates: 5-minute (retained 7 days), hourly and daily (retained forever), keyed by user, model, provider, key and the `aborted` flag.
  - There are no per-request rows. A per-user ring buffer of the last 200 requests lives in Redis with a TTL.
  - If Redis comes back empty, window counters are rebuilt from 5-minute aggregates.
  - The accepted loss window is about 1 s (Redis `appendfsync everysec`); there is no WAL.
- **Proxy manager.**
  - Proxies and groups are entities with encrypted credentials.
  - Resolution order is account, then provider, then global, then direct; any level may explicitly set "direct".
  - One warm transport per (provider, proxy) pair. OAuth refresh uses the account's proxy.
  - Health checks run periodically; switching happens only before a stream starts.
  - Changes apply without restart (REQ-PROXY-01…06).
- **Auth.**
  - Personal keys use the `qnt_live_` prefix and are stored as hashes only, resolved from an in-memory cache invalidated via PostgreSQL `LISTEN/NOTIFY`.
  - Dashboard sign-in is OIDC Authorization Code + PKCE, handled server-side. The browser never holds a Keycloak token.
  - The entry gate is Keycloak group `qanat-users` (D11). JIT provisioning creates role `user` with the zero-quota default Plan. Roles live in Qanat (D10). The first admin comes from `BOOTSTRAP_ADMIN_EMAIL`.
  - Disabling a user in Keycloak revokes their keys within 60 s.
  - The OIDC configuration has a public issuer URL (the expected `iss`, used for browser redirects) and an optional internal URL for discovery/JWKS fetches.
  - Phase 2 adds short-lived tokens via `apiKeyHelper` and the Device Authorization Flow, verified locally against a cached JWKS.

### Schema (PostgreSQL)

- `users`: id, email, name, role, plan_id, created_at, disabled_at.
- `api_keys`: id, user_id, name, hash, prefix, expires_at, last_used_at, last_ip, revoked_at.
- `plans`: id, name, timezone, daily/weekly/monthly anchors; with child rows:
  - `plan_quotas`: window kind (rolling_5h, daily, weekly, monthly), optional model scope, limit_usd;
  - `plan_limits`: rpm, max_concurrent;
  - `plan_models`: allowed Qanat names; empty means all.
- `user_overrides` and `quota_grants`: amount_usd, expires_at, granted_by.
- `model_mappings`: pattern, is_glob, target_kind (model or combo), provider, upstream_model, combo_id, priority.
- `combos` and `combo_members`: ordered.
- `model_prices`: upstream_model, p_in, p_out, p_cache_read, p_cache_write, effective_from, confirmed_by.
- `provider_accounts`: id, provider, label, encrypted credentials, state, cooldown_until, proxy_ref.
- `proxies`, `proxy_groups`, `proxy_group_members`, `provider_proxy_assignments`.
- `usage_5m`, `usage_hourly`, `usage_daily`: bucket_start, user_id, key_id, provider, upstream_model, aborted, input/output/cache_read/cache_write tokens, cost_usd, request_count, price_version.
- `audit_log`: actor, action, entity, before, after, at.
- `sticky_bindings`: user_id, provider, account_id. Mirrored to memory; the source of truth survives restarts.

### API contracts

- **Gateway (Claude Code surface):**
  - `POST /v1/messages` (streaming and non-streaming)
  - `POST /v1/messages/count_tokens`
  - `GET /v1/models` (filtered by Plan)
  - `GET /healthz`, `GET /readyz`
  - `GET /metrics` (internal only)
- **Gateway errors** always use the Anthropic envelope:
  - `401 authentication_error`
  - `403 permission_error` for a disallowed model
  - `429 rate_limit_error` with `retry-after` until reset, plus a readable message naming the window and reset time
  - a distinct message for "pool exhausted" vs "your quota is exhausted"
  - after the first byte, upstream failures become an `error` SSE event
- **Gateway headers:**
  - SSE responses set `Cache-Control: no-cache` and `X-Accel-Buffering: no`;
  - responses carry `x-qanat-quota-remaining-usd`;
  - the user's key never reaches a provider.
- **Admin API (control):** JSON over HTTP with an OpenAPI spec, from which the dashboard client is generated. Resource groups:
  - me (overview, usage, requests, keys)
  - users, plans, grants
  - accounts (including the OAuth start/paste and credential upload)
  - proxies (including test), mappings, combos, prices
  - audit
  - metrics summary for the admin overview and Needs-you queue
- **Test-only endpoints** (only with `QANAT_TEST_MODE=1`, which refuses production-looking config): `POST /test/reset`, `POST /test/users`, `POST /test/session`, `POST /test/flush-usage`.

### Performance decisions

- Hot path: auth, quota, routing and the first upstream write involve no synchronous database I/O.
- Flush after every event; `TCP_NODELAY`; no compression on SSE.
- Real backpressure with bounded buffers.
- Client disconnect cancels upstream immediately and records usage so far.
- Warm HTTP/2 pools with TLS resumption.
- `sync.Pool` buffers, with a target of zero allocations per chunk on passthrough.
- `GOMEMLIMIT` at about 80% of the container limit.
- Never log request or response bodies.
- **Passthrough fidelity** (D7): when the mapped name equals the incoming one, zero bytes change. Otherwise only the top-level `model` value is replaced by a byte-level scan. The response is never modified.

### Dashboard

- Next.js App Router, TypeScript strict, Tailwind v4, shadcn/ui, TanStack Query and Table, react-hook-form with zod, Recharts via shadcn `chart`, next-themes (light, dark, system).
- Visual language from Tower, per design.md: monochrome ink, amber and red reserved for state, flat rows, motion only on state change.
- Design tokens live only in `globals.css`. Lint bans raw colors and physical (non-RTL) spacing.

## Testing Decisions

- **Good tests assert external behavior only:** bytes on the wire, status codes, error envelopes, timing, and usage visible through the Admin API. They never touch internals, so the implementation can be refactored freely. Tests cite the requirement they prove.
- **Seam A, the gateway HTTP boundary (primary):**
  - The acceptance suite drives the gateway like Claude Code against scripted mock upstreams.
  - It is implementation-agnostic through a `Driver`: an in-process reference gateway proves the suite can fail (validated by mutation), and the `qanat` driver runs it against the real stack through Nginx.
  - It covers passthrough fidelity, no-buffering (structural), disconnect cancellation, mid-stream failure, pre-first-byte failover, auth, model access, `/v1/models` filtering, quota errors, usage pricing and TTFT overhead.
- **Seam B, translator goldens:** recorded upstream streams per provider fed through each translator, compared with normalized golden event sequences, plus structural contract checks. Fixtures are scrubbed and secret-scanned in CI.
- **Seam C, Admin API:** HTTP tests with an in-process OIDC issuer standing in for Keycloak (entry gate, JIT provisioning, roles, bootstrap admin). Aggregation-worker tests run against real Redis and PostgreSQL, including the rebuild-after-Redis-loss path.
- **Latency gate:** a load generator compares TTFT direct to the mock vs through gateway and Nginx under concurrency. It fails at p99 overhead > 10 ms or a > 10% regression against the committed baseline (REQ-PERF-05).
- **Dashboard:**
  - Vitest for formatting, quota thresholds, reset math and model tiers.
  - A token-contrast test that reads `globals.css` and enforces WCAG AA in both themes.
  - Component tests with MSW.
  - Playwright on every page in light and dark: OIDC sign-in via the mock issuer, zero axe violations, reduced-motion stillness, and screenshot comparison.
- **Prior art:** this repo's testing infrastructure (`internal/testkit`, `test/acceptance`, `cmd/loadgen`, `cmd/mockupstream`, `cmd/oidcmock`, `deploy/test`, `web/` test setup), described in testing.md. There is no earlier codebase.

## Out of Scope

- Providers beyond Claude, Codex and Antigravity; official provider API keys; bring-your-own accounts (D5).
- Clients other than Claude Code (Cursor, Codex CLI, OpenAI-compatible endpoints). The architecture keeps them to one inbound translator each.
- Real billing, payments or invoices. Dollar figures are API-equivalent accounting.
- Token-saving prompt rewriting such as RTK or Caveman (D22).
- Kubernetes, HA and multiple replicas (the design keeps the path open).
- A notification layer inside Qanat. Alerts go through the existing Alertmanager.
- Per-request usage rows and prompt or response logging.
- Prometheus and Grafana inside compose.

## Further Notes

- **Phasing (PRD §16):**
  - **P0:** Claude passthrough, keys, Keycloak, compose, USD quotas, async usage store, latency gate. Exit gate: overhead p99 < 10 ms.
  - **P1:** Codex translator, multi-account pool, sticky routing, 5-hour window, full user dashboard, per-provider proxies. Exit gate: goldens green.
  - **P2:** Antigravity, admin console, per-model quotas, model discovery, `apiKeyHelper`. Exit gate: three providers pass end to end.
  - **P3:** hardening.
- **First P0 tasks for agents:** implement the `qanat` acceptance driver and the test-only endpoints, so `make test-acceptance` goes from deliberately failing to green; replace the synthetic fixtures with scrubbed real recordings.
- **Risks to watch** (PRD §17):
  - provider terms on account sharing;
  - undocumented Codex/Antigravity formats;
  - Gemini tool-schema incompatibilities;
  - prompt-cache loss on account moves;
  - CLIProxyAPI upstream churn;
  - oversubscription.
- **Name:** Qanat (قنات), an underground channel that moves water without being seen. The gateway should be just as unnoticeable.
