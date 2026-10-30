# Testing Qanat

This is how Qanat is tested, and how to use the testing infrastructure that ships with the repo. Requirement IDs (`REQ-…`) refer to [prd.md](prd.md).

## What makes a good test here

- **Test behavior at the highest seam, not implementation.** Assert what Claude Code, an admin or Prometheus could observe: bytes on the wire, status codes, error shapes, timing, usage in the Admin API. Don't assert on internal structs, call counts or private functions; refactors must not break tests.
- **Name tests after the requirement they prove** and cite the ID: `TestPassthrough_ModelRewriteOnlyTouchesModelField // REQ-PASS-03`.
- **A test must be able to fail.** New acceptance tests are checked against a deliberately broken reference gateway (see [Proving the suite](#proving-the-suite)).
- **Fail fast, never hang.** Every request has a timeout; the mock upstream releases held streams at shutdown.
- **Determinism over sleeps.** Synchronize on recorded events (`Request.Done`, `WrittenAt`) and poll with `eventually` for async effects. No bare `time.Sleep` to wait for work.

## The seams

There are three seams, as few as possible and as high as possible.

| Seam | What drives it | What it covers | Where |
|---|---|---|---|
| **A. Gateway HTTP boundary** (primary) | A real HTTP client acting like Claude Code, against scripted mock upstreams | passthrough fidelity, streaming without buffering, auth, model mapping and access, quotas and errors, routing and failover, disconnects, usage recording, latency | `test/acceptance/` |
| **B. Translator goldens** | Recorded upstream streams fed through a translator state machine | the translator contract (REQ-STREAM-01…07) for Codex and Antigravity: event order, block indexes, tool-argument fragments, thinking, usage | next to each translator, using `internal/testkit/golden` |
| **C. Control-plane Admin API** | HTTP tests against `control` with the test OIDC issuer | users, roles, Plans, mapping, prices, keys, OAuth paste flow, aggregation worker | next to the control plane, using `oidctest` + `itenv` |

Plus a smoke tier that is not CI-blocking: headless Claude Code against the full compose stack.

## Layout

```
internal/testkit/
  sse/            SSE codec that keeps raw bytes (for byte-exact assertions)
  mockupstream/   scripted fake provider: timed chunks, aborts, holds, request recording
  latency/        per-chunk and TTFT overhead measurement, PRD latency budgets
  golden/         golden files with normalization and scrubbing
  oidctest/       in-process Keycloak stand-in (also standalone: cmd/oidcmock)
  itenv/          resolves Redis/PostgreSQL for integration tests, or skips
test/acceptance/  Seam A suite + Driver interface + reference gateway
test/fixtures/    recorded upstream streams (JSON Lines), validated and secret-scanned
cmd/mockupstream  fixture-serving fake provider for the compose stack
cmd/loadgen       concurrent TTFT benchmark and CI latency gate
cmd/oidcmock      standalone test OIDC issuer for Playwright
deploy/test/      compose.test.yml, nginx.test.conf (PRD SSE settings), tools Dockerfile
web/              Vitest (unit, component, token contrast) and Playwright (light + dark, axe)
```

The test kit uses only the Go standard library, so it adds no dependencies to the module.

## Running tests

| Command | Needs Docker | What it runs |
|---|---|---|
| `make test` | no | unit tests, goldens, fixture checks; acceptance tests skip (no driver) |
| `make test-race` | no | the same with the race detector |
| `make test-reference` | no | acceptance suite against the in-process reference gateway |
| `make ci` | no | lint + test + test-race + test-reference (what the `go` CI job runs) |
| `make test-integration` | yes | starts Redis/PostgreSQL/mockupstream, runs everything that needs them |
| `make test-acceptance` | yes | full stack via Nginx, acceptance suite with `-tags qanat` |
| `make bench` | yes | latency gate with `loadgen` |
| `make web-test` | no | Vitest |
| `make web-e2e` | yes | Playwright, light and dark |

Integration tests call `itenv.Redis(t)` / `itenv.Postgres(t)`. Without the env vars set by `make test-integration` they **skip**, so `go test ./...` stays green on a laptop without Docker.

## Seam A: the acceptance suite

Tests talk to the system only through a `Driver` (`test/acceptance/driver.go`):

```
GatewayURL · Reset · CreateUser · SetPlan · CreateKey · AddUpstreamAccount · SetMapping · SetPrice · Usage
```

Drivers register from build-tagged files:

| Tag | Driver | Purpose |
|---|---|---|
| none | — | every test skips |
| `reference` | in-process reference gateway | proves the suite asserts real behavior |
| `qanat` | the real stack | the actual acceptance gate |

### Current coverage

| Test | Requirement |
|---|---|
| `TestPassthrough_RequestBodyAndHeadersUnchanged` | REQ-PASS-01/02/05, user key never sent upstream |
| `TestPassthrough_ModelRewriteOnlyTouchesModelField` | REQ-PASS-03 |
| `TestPassthrough_ResponseBytesUnchanged` | REQ-PASS-04, REQ-PERF-01 |
| `TestStreaming_NoBuffering` | REQ-STREAM-01 (structural: each chunk arrives before the next is written) |
| `TestStreaming_ClientDisconnectCancelsUpstream` | REQ-PERF-03 |
| `TestStreaming_UpstreamDiesMidStreamYieldsErrorEvent` | REQ-POOL-06 |
| `TestRouting_FailoverBeforeFirstByte` | REQ-POOL-04/06 |
| `TestAuth_UnknownKeyIsRejected` | REQ-ERR-01 |
| `TestAccess_DisallowedModelIs403WithoutFallback` | REQ-ERR-03, REQ-ACCESS-01 |
| `TestAccess_ModelsListIsFilteredByPlan` | REQ-ACCESS-02 |
| `TestQuota_ZeroQuotaIs429WithRetryAfter` | REQ-PLAN-03, REQ-ERR-02 |
| `TestUsage_RecordedAndPriced` | §10.1, REQ-QUOTA-01 |
| `TestLatency_PassthroughTTFTOverhead` | §2.3 |

Tests to add as phases land: sticky routing (REQ-POOL-03), rolling and periodic windows (§10.3), per-model quota (§10.2), aborted tagging (REQ-QUOTA-01), Combo fallback (REQ-COMBO-02), proxy routing (REQ-PROXY-01..04), key revocation within 60 s (REQ-AUTH-05), `count_tokens` (§4.2), and the translated paths (Codex, Antigravity).

### The `qanat` driver

`test/acceptance/qanat_driver_test.go` is a placeholder that **fails every test on purpose** until implemented, so the acceptance CI job can never pass by skipping. Implementing it is a P0 task. It talks to:

- the gateway through Nginx (`QANAT_ACCEPTANCE_GATEWAY`, default `http://localhost:58080`);
- the control plane (`QANAT_ACCEPTANCE_CONTROL`, default `http://localhost:58081`), using the normal Admin API where possible, plus **test-only endpoints** that exist only when `QANAT_TEST_MODE=1`:

| Endpoint | Purpose |
|---|---|
| `POST /test/reset` | truncate all tables and flush Redis |
| `POST /test/users` `{email}` → `{id}` | create a user without an OIDC login |
| `POST /test/session` `{user_id}` → `{admin_token}` | Admin API credentials for seeding |
| `POST /test/flush-usage` | make the aggregation worker drain Redis Streams now (keeps `Usage` polling short) |

`QANAT_TEST_MODE` must refuse to start if any production-looking setting is present (e.g. a non-localhost OIDC issuer).

### Latency tests

`TestLatency_PassthroughTTFTOverhead` measures `client TTFT − upstream TTFT` over 55 warm requests. On shared CI runners it enforces 10× the PRD budget (catches gross regressions without flaking). With `QANAT_LATENCY_STRICT=1` on a dedicated runner it enforces the real budget (p50 < 2 ms, p99 < 10 ms). Skipped with `-short`.

### Proving the suite

A test that can't fail proves nothing. The reference gateway was mutated to check that the suite catches each fault:

| Mutation | Caught by |
|---|---|
| no per-chunk flush (buffering) | `NoBuffering`, `ClientDisconnectCancelsUpstream` |
| re-encodes the JSON body | `RequestBodyAndHeadersUnchanged`, `ModelRewriteOnlyTouchesModelField` |
| upstream request not tied to client context | `ClientDisconnectCancelsUpstream` |
| model never rewritten | `ModelRewriteOnlyTouchesModelField` |
| missing `X-Accel-Buffering: no` | `ResponseBytesUnchanged` |

When you add a test, make the reference (or the real gateway, in a scratch branch) violate the requirement once and confirm the test fails.

## Seam B: translator goldens

```go
sc, _ := mockupstream.LoadFixture("../../test/fixtures/upstream/codex/reasoning_function_call.jsonl", 0)
events := runTranslator(sc)                     // the translator under test
golden.AssertEvents(t, "codex_reasoning_function_call.events", events)
```

- Goldens live in `testdata/golden/` beside the test.
- `golden.Normalize` sorts JSON keys and scrubs volatile values (`id`, `signature`), so diffs show real changes only.
- Update after an intended change with `make golden-update`, then review the diff like code.
- Each golden test also asserts the contract structurally:
  - block indexes are monotonic;
  - every `content_block_start` has a matching stop;
  - `message_start` comes first;
  - `usage` is present in `message_delta`.

### Recording fixtures

Fixtures are JSON Lines: a `meta` line (status, headers) followed by `chunk` lines with `at_ms` (offset from request arrival) and the raw bytes received:

```
{"type":"meta","status":200,"headers":{"Content-Type":"text/event-stream"}}
{"type":"chunk","at_ms":412,"data":"event: message_start\ndata: {...}\n\n"}
```

To record one:

1. Run the gateway with a recording tap on a personal account.
2. Exercise the case with Claude Code.
3. Scrub tokens, account IDs, emails and prompt content (replace with neutral text).
4. Commit under `test/fixtures/upstream/<provider>/`.

`make fixtures-check` parses every fixture as SSE and **fails on secret-like values**: Anthropic/OAuth tokens, JWTs, Google access tokens, Qanat keys, recorded `authorization` headers.

The fixtures shipped now are **synthetic** (marked in their `meta.note`). Replace them with scrubbed real recordings before relying on Seam B, because the providers' undocumented formats are exactly what goldens must pin down.

## Seam C: control plane

- `oidctest.New(t)` gives an in-process Keycloak stand-in:
  - Keycloak-shaped endpoints;
  - RS256 tokens with a `groups` claim;
  - an auto-login code flow: `LoginAs(claims)`, and the next `/auth` redirects back with a code.
- Test the entry gate (REQ-AUTH-02), JIT provisioning with role `user` and the zero-quota Plan (REQ-USER-01, REQ-AUTH-03), role changes in the audit log (REQ-USER-02), and `BOOTSTRAP_ADMIN_EMAIL` (REQ-USER-03).
- The aggregation worker is tested against real Redis/PostgreSQL via `itenv`:
  - publish usage events;
  - assert 5-minute, hourly and daily aggregates;
  - kill Redis and assert the windows are rebuilt from the 5-minute aggregates (§10.6).

## Benchmarks and the latency gate

`cmd/loadgen` sends the same streaming request to the mock upstream directly and through the gateway (via Nginx), with concurrency, and compares TTFT distributions. `make bench` fails when:

- overhead p99 exceeds 10 ms (`-budget-p99`), or
- overhead p99 regresses more than 10% against `bench/baseline.json` (REQ-PERF-05), or
- any request errors.

The result is uploaded as a CI artifact. Promote a new baseline deliberately by committing `bench/result.json` as `bench/baseline.json`.

## Dashboard tests

See [design.md §11](../design.md#11-testing-the-ui). In short:

- **Vitest unit tests:**
  - `lib/format.ts` (quota thresholds, money, tokens, Tehran-midnight resets);
  - `lib/model-tier.ts`;
  - **`lib/contrast.test.ts`**, which reads `globals.css` and fails if any token pairing drops below WCAG AA in either theme.
- **Component tests:** opt in to jsdom per file (`// @vitest-environment jsdom`) and mock the Admin API with MSW.
- **Playwright:**
  - runs every page in `light` and `dark` projects (plus a mobile project for `@mobile` specs);
  - signs in through `oidcmock` with `POST /test/login-as`;
  - requires zero axe WCAG 2.1 AA violations;
  - checks that no infinite animation runs under reduced motion;
  - compares full-page screenshots.

## CI

`.github/workflows/ci.yml`:

| Job | Runs | Active |
|---|---|---|
| `go` | lint, race, reference acceptance, fixture checks | always |
| `integration` | everything with Redis/PostgreSQL service containers | always |
| `acceptance` | `make test-acceptance` + `make bench` through Nginx | once `cmd/gateway/main.go` exists |
| `web` | typecheck, lint, Vitest; Playwright once `cmd/control/main.go` exists | once `web/package-lock.json` exists |

Jobs light up automatically as P0 lands, so the gates exist before the code they guard.
