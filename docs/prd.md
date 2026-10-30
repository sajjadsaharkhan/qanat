# PRD — Qanat (AI Agent Gateway)

- **Status:** Approved for implementation (post-review)
- **Date:** 2026-10-30
- **Owner:** Sajjad
- **Primary client:** Claude Code
- **Language / runtime:** Go
- **Deployment:** docker compose, single replica

> **For agents:** Requirements use RFC 2119 keywords (MUST, SHOULD, MAY). Each requirement has a stable ID (e.g. `REQ-STREAM-01`) for referencing in tasks, commits and tests. Section 18 lists every settled decision. Do not reopen those decisions without the owner's approval.

---

## 1. Summary

Qanat is a multi-user gateway for AI coding agents.

- **What Claude Code sees:** to Claude Code, Qanat behaves exactly like the Anthropic Messages API.
- **What it pools:** behind it, Qanat pools subscription accounts for Claude, Codex (ChatGPT) and Antigravity (Google).
- **What it enforces:** per-user dollar-based quotas with scheduled resets.
- **How users connect:** by setting `ANTHROPIC_BASE_URL` and `ANTHROPIC_AUTH_TOKEN` in Claude Code.

**Feature inspiration:** [9router](https://github.com/decolua/9Router). Qanat closes three gaps in it:

| 9router gap | Qanat answer |
|---|---|
| Single-user. No user management, roles, personal quotas or per-user dashboard. | Keycloak SSO, two roles, Plans with quotas, per-user dashboard. |
| Slow. Adds noticeable TTFT overhead, and format translation delays the stream. | Separate Go data plane. Zero buffering. Event-by-event translation. Warm connection pools. |
| No usage control. No limits, windows, resets or per-user reporting. | Dollar-based quotas across rolling and fixed windows. Token and USD reporting. |

**Design principle:** the gateway must be imperceptible. Every byte received from a provider is forwarded to the client immediately.

---

## 2. Goals, non-goals, success metrics

### 2.1 Goals

1. Full Claude Code compatibility via the Anthropic Messages API: streaming, tool use, thinking, prompt caching.
2. Negligible TTFT overhead, and immediate forwarding of every stream chunk, including when protocol translation is required.
3. User management with Keycloak as the identity provider.
4. Per-user quotas and rate limits with predictable windows and resets.
5. A personal dashboard showing usage, remaining quota and next reset time.
6. Backend connectivity to Claude, Codex and Antigravity, with a multi-account pool per provider.

### 2.2 Non-goals (current scope)

- Dozens of providers like 9router. Scope is three providers; others are added later via `ProviderAdapter`.
- Real billing or payments. Quotas are internal accounting, not invoices.
- Token-saving prompt rewriting (9router's RTK, Caveman, etc.).
- Clients other than Claude Code (Cursor, Codex CLI, OpenAI-compatible API). The hub architecture (§7.1) MUST keep adding a client to a single inbound translator.
- Kubernetes, HA, multiple replicas. The data plane MUST stay stateless so scale-out later is only a replica-count change.
- Official provider API keys. Only subscription OAuth accounts are in scope.

### 2.3 Success metrics

| Metric | Target |
|---|---|
| Gateway TTFT overhead, passthrough path | p50 < 2 ms, p99 < 10 ms |
| Gateway TTFT overhead, translation path | p50 < 5 ms, p99 < 15 ms |
| Per-chunk latency (receive → send) | p99 < 1 ms |
| Buffer held per stream | At most one incomplete SSE event |
| Token accounting accuracy vs provider-reported usage | 100% for completed requests |
| Claude Code compatibility | All features pass the end-to-end suite with zero protocol errors |

---

## 3. Users and roles

There are exactly **two roles**, stored in Qanat (not in Keycloak).

| Role | Purpose | Permissions |
|---|---|---|
| `user` | Use Claude Code through Qanat | Manage own API keys; view own token and USD usage and limits |
| `admin` | Operate Qanat | Users and roles, Plans, quotas, provider accounts, proxies, model mapping, price table, manual resets, audit log |

- **REQ-USER-01:** A user logging in for the first time via Keycloak MUST be auto-provisioned (JIT) with role `user`.
- **REQ-USER-02:** Only an existing admin MAY promote a user to `admin`. Every role change MUST be written to the audit log.
- **REQ-USER-03:** The first admin MUST be set via the env var `BOOTSTRAP_ADMIN_EMAIL`.

### 3.1 Plans

- **REQ-PLAN-01:** Every user MUST belong to exactly one Plan.
- **REQ-PLAN-02:** A Plan MUST contain only these three things:
  - dollar quotas (§10.2)
  - rate limits (§10.2)
  - a model allowlist over Qanat model names, i.e. aliases and Combos (§6.4)
- **REQ-PLAN-03:** The default Plan for new users MUST allow all models and MUST have **zero quota**, so an admin has to assign a real Plan before the user can consume anything.

---

## 4. Claude Code compatibility

Claude Code speaks the Anthropic Messages API. Qanat MUST expose that same surface with byte-compatible behavior.

- `ANTHROPIC_AUTH_TOKEN` is sent as `Authorization: Bearer <token>`.
- `ANTHROPIC_BASE_URL` redirects all requests to Qanat.

### 4.1 Client configuration

The dashboard MUST show this snippet, prefilled with the user's own key, ready to copy:

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://qanat.example.com",
    "ANTHROPIC_AUTH_TOKEN": "qnt_live_xxxxxxxx",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "opus",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "sonnet",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "haiku",
    "CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY": "1",
    "CLAUDE_CODE_ATTRIBUTION_HEADER": "0"
  }
}
```

- `CLAUDE_CODE_ATTRIBUTION_HEADER=0` removes the attribution block from the system prompt, which improves the prompt-cache hit rate through a gateway.
- With `CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1`, Claude Code reads `/v1/models` and shows the user's allowed models in its `/model` picker.

### 4.2 Endpoints

| Endpoint | Behavior |
|---|---|
| `POST /v1/messages` | Main path. SSE streaming and non-streaming. |
| `POST /v1/messages/count_tokens` | Passed through for Claude upstream. Estimated with a local tokenizer for other providers. |
| `GET /v1/models` | Returns **only** the models allowed by the user's Plan, using Qanat names. |
| `GET /healthz`, `GET /readyz` | Health checks for docker compose. |
| `GET /metrics` | Prometheus metrics. Internal network only (§12.1). |

### 4.3 Errors

- **REQ-ERR-01:** All errors MUST use the Anthropic error schema: `{"type":"error","error":{"type":"...","message":"..."}}`.
- **REQ-ERR-02:** When a quota is exhausted, Qanat MUST return:
  - status `429` with type `rate_limit_error`
  - a `retry-after` header equal to the time until reset
  - a human-readable message, e.g. "5-hour quota exhausted; resets at 14:30".
- **REQ-ERR-03:** A request for a model not allowed by the user's Plan MUST return `403` with type `permission_error` and a readable message. Qanat MUST NOT silently fall back to another model.
- **REQ-ERR-04:** "Pool exhausted" (no upstream capacity left) MUST return a distinct message from "your quota is exhausted".

---

## 5. Architecture

The system has two parts:

- **Data plane** (`gateway`): moves streams. It has no synchronous dependency on any database.
- **Control plane** (`control`): manages users, Plans, keys, accounts, mapping and prices. It also runs the usage-aggregation worker.

```
                    ┌──────────────────── gateway (data plane, stateless) ────────────────────┐
