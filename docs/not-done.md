# Not done yet

What the PRD asks for that the code on `p0` does not do yet, as of 2026-10-30.
Each item names the requirement, what exists today, and where the work goes.
Phase is the PRD §16 phase the item belongs to.

## Gateway

| # | Requirement | Today | Phase |
|---|---|---|---|
| G1 | **OAuth connect and token refresh** (REQ-OAUTH-01/02/03). Reopened 2026-10-30 (owner decision): provider OAuth like 9router, alongside API keys and `claude setup-token`. | Landed: dashboard sign-in for Claude, Codex (ChatGPT; the CLI's client only redirects to `localhost:1455/auth/callback`, so the admin pastes that address; email and ChatGPT account ID come from the id_token and are sealed with the tokens) and Antigravity (Google; Cloud Code project via `loadCodeAssist`/`onboardUser`, sealed with the tokens), automatic refresh — a control-plane sweep refreshes tokens within 5 min of expiry, a gateway 401 on an OAuth account fails over and NOTIFYs `qanat_oauth_refresh` for an immediate refresh, serialized per account with a Redis lock, through the account's proxy; a revoked refresh token disconnects the account with the reason shown. Missing: REQ-OAUTH-02 (credential-file upload). | P0 |
| G2 | **Capacity-aware routing** (REQ-POOL-04): read upstream 5-hour and weekly subscription windows from response headers; when a user's sticky account is exhausted, move them to the account with the most remaining capacity. | A 429 cools the account down and the user moves to the next account in registration order (`candidates` in `internal/gateway/proxy.go`). Subscription headers are not read. | P1 |
| G3 | **Pool headroom** (REQ-POOL-05): `qanat_pool_headroom_usd` metric and an admin dashboard view. | Not implemented. Depends on G2. | P1 |
| G4 | **Codex translator** (§7): Anthropic ↔ Responses API, streaming state machine per REQ-STREAM-01…07. | Requests that map to `codex` return `404 "doesn't support yet"`. | P1 |
| G5 | **Antigravity adapter** (§7). | Landed 2026-10-31 (`internal/antigravity`, `internal/gateway/antigravity.go`): Messages → Cloud Code `streamGenerateContent` and Gemini SSE → Anthropic SSE, after 9router (MIT) without its tool cloaking or prompt rewriting. Thought signatures round-trip statelessly inside `thinking` blocks; 429 honours Google's retry delay (capped 10 min). Missing: `count_tokens` is a ~4-bytes-a-token estimate; image output and server tools (web search) are dropped; G6 pings. | P2 |
| G6 | **Ping during long gaps** (REQ-STREAM-07): a `ping` event every 15 s. | Not implemented. Claude passthrough forwards Anthropic's own pings, so this matters only once translators exist. | P1 |
| G7 | **Local `count_tokens` for non-Claude providers** (§4.2). | Passthrough to Claude only. | P1 |

## Proxy

| # | Requirement | Today | Phase |
|---|---|---|---|
| P1 | **Provider-level proxy** (§8.2, step 2): a proxy or group set on the provider, between the account and the global default. | Resolution is account, then global default, then direct. `0004_proxies.sql` notes provider-level proxies "come later". | P1 |
| P2 | **Proxy groups** (§8.1): `failover` (priority order, skip unhealthy) and `sticky` (fixed proxy per account). | No group entity. | P3 |
| P3 | **Periodic health checks** (REQ-PROXY-03): connect and TLS handshake to each provider through each proxy, recording latency; skip unhealthy proxies in a failover group. | Only the on-demand Test button (`internal/proxy/probe.go`). No background loop. | P0/P1 |
| P4 | **Per-proxy metrics** (REQ-PROXY-06): connect latency, errors, active streams; `qanat_proxy_healthy`. | Not implemented. Depends on P3. | P0/P1 |
| P5 | **Token refresh through the account's proxy** (REQ-PROXY-01). | Done: the refresher resolves the account's §8.2 proxy chain for the token call. | with G1 |

## Performance

| # | Requirement | Today | Phase |
|---|---|---|---|
| F1 | **Capacity target** (§9.2): 500 concurrent streams on 1 vCPU / 512 MB for one replica. | `make bench` runs 50 concurrent requests. The limit has not been load-tested. | P0 |
| F2 | **Allocation gate** (§11.4): zero allocations per chunk on the passthrough path; CI fails on allocation growth; `pprof` in the benchmark. | Buffers are pooled (`bufPool`), but nothing measures or enforces allocations. | P0 |
| F3 | **`GOGC` tuned by benchmark** (§11.4). | `GOMEMLIMIT=400MiB` is set; `GOGC` is default. | P0 |

## Quota

No gaps against P0. One behavior to keep in mind:

- **Fail-open on Redis errors.** If the admission check errors or exceeds 500 ms, the request is admitted (§15.3, deliberate). Usage is still recorded through Redis Streams and aggregated, but a user can exceed quota while Redis is down.

## Suggested order

1. G1: credential-file upload (P5 landed with the refresh).
2. P3 + P4: proxy health checks and `qanat_proxy_healthy`.
3. F1 + F2 + F3: capacity test, allocation gate, `GOGC` tuning; these close the P0 performance claims.
4. P1: provider-level proxy.
5. G2 + G3: capacity-aware routing and pool headroom.
6. G4, G6, G7, then G5: translators.
7. P2: proxy groups.
