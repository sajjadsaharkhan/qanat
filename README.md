# Qanat

A self-hosted, multi-user gateway that Claude Code talks to as if it were the Anthropic API. It pools Claude, Codex and Antigravity subscription accounts, enforces per-user dollar quotas, and streams every byte the moment it arrives.

Connect Claude Code:

```json
{ "env": { "ANTHROPIC_BASE_URL": "https://qanat.example.com", "ANTHROPIC_AUTH_TOKEN": "qnt_live_…" } }
```

## Status

P0 in progress. Working now:

- **Gateway:** Claude passthrough (zero bytes changed; only `model` rewritten when mapped), unbuffered streaming, key auth, global model mapping, Plan allowlists, USD quotas (rolling 5-hour, daily, weekly, monthly, per model) with rate limits, failover before the first byte, usage recording, `/v1/models`, `/metrics`. The acceptance suite passes against the real stack through Nginx; measured overhead p99 ≈ 4–6 ms.
- **Control plane:** Admin API, root admin sign-in, Plans, keys, accounts, mapping, prices, usage aggregation, audit log.
- **Dashboard:** overview, keys, setup, admin users/Plans/audit.

Not yet: connecting real Claude subscription accounts with OAuth (M5), Keycloak sign-in, Codex and Antigravity (P1/P2).

## Run it locally

```sh
make local-up      # builds and starts everything; prints where the root password is
```

Open http://localhost:3000 and sign in as `root` with the password in `deploy/local/secrets/root_password` (generated on first run, never committed). The gateway listens on http://localhost:18080. Change ports with `QANAT_WEB_PORT` / `QANAT_GATEWAY_PORT`. `make local-down` stops it; `make local-reset` also deletes its data.

## Documents

| Document | What it is |
|---|---|
| [docs/prd.md](docs/prd.md) | Product requirements with stable IDs (`REQ-…`) and the decision log |
| [docs/spec.md](docs/spec.md) | Implementation spec: user stories, module interfaces, schema, API contracts |
| [docs/testing.md](docs/testing.md) | Test seams, how to run everything, fixtures, latency gate, CI |
| [design.md](design.md) | Dashboard design system: shadcn/ui, light and dark themes, tokens, components |

## Quick start (tests)

```sh
make ci               # lint, unit, race, acceptance suite vs reference gateway (no Docker)
make test-integration # + Redis, PostgreSQL, mock upstream (Docker)
make help             # everything else
```

## Repository map

```
cmd/            gateway, control; test tools loadgen, mockupstream, oidcmock
internal/       control (Admin API + openapi.yaml), core (quota windows, API keys),
                store (migrations, sqlc queries), config, platform, testkit
deploy/local/   compose stack for running Qanat on your machine
test/           acceptance suite (Seam A), recorded upstream fixtures
deploy/test/    compose test stack, Nginx SSE config, tools Dockerfile
web/            dashboard (Next.js, shadcn/ui); API types generated from openapi.yaml
```

## Rules for agents working here

- Requirements in `docs/prd.md` §18 are settled; don't reopen them without the owner.
- Cite requirement IDs in test names and commit messages.
- Never log prompt or response content, and never commit secrets (`make fixtures-check` scans fixtures).
- UI changes follow design.md, using tokens only, and must be checked in both themes.
- Admin API changes: edit `internal/control/openapi.yaml`, then `cd web && npm run api:generate`. SQL changes: `make sqlc`.