Claude Code ──TLS──► nginx ──► Auth ──► Quota & limits ──► Router ──► Stream adapter ──┬──► Claude      (Anthropic Messages, passthrough)
     ▲                         (memory)  (Redis counters)  (sticky pool)                ├──► Codex       (Responses API, translated)
     └────────────── SSE, flushed per event ◄───────────────────────────────────────────┘──► Antigravity (Gemini-style, translated)
                                     │ async usage events (Redis Streams)
                                     ▼
Keycloak ──OIDC──► control (Admin API + aggregation worker) ──► PostgreSQL (config, usage aggregates)
                     ▲                                          Redis (counters, streams, recent-request ring buffer)
                     └── web (Next.js dashboard)
```

### 5.1 Request lifecycle

1. Claude Code sends `POST /v1/messages` with the user's personal key.
2. Auth resolves the key from the in-memory cache and identifies the user and Plan.
3. The model name is resolved via global mapping, then checked against the Plan allowlist.
4. Quota and rate limits are checked against the hot counters.
5. The Router picks the user's sticky upstream account for the target provider.
6. The stream adapter sends the request upstream (translating it only if needed) and flushes every response event to the client immediately.
7. After the stream ends, the usage event is published asynchronously and settled.

---

## 6. Model mapping and routing

### 6.1 Global model mapping

- **REQ-MAP-01:** The model name sent by the client MUST be resolved through a single **global** mapping table to either `provider/model` or a Combo. Mapping MUST NOT be overridable per Plan.
- **REQ-MAP-02:** Mapping rules MUST support exact names and glob patterns (e.g. `claude-haiku-*`). An exact match MUST take precedence over a pattern.
- **REQ-MAP-03:** If a user does not set the model env vars, Claude Code sends real Claude names. The default mapping for `claude-*` MUST be the same model on the Claude account pool.
- **REQ-MAP-04:** Provider-prefixed names MUST also be accepted: `cc/…` (Claude), `cx/…` (Codex), `ag/…` (Antigravity).

### 6.2 Combos (from 9router)

- **REQ-COMBO-01:** An admin MAY define a Combo: an alias pointing to an ordered list of models, possibly across providers (e.g. Opus on Claude → GPT on Codex → Gemini on Antigravity).
- **REQ-COMBO-02:** If a member hits its cap or errors, the next member MUST be tried. This fallback is allowed **only before the first byte has been sent to the client**.

### 6.3 Passthrough rules for Claude

- **REQ-PASS-01:** Between Claude Code and the Claude provider there MUST be **no protocol translation**.
- **REQ-PASS-02:** If the mapped model name equals the incoming name, the request body MUST be forwarded with **zero bytes changed**.
- **REQ-PASS-03:** If the name differs, only the top-level `model` value MAY be replaced, via a byte-level scan, without a full JSON parse.
- **REQ-PASS-04:** The upstream response MUST be forwarded completely unmodified, including the model name inside `message_start`.
- **REQ-PASS-05:** The `anthropic-version` and `anthropic-beta` headers MUST pass through unchanged to Anthropic upstream.

### 6.4 Model access

- **REQ-ACCESS-01:** The Plan allowlist MUST be defined over Qanat names (aliases and Combos), not over upstream model names. Combo internals stay hidden from users.
- **REQ-ACCESS-02:** `/v1/models` MUST return only allowed names. Disallowed requests MUST get `403 permission_error` (see REQ-ERR-03).

### 6.5 Account pool and routing

- **REQ-POOL-01:** Upstream accounts form a **central pool** connected by an admin. All users consume from it. Bring-your-own accounts are out of scope.
- **REQ-POOL-02:** Each account has one of three states:
  - `healthy`
  - `cooldown`, after an upstream 429
  - `disconnected`, after a failed token refresh
- **REQ-POOL-03 (sticky):** Each user MUST stay pinned to one account per provider, because the prompt cache is scoped to the upstream account.
- **REQ-POOL-04:** Upstream subscription windows (5-hour and weekly) MUST be read from response headers and considered by the Router. When a user's sticky account is exhausted, the user MUST move to the account with the most remaining capacity, and the new sticky binding is recorded. Losing the cache in this case is accepted.
- **REQ-POOL-05:** Oversubscription is allowed: the sum of user quotas MAY exceed real pool capacity. Pool headroom MUST be visible in the admin dashboard and exposed as `qanat_pool_headroom_usd`.
- **REQ-POOL-06:** Failover is allowed **only before the first byte is sent to the client**. After the stream starts, upstream errors MUST be reported as an Anthropic error event.

### 6.6 Connecting OAuth accounts on a server

Provider OAuth flows are designed for local CLIs (e.g. a callback on `localhost`). Both methods below use CLIProxyAPI packages.

- **REQ-OAUTH-01 (primary):** The admin starts login from the dashboard, signs in in their own browser, and pastes the returned redirect URL into the dashboard.
- **REQ-OAUTH-02 (fallback):** The admin uploads a credential file produced by a CLI login on their laptop.
- **REQ-OAUTH-03:** Token refresh MUST be automatic. Refresh MUST be serialized per account with a Redis lock.

---

## 7. Providers and unbuffered stream translation

Each provider is a `ProviderAdapter` with three responsibilities: upstream auth (OAuth with auto-refresh), request translation, and event-by-event response stream translation.

| Provider | Upstream format | Path | Notes |
|---|---|---|---|
| Claude (subscription OAuth) | Anthropic Messages | Passthrough | No content parsing. Usage is read from `message_start` and `message_delta` only. |
| Codex (ChatGPT OAuth) | OpenAI Responses API | Translated | Tool calls and reasoning map to Anthropic blocks. |
| Antigravity (Google OAuth) | Gemini-style `streamGenerateContent` | Translated | Includes Gemini models and Claude models served via Google. |

OAuth and upstream formats for all three providers come from [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) packages (§11.2). The translator contract below applies to them too. Any translator that violates it MUST be rewritten inside Qanat.

### 7.1 Hub format

- **REQ-HUB-01:** Anthropic Messages is the canonical hub format. Every future client surface translates **into** Anthropic, and every non-Claude provider translates **out of** Anthropic.

### 7.2 Translator contract

A translator is a **streaming state machine**, not a function over the whole response.

1. **REQ-STREAM-01:** Every complete upstream event MUST be converted to one or more Anthropic events and flushed in the same tick. The only data held back is the incomplete tail of one SSE line or JSON frame.
2. **REQ-STREAM-02:** `message_start` MUST be emitted as soon as the first upstream byte arrives, not after the first text token.
3. **REQ-STREAM-03:** Tool-call arguments MUST never be accumulated. Each fragment maps directly to `input_json_delta`; Claude Code assembles the JSON itself.
4. **REQ-STREAM-04:** Upstream reasoning maps to `thinking` blocks with `thinking_delta`. No valid Anthropic signature exists for other providers, so these blocks MUST be converted back to the provider's native form on the next turn before being sent upstream.
5. **REQ-STREAM-05:** The state machine MUST maintain block indexes and `content_block_start` / `content_block_stop`, so the ordering of text, thinking and tool_use blocks stays valid.
6. **REQ-STREAM-06:** Usage and `stop_reason` MUST be sent at the end via `message_delta`. If upstream reports no usage, it MUST be estimated with a tokenizer and flagged `estimated`.
7. **REQ-STREAM-07:** A `ping` event MUST be sent every 15 s during long upstream gaps (e.g. long reasoning).

### 7.3 Capability mismatch

When mapping sends a Claude request to a less capable provider:

- **REQ-CAP-01:** Map to an equivalent wherever one exists (e.g. thinking budget → reasoning effort).
- **REQ-CAP-02:** Silently drop hints with no equivalent (`cache_control`, beta headers). Count every drop in `qanat_translation_dropped_total`.
- **REQ-CAP-03:** Return a readable Anthropic error only for hard incompatibilities (a content type the target does not support).

### 7.4 Request history

- **REQ-HIST-01:** Full history is translated on every turn in the request direction. This path is CPU-bound and MUST be allocation-light with no extra round-trips.
- **REQ-HIST-02:** `tool_result` content and images MUST move to the target format without extra copies.

---

## 8. Proxy management

Unlike 9router (one global `HTTP_PROXY` / `ALL_PROXY` only), outbound proxies in Qanat are managed entities.

### 8.1 Proxy entity

- **Types:** HTTP, HTTPS, SOCKS5, SOCKS5h (DNS resolved on the proxy side).
- **Fields:** host, port, username, password (encrypted at rest), label, enabled flag.
- **Proxy group:** several proxies under one policy:
  - `failover`: by priority order
  - `sticky`: each account always uses one fixed proxy

### 8.2 Resolution order

The effective proxy for a request is the first level that has a value:

1. Proxy or group set on the **upstream account**.
2. Proxy or group set on the **provider**.
3. Global default proxy.
4. Direct connection.

Any level MAY be set explicitly to `direct`, which overrides the levels below it.

### 8.3 Behavior

- **REQ-PROXY-01:** All traffic for an account, including OAuth token refresh, MUST go through the same proxy, so each account keeps a stable source IP.
- **REQ-PROXY-02:** A separate warm connection pool MUST be kept per (provider, proxy) pair, so a proxy adds no handshake cost to TTFT.
- **REQ-PROXY-03:** Periodic health checks: connect and TLS handshake to each provider host through the proxy, recording latency. An unhealthy proxy in a `failover` group is skipped. Switching happens only before a stream starts.
- **REQ-PROXY-04:** Assignment changes MUST apply without restart. In-flight streams finish on their existing connection.
- **REQ-PROXY-05:** The admin panel MUST have a "Test" button showing egress IP, latency to each provider and TLS result.
- **REQ-PROXY-06:** Per-proxy metrics: connect latency, errors, active streams. Health is exposed as `qanat_proxy_healthy`.

---

## 9. Performance requirements and latency budget

**Rule zero:** apart from the upstream connection, there is **no blocking I/O on the hot path**. Auth, quota checks and account selection answer from memory or Redis. Accounting happens asynchronously after the stream.

| Hot-path stage | Budget (p99) | How |
|---|---|---|
| Header parse + key auth | 0.2 ms | Key-hash lookup in in-memory cache; invalidated via `LISTEN/NOTIFY` |
| Quota + concurrency check | 0.5 ms | Hot counters in Redis (`CounterStore`) |
| Upstream account selection | 0.1 ms | Sticky table in memory |
| Request translation (translation path only) | 3 ms | Streaming JSON parse; no full tree when unnecessary |
| Upstream connect | ~0 | Warm HTTP/2 pool, keep-alive, TLS session resumption |
| Per-chunk send to client | 1 ms | Flush after every event, `TCP_NODELAY`, no compression on SSE |

### 9.1 Implementation requirements

- **REQ-PERF-01:** SSE responses MUST set `Cache-Control: no-cache` and `X-Accel-Buffering: no`. The Nginx config in §12.2 is part of the deliverable.
- **REQ-PERF-02:** Real backpressure. If the client reads slowly, upstream reads slow down too. Unbounded in-memory queues are forbidden.
- **REQ-PERF-03:** A client disconnect MUST cancel the upstream request immediately. Usage up to that point MUST be recorded.
- **REQ-PERF-04:** Logging request or response bodies on the hot path is forbidden. Log metadata and usage only.
- **REQ-PERF-05:** Continuous benchmark in CI using a mock upstream with precise chunk timing. A PR that worsens p99 by more than 10% MUST fail.

### 9.2 Capacity

Current real load is 1–20 users. The design target is 500 concurrent streams on 1 vCPU and 512 MB for one replica.

---

## 10. Usage accounting, quotas and resets

Like 9router, every request is measured in **tokens** and **USD**.

- USD is the "API-equivalent cost": what the same usage would cost on the paid API. It is not a real charge.
- **Quota caps are USD only.** Tokens are recorded for display and reporting.

### 10.1 Cost calculation

For each request, these values are recorded from provider-reported usage: input tokens, output tokens (including reasoning), cache-read tokens and cache-write tokens.

```
cost_usd = (t_in * p_in + t_out * p_out + t_cache_read * p_cr + t_cache_write * p_cw) / 1_000_000
```

- `p_*` is the price per 1M tokens for that model, from the **model price table**.
- The table is seeded with official prices and is editable by admins.
- **REQ-PRICE-01:** Prices MUST be versioned with an effective date. Usage aggregates reference the price version, so a price change never alters past cost.
- **REQ-PRICE-02:** For models with no public API price (some Codex- or Antigravity-specific models), the nearest public same-tier price is proposed and the admin confirms or edits it.
- **REQ-PRICE-03:** A model without a confirmed price MUST NOT be usable in any Plan, because a USD quota has no effect on it.

### 10.2 Limit types

Rate limits stay separate from quotas, so one user cannot lock the whole pool through parallelism.

| Type | Unit | Behavior when exceeded |
|---|---|---|
| Rate limit | Requests per minute, concurrent streams | Immediate `429`, `retry-after` of a few seconds |
| Rolling quota | USD in the last 5 hours | `429` with time until capacity frees up |
| Periodic quota | USD daily, weekly, monthly | `429` with time until the period resets |
| Per-model / per-Combo quota | USD for one model (e.g. "Opus max $5/day") | `429` for that model only; others stay open |

### 10.3 Resets

- **Periodic (fixed window):** the reset instant is defined by a timezone and an anchor in the Plan. Defaults:
  - daily: 00:00 `Asia/Tehran`
  - weekly: Saturday
  - monthly: 1st of the month

  Counters are Redis keys with a TTL until reset, so no cron is needed.
- **Rolling (sliding window):** counted in 5-minute buckets. A bucket's usage frees automatically once it leaves the window.
- **Manual:** an admin can reset a user's quota or add a temporary USD grant with an expiry. Both actions are audit-logged.

### 10.4 Admission algorithm: admit-then-settle

The cost of a request is unknown until it finishes.

1. **On admission:** if every applicable window has remaining > 0, admit the request and increment concurrency.
2. **During the stream:** record input usage from `message_start` immediately, so parallel requests by the same user see it.
3. **At the end:** settle the actual output usage.
4. **Overdraft bound:** maximum overdraft is one in-flight request per concurrent stream. An in-flight request is never cut off mid-stream.

- **REQ-QUOTA-01:** Real usage MUST always be recorded, including streams cut mid-way that Claude Code then retries (upstream really consumed them). Such usage MUST be tagged `aborted` in aggregates so it is distinguishable in the dashboard.

### 10.5 Warnings

- The dashboard MUST warn at 80% and 95% of every quota.
- A response header `x-qanat-quota-remaining-usd` SHOULD be exposed for status-line tools.

### 10.6 Usage storage

Usage data is important and must be durable, but there is no synchronous write on the hot path.

1. **Hot counters:** Redis, behind the `CounterStore` interface, with AOF `appendfsync everysec`. Quota checks read only these counters.
2. **Async channel:** after each stream ends, the data plane writes a usage event to **Redis Streams** without waiting.
3. **Durable aggregates:** a worker in `control` is the **only** consumer of the stream. It buffers events and writes them to PostgreSQL as aggregate upserts. A single consumer means no race conditions on aggregate rows.
4. **Aggregation levels**, keyed by user, model, provider and the `aborted` flag:
   - 5-minute buckets, retained 7 days, for exact rolling-window rebuild
   - hourly and daily, retained forever, for reporting
5. **No per-request rows** in PostgreSQL.
6. **Rebuild:** if Redis comes back empty, window counters are rebuilt from the 5-minute aggregates.
7. **Recent requests:** a Redis ring buffer with the last 200 requests per user, with a TTL of a few days, for dashboard debugging only.

**Accepted data-loss window** on a hard crash: Redis fsync window, about 1 s. A local WAL is intentionally **not** added.

---

## 11. Tech stack

### 11.1 Why Go

- **Ready-made code for the hardest part.** CLIProxyAPI (Go, MIT) covers Claude Code, Codex and Antigravity OAuth, token refresh and multi-account handling, and offers a Go SDK for embedding. These providers' internal protocols are undocumented and change often; maintaining them from scratch was the project's biggest cost.
- **Rust gateways don't cover this.** Rust gateways (TensorZero, Helicone) cover API-key providers only, not subscription OAuth.
- **The speed gap is small.** Go vs Rust differs by microseconds up to ~1 ms at p99. That is negligible against provider TTFT and is offset by allocation control.
- **Design reference.** [new-api](https://github.com/QuantumNous/new-api) (Go) is a reference for user, quota and cost-accounting design. It is AGPL-3.0: take design inspiration only and **copy no code**.

### 11.2 Use of CLIProxyAPI

- **REQ-CLIP-01:** Use only its provider packages: OAuth login and refresh, upstream client, request format. The hot path (auth, quota, routing, streaming, flushing) belongs entirely to Qanat.
- **REQ-CLIP-02:** Benchmark its translators against a mock upstream before adopting them. Any translator that buffers or exceeds the latency budget is rewritten inside Qanat.
- **REQ-CLIP-03:** Pin the dependency to an exact version. Upgrade only after golden tests and benchmarks pass.
- **REQ-CLIP-04:** All usage goes behind the internal `ProviderAdapter` interface, so replacing it later only touches adapters.

### 11.3 Libraries

| Area | Choice | Reason |
|---|---|---|
| HTTP server/client | `net/http` with HTTP/2; `http.Flusher` after every event | Direct control of flushing; warm pools |
| Providers | CLIProxyAPI packages (pinned) | OAuth and formats for Claude, Codex, Antigravity |
| Outbound proxy | `http.Transport.Proxy` + `golang.org/x/net/proxy` | HTTP, HTTPS, SOCKS5 per transport |
| JSON | `bytedance/sonic` on the hot path | Fast parse without reflection |
| In-memory cache | `ristretto` or `otter` | Keys, Plans, mapping, prices |
| PostgreSQL / Redis | `pgx` + `sqlc` / `go-redis` | Type-safe, pooled |
| Auth | `coreos/go-oidc` | Dashboard OIDC; local JWT verification |
| Observability | Prometheus client; OpenTelemetry (optional) | Metrics and traces |
| Dashboard | Next.js + React + Tailwind | Separate from the data plane; consumes the Admin API only |

### 11.4 Keeping p99 low in Go

- Set `GOMEMLIMIT` to ~80% of the container memory limit. Tune `GOGC` by benchmark.
- Use `sync.Pool` for SSE buffers and event structs. Target: zero allocations per chunk on the passthrough path.
- Profile continuously with `pprof` in the CI benchmark. Any allocation growth on the hot path fails the PR.

### 11.5 Code layout

```
internal/protocol   Anthropic Messages types, streaming SSE codec
internal/provider   ProviderAdapter + Claude/Codex/Antigravity adapters on CLIProxyAPI
internal/core       quota, pricing, routing, mapping, Combo; shared by both binaries
internal/proxy      transport management per proxy
cmd/gateway         data plane binary
cmd/control         Admin API + usage aggregation worker
web/                dashboard (Next.js)
```

### 11.6 Runtime architecture

- The data plane is stateless. It holds a config snapshot in memory (users, keys, Plans, mapping, prices, proxies), refreshed via PostgreSQL `LISTEN/NOTIFY`.
- Quota counters live in Redis behind `CounterStore`. Durable usage flows through Redis Streams to the `control` worker (§10.6).
- Redis also holds the OAuth refresh locks and the recent-request ring buffer.

---

## 12. Deployment

Qanat runs with **docker compose** and **one replica**. Kubernetes is not required.

| Service | Role |
|---|---|
| `nginx` | TLS and network edge, no buffering for SSE |
| `gateway` | Data plane (1 replica) |
| `control` | Admin API + usage aggregation worker; same image, different entrypoint |
| `web` | Next.js dashboard |
| `postgres` | Config and usage aggregates. Volume plus daily `pg_dump` backup. |
| `redis` | Counters, Streams, ring buffer. AOF with `appendfsync everysec`. |

Keycloak, Prometheus, Grafana and Alertmanager come from the owner's existing infrastructure. They are **not** part of the compose file.

### 12.1 Required Nginx configuration for SSE

These settings are part of the deliverable. Without them the latency targets are lost.

```nginx
upstream qanat_gateway {
    server gateway:8080;
    keepalive 64;
}

