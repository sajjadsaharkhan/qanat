# Qanat

A self-hosted, multi-user gateway that Claude Code talks to as if it were the Anthropic API. It pools Claude, Codex and Antigravity subscription accounts, enforces per-user dollar quotas, and streams every byte the moment it arrives.

Connect Claude Code:

```json
{ "env": { "ANTHROPIC_BASE_URL": "https://qanat.example.com", "ANTHROPIC_AUTH_TOKEN": "qnt_live_…" } }
```

## Status

Pre-implementation. This repository holds the product documents and the **testing infrastructure**, built first so every requirement has a gate before its code exists. P0 starts with `cmd/gateway`.

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
cmd/            loadgen, mockupstream, oidcmock (test tools); gateway, control come in P0
internal/       testkit: sse, mockupstream, latency, golden, oidctest, itenv
test/           acceptance suite (Seam A), recorded upstream fixtures
deploy/test/    compose test stack, Nginx SSE config, tools Dockerfile
web/            dashboard test setup, design tokens (globals.css), shared lib
```

## Rules for agents working here

- Requirements in `docs/prd.md` §18 are settled; don't reopen them without the owner.
- Cite requirement IDs in test names and commit messages.
- Never log prompt or response content, and never commit secrets (`make fixtures-check` scans fixtures).
- UI changes follow design.md, using tokens only, and must be checked in both themes.
