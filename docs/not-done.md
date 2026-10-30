# Not done yet

What the PRD asks for that the code on `p0` does not do yet, as of 2026-10-30.
Each item names the requirement, what exists today, and where the work goes.
Phase is the PRD §16 phase the item belongs to.

## Gateway

| # | Requirement | Today | Phase |
|---|---|---|---|
| G1 | ~~**OAuth connect and token refresh** (REQ-OAUTH-01/02/03)~~ **Rewritten 2026-10-30 (owner decision):** onboarding stays on official credentials — API keys, or a Claude Code token from `claude setup-token`. There is no provider-web OAuth login and no token refresh; a pasted token carries its own expiry. Landed: `auth_kind`/`expires_at`/`disabled_at`/check-result columns (migration 0007), the account section at /admin/accounts with per-provider connections, Check/Test endpoints, enable toggle and pool reordering. The old REQ-OAUTH-01/02/03 (dashboard OAuth redirect + auto-refresh) is dropped from scope. | Done. | P0 |
| G2 | **Capacity-aware routing** (REQ-POOL-04): read upstream 5-hour and weekly subscription windows from response headers; when a user's sticky account is exhausted, move them to the account with the most remaining capacity. | A 429 cools the account down and the user moves to the next account in registration order (`candidates` in `internal/gateway/proxy.go`). Subscription headers are not read. | P1 |
| G3 | **Pool headroom** (REQ-POOL-05): `qanat_pool_headroom_usd` metric and an admin dashboard view. | Not implemented. Depends on G2. | P1 |
| G4 | **Codex translator** (§7): Anthropic ↔ Responses API, streaming state machine per REQ-STREAM-01…07. | Requests that map to `codex` return `404 "doesn't support yet"`. | P1 |
| G5 | **Antigravity adapter** (§7). | Same as G4, for `antigravity`. | P2 |
| G6 | **Ping during long gaps** (REQ-STREAM-07): a `ping` event every 15 s. | Not implemented. Claude passthrough forwards Anthropic's own pings, so this matters only once translators exist. | P1 |
| G7 | **Local `count_tokens` for non-Claude providers** (§4.2). | Passthrough to Claude only. | P1 |

## Proxy

| # | Requirement | Today | Phase |
|---|---|---|---|
| P1 | **Provider-level proxy** (§8.2, step 2): a proxy or group set on the provider, between the account and the global default. | Resolution is account, then global default, then direct. `0004_proxies.sql` notes provider-level proxies "come later". | P1 |
| P2 | **Proxy groups** (§8.1): `failover` (priority order, skip unhealthy) and `sticky` (fixed proxy per account). | No group entity. | P3 |
| P3 | **Periodic health checks** (REQ-PROXY-03): connect and TLS handshake to each provider through each proxy, recording latency; skip unhealthy proxies in a failover group. | Only the on-demand Test button (`internal/proxy/probe.go`). No background loop. | P0/P1 |
| P4 | **Per-proxy metrics** (REQ-PROXY-06): connect latency, errors, active streams; `qanat_proxy_healthy`. | Not implemented. Depends on P3. | P0/P1 |
| P5 | **Token refresh through the account's proxy** (REQ-PROXY-01). | Nothing to apply it to until G1 lands. G1 must use `proxy.Pool.Client(account.Proxy)`. | with G1 |

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

1. G1 + P5: OAuth connect and refresh, through the account's proxy.
2. P3 + P4: proxy health checks and `qanat_proxy_healthy`.
3. F1 + F2 + F3: capacity test, allocation gate, `GOGC` tuning; these close the P0 performance claims.
4. P1: provider-level proxy.
5. G2 + G3: capacity-aware routing and pool headroom.
6. G4, G6, G7, then G5: translators.
7. P2: proxy groups.