location /v1/ {
    proxy_pass http://qanat_gateway;
    proxy_http_version 1.1;
    proxy_set_header Connection "";
    proxy_buffering off;
    proxy_request_buffering off;
    proxy_cache off;
    gzip off;
    proxy_read_timeout 3600s;
    proxy_send_timeout 3600s;
    client_max_body_size 32m;
    tcp_nodelay on;
}
```

- `proxy_request_buffering off` lets large Claude Code bodies (long history) reach the gateway without waiting for the full upload.
- `client_max_body_size` MUST exceed the largest Claude Code request, including images and history.
- `/metrics` MUST be reachable only from the internal network or an IP allowlist for Prometheus, never publicly.
- Set `stop_grace_period: 10m` on the `gateway` service to match graceful shutdown.

### 12.2 Location and scale path

- Network RTT to providers affects TTFT more than code does. Host in a datacenter near the providers' endpoints (Europe or US).
- **Scale path:** add `gateway` replicas behind the same Nginx. State lives in Redis and PostgreSQL, and aggregation has a single consumer, so no architectural change is needed.

---

## 13. Authentication with Keycloak

Keycloak is the source of truth for **identity only**. Roles, Plans, quotas and keys live in Qanat. There are two auth paths, because Claude Code cannot perform an OIDC login.

### 13.1 Dashboard (humans)

- **REQ-AUTH-01:** OIDC Authorization Code + PKCE against the existing Keycloak (a new client in the current realm).
- **REQ-AUTH-02:** **Entry gate.** Only members of the Keycloak group `qanat-users` MAY log in. Other realm users get an access-denied page.
- **REQ-AUTH-03:** First login JIT-provisions the user with role `user` and the zero-quota default Plan. These two layers mean a mistake in one does not open the pool.
- **REQ-AUTH-04:** Basic profile fields (email, name) are refreshed from the token on every login.

### 13.2 Claude Code (machines)

- **Phase 1: personal API key.**
  - The user creates a key in the dashboard: name, expiry, optional model restriction.
  - Only the key hash is stored, and the full key is shown once.
  - Keys use the prefix `qnt_live_` to support secret scanning.
- **Phase 2: short-lived token.**
  - An `apiKeyHelper` script obtains a short-lived access token via the Keycloak Device Authorization Flow.
  - The gateway verifies it locally against a cached JWKS, with no Keycloak call on the hot path.

### 13.3 Lifecycle

- **REQ-AUTH-05:** Disabling a user in Keycloak MUST invalidate all their keys within 60 s, via a Keycloak admin event listener or polling plus cache invalidation.
- **REQ-AUTH-06:** For every key, show last-used time, last IP and per-key usage. The user or an admin can revoke it instantly.

---

## 14. Dashboards

The user dashboard answers one question at a glance: *"How much is left, and when does it refill?"*

### 14.1 User dashboard

- **Quota status:** for each window (5-hour, daily, weekly, monthly), a usage bar, remaining USD and a countdown to reset.
- **Usage:** charts in USD and tokens over time, broken down by model, key and token type (input, output, cache read, cache write), plus cache-hit rate.
- **Recent requests:** the last 200 from the Redis ring buffer, showing model, provider, TTFT, duration, tokens, cost and status. No prompt content.
- **Keys:** create, revoke, last used.
- **Setup:** the ready-to-copy `settings.json` snippet and a one-line connection test command.

### 14.2 Admin dashboard

- **Users and Plans:** list users, assign Plans, manual reset, grants, role changes.
- **Provider accounts:** OAuth connect (§6.6), health, cooldown, each account's usage against its own subscription windows, number of sticky users.
- **Overview:** total usage, top users, pool headroom vs total assigned quotas, error rate and TTFT per provider.
- **Proxies:** define, group, test, assign.
- **Model mapping, Combos and price table.**
- **Audit log:** every change to Plans, quotas, keys, roles, accounts, mapping and prices, with actor and timestamp.

The frontend runs separately from the data plane, so dashboard load never affects streaming.

---

## 15. Observability, security, non-functional requirements

### 15.1 Observability

- **REQ-OBS-01:** Qanat exposes only `/metrics`. The existing Prometheus, Grafana and Alertmanager scrape it. They are **not** in compose.
- **Performance metrics:**
  - gateway overhead (client TTFT minus upstream TTFT)
  - inter-chunk gap
  - tokens per second
  - active streams
  - errors and 429s by provider, account and reason
  - `qanat_translation_dropped_total`
- **Status metrics for alerting:**
  - `qanat_account_healthy`
  - `qanat_proxy_healthy`
  - `qanat_pool_headroom_usd`
  - aggregation worker lag on Redis Streams
- **REQ-OBS-02:** Ship a sample Prometheus alert-rules file in the repo. Qanat has **no** notification layer of its own; states are also shown in the admin dashboard.
- Logs are structured JSON. OpenTelemetry tracing is optional.

### 15.2 Security

- Upstream credentials (refresh tokens) are stored with envelope encryption in the database. The master key is a Docker secret in compose.
- User API keys are stored as hashes only.
- Prompt and response content is never logged by default. Debug mode for a specific user requires admin approval and expires automatically.
- TLS at the edge. Keycloak and upstream traffic is HTTPS only.
- User isolation: no data from one user's request is visible to another user. The gateway's internal cache holds metadata only.

### 15.3 Non-functional requirements

| Area | Requirement |
|---|---|
| Deployment | docker compose, 1 replica. All Qanat services from one image; Nginx, PostgreSQL, Redis and web use their own images. |
| Scalability | Data plane holds no local state, so adding replicas needs no architectural change. Aggregation has a single consumer. |
| Resilience | A temporary Redis outage MUST NOT cut in-flight streams. Usage events wait in a bounded in-memory buffer until Redis returns. |
| Graceful shutdown | In-flight streams get up to 10 minutes to finish. The usage event buffer is fully flushed. |
| Configuration | Plans, mapping, prices and providers live in the database, with hot reload and no restart. |
| Testing | Golden tests per translator on recorded real streams. End-to-end tests with headless Claude Code. |

---

## 16. Delivery phases

P0 builds only the Claude passthrough path, with users, quotas and benchmarks, so the latency target is proven on the simplest path first. Each translator is then measured against the same bar. Codex comes before Antigravity because the Responses API is better documented and sets the translator pattern for the next one.

| Phase | Scope | Exit gate |
|---|---|---|
| **P0 — Claude MVP** | Claude passthrough; personal API keys; Keycloak SSO + compose deployment; USD quotas with token reporting; async usage store (Redis Streams → aggregates); latency benchmark in CI | Gateway overhead p99 < 10 ms |
| **P1 — Codex + pool** | Codex translator; multi-account pool; sticky routing; 5-hour rolling window; full user dashboard; per-provider proxies | Golden tests green |
| **P2 — Antigravity** | Antigravity adapter; admin console; per-model quotas; model discovery; `apiKeyHelper` + SSO device flow | All 3 providers pass e2e |
| **P3 — Hardening** | Multi-replica gateway; more client surfaces (via inbound translators); proxy failover groups; alert-rules pack | — |

---

## 17. Risks

| Risk | Impact | Mitigation |
|---|---|---|
| Consumer subscription ToS (Claude, ChatGPT, Antigravity) typically disallow sharing one account among several people | Upstream account suspension | Review each provider's terms before multi-user use. Official API keys are out of scope for now. |
| Codex and Antigravity internal APIs are undocumented and unstable | Sudden translator breakage | Isolated adapters, golden tests on recorded traffic, a feature flag to disable each provider |
| New Claude Code features (beta headers, new blocks) | Protocol errors on the translation path | Unknowns pass through on passthrough; explicit, readable rejection on the translation path |
| Tool JSON Schema incompatibility with Gemini | Tool-call failures on Antigravity | Tool-schema sanitization layer, tested against real Claude Code tools |
| Prompt-cache loss when moving users between accounts | Higher TTFT and usage | Sticky routing; cache-hit-rate metric per account |
| CLIProxyAPI dependency: internal API changes, abandonment, or a buffering translator | Broken build or latency regression after upgrade | Version pin; golden tests and benchmarks before every upgrade; everything behind `ProviderAdapter` |
| Oversubscription: total user quotas exceed real pool capacity | A user with quota left is rejected because the pool is full | `qanat_pool_headroom_usd` metric and alert; headroom in admin dashboard; distinct "pool exhausted" error |

---

## 18. Decision log

All decisions below are settled. Open questions: **none**.

| # | Decision |
|---|---|
| D1 | Product name: **Qanat**. |
| D2 | Language: **Go** for both data plane and control plane. Dashboard in Next.js. |
| D3 | Provider layer built on CLIProxyAPI packages (pinned, behind `ProviderAdapter`). |
| D4 | Scale: 1–20 users. docker compose, 1 replica, no Kubernetes. The architecture must allow adding replicas later. |
| D5 | Upstream accounts: central admin-owned pool. No BYO accounts. Official API keys out of scope. |
| D6 | Clients: Claude Code only for now. Anthropic Messages is the hub format, so new clients mean one inbound translator. |
| D7 | Claude path: zero translation. Body unchanged unless the model name is remapped, in which case only the `model` field changes. Response always unchanged. |
| D8 | Model mapping is global (exact + glob; exact wins). Targets may be a model or a Combo. |
| D9 | Plans contain only USD quotas, rate limits and a model allowlist on Qanat names. Disallowed model → `403 permission_error`. |
| D10 | Roles: `user` and `admin`, stored in Qanat. JIT users start as `user`. |
| D11 | Keycloak (existing) gate: group `qanat-users`, plus a default Plan with zero quota. |
| D12 | Quotas are USD-only. Tokens are for display and reporting. Rate limits (RPM, concurrency) are kept. |
| D13 | Unpriced models are unusable until an admin confirms a price (default proposal: nearest public same-tier price). |
| D14 | Aborted or retried streams are charged for real usage and tagged `aborted`. |
| D15 | Pool oversubscription allowed. An exhausted sticky account moves the user to the account with the most headroom. |
| D16 | Capability mismatch: map equivalents, silently drop the rest (counted in metrics), error only on hard incompatibility. |
| D17 | Usage storage: Redis hot counters → Redis Streams → single `control` worker → PostgreSQL aggregates. No WAL; ~1 s loss window accepted. |
| D18 | Aggregates: 5-minute (7 days), hourly and daily (forever). No per-request rows. Recent requests come from a 200-entry Redis ring buffer per user. |
| D19 | Edge: Nginx with the SSE config in §12.1. |
| D20 | Observability: `/metrics` only, consumed by existing Prometheus/Grafana. Alerting via existing Alertmanager with a sample rules file. No notification layer in Qanat. |
| D21 | OAuth onboarding on the server: paste the redirect URL in the dashboard (primary), or upload a credential file (fallback). |
| D22 | No token-saver features (RTK etc.). |
