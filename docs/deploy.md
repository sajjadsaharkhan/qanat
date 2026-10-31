# Deploying Qanat on a server

One hostname, one port. nginx routes by path:

| URL | Goes to |
|---|---|
| `https://<domain>/` | dashboard (`web`) |
| `https://<domain>/api/` | control plane (`control`), paths unchanged |
| `https://<domain>/gateway/` | gateway, `/gateway` stripped (`/gateway/v1/messages` → `/v1/messages`) |

TLS ends **in front of** nginx (Cloudflare or another proxy); nginx listens on
plain HTTP. Only nginx publishes a port. PostgreSQL, Redis, control and the
gateway are reachable only on the compose network. Keycloak is your existing
instance (PRD §12) and is not part of the stack.

The files are in `deploy/prod/`: `compose.yml`, `nginx/default.conf.template`,
`.env.example`, `deploy.sh`. CI/CD is in `.github/workflows/ci.yml` (see §9).

## 1. Server

- Docker with the compose plugin, `make`, `openssl`, `git`.
- Host it near the providers' endpoints, in Europe or the US (PRD §12.2).
- Firewall: open only the port nginx listens on, and only to your TLS proxy
  if you can (Cloudflare publishes its IP ranges).

## 2. Configure

```sh
git clone <repo> qanat && cd qanat
cp deploy/prod/.env.example deploy/prod/.env
$EDITOR deploy/prod/.env
```

| Variable | Meaning |
|---|---|
| `QANAT_DOMAIN` | Public hostname, without scheme or path, e.g. `qanat.example.com`. Requests for any other Host are dropped. |
| `QANAT_HTTP_PORT` | Host port nginx listens on (default `80`). |
| `QANAT_OIDC_ISSUER` | Keycloak realm URL, e.g. `https://sso.example.com/realms/main`. Empty = root password sign-in only. |
| `QANAT_OIDC_CLIENT_ID` | Keycloak client ID (default `qanat-dashboard`). |
| `BOOTSTRAP_ADMIN_EMAIL` | This Keycloak user becomes admin on first sign-in. |

The dashboard URL (`https://<domain>`) and the gateway URL shown to users
(`https://<domain>/gateway`) are built from `QANAT_DOMAIN`.

### Secrets

Secrets are files in `deploy/prod/secrets/`, not in `.env`. `make prod-up`
creates any that are missing:

| File | Contents |
|---|---|
| `root_password` | Password for the built-in `root` admin. |
| `master_key` | Seals upstream credentials at rest. **Back it up off the server.** Without it, stored provider accounts can't be read. |
| `postgres_password`, `postgres_dsn` | Database password and the DSN built from it. |
| `oidc_client_secret` | Empty for a public Keycloak client. Write the secret here if the client is confidential. |

The folder is `0700`, and the files in it are `0644` so the containers' users
can read them. Don't loosen the folder's permissions.

## 3. Keycloak client

In your existing realm (PRD §13):

- **Client ID:** the value of `QANAT_OIDC_CLIENT_ID`. Standard flow on. PKCE method `S256`.
- **Valid redirect URIs:** `https://<domain>/api/auth/oidc/callback`
- **Web origins:** `https://<domain>`
- **Valid post-logout redirect URIs:** `https://<domain>/*`
- **Group gate:** create the group `qanat-users`. Only its members can sign in.
- **Groups claim:** add a *Group Membership* mapper with claim name `groups`,
  *Full group path* off, and *Add to ID token* on.

The control container must be able to reach the issuer URL over HTTPS.

## 4. TLS proxy (Cloudflare)

- DNS: an `A` record for `<domain>`, proxied, pointing at the server.
- SSL/TLS mode **Full**. Use **Full (strict)** with an origin certificate only
  if you also terminate TLS on the server; this setup doesn't.
- Disable caching for `/api/*` and `/gateway/*`. The default rules don't cache
  them, so this only matters if you've added a cache-everything rule.
- Claude Code streams with periodic pings, so Cloudflare's 100 s idle timeout
  doesn't apply in practice. Request bodies are capped at 100 MB on the free
  plan, and nginx caps them at 32 MB.

The proxy must send `X-Forwarded-Proto: https`. Cloudflare does. Session
cookies are `Secure`, so signing in over plain `http://` doesn't work.

## 5. Start

The server never builds images. CI builds them and publishes them to GHCR
(§9); the server only pulls. If the packages are private, log Docker in once
with a token that has only `read:packages`:
`docker login ghcr.io -u <github-user>`.

```sh
make prod-up      # pulls images from GHCR, creates secrets, waits until healthy
make prod-logs
make prod-down    # stop; data stays in the postgres/redis volumes
```

Sign in at `https://<domain>` as `root` with the password in
`deploy/prod/secrets/root_password`, or with Keycloak.

Claude Code users set:

```sh
export ANTHROPIC_BASE_URL=https://<domain>/gateway
export ANTHROPIC_AUTH_TOKEN=<their Qanat key>
```

The dashboard's Setup page shows these values for each user.

## 6. Update

With CI/CD (§9), every push to `main` deploys itself. By hand:

```sh
git pull && make prod-deploy            # pull the CI images for `latest`
make prod-deploy TAG=abc1234            # pin, or roll back to a commit's images
deploy/prod/deploy.sh --dry-run         # show which images would run
```

Control applies database migrations when it starts. Migrations only go
forward, so roll back across a migration only after restoring a backup (§7).

The gateway drains open streams for up to 10 minutes before stopping
(`stop_grace_period`).

## 7. Backups

```sh
make prod-backup   # pg_dump -Fc into deploy/prod/backups/
```

Run it daily from cron (PRD §12) and copy the dumps off the server along with
`deploy/prod/secrets/master_key`. Restore:

```sh
docker compose -f deploy/prod/compose.yml exec -T postgres \
  pg_restore -U qanat -d qanat --clean --if-exists < deploy/prod/backups/<file>.dump
```

## 8. Monitoring

`/gateway/metrics` returns 404 at the edge. Prometheus must scrape
`gateway:8080/metrics` from inside the compose network, or through a port you
publish only to a private interface.

## 9. CI/CD (GitHub Actions)

`.github/workflows/ci.yml`:

1. **detect changes.** A backend change (`cmd`, `internal`, `go.mod`,
   `Dockerfile`, …) runs the Go jobs and rebuilds the `qanat` image. A `web/`
   change runs the dashboard jobs and rebuilds the `web` image. A manual run
   (*Run workflow*) does everything.
2. **Tests:** `go`, `integration`, `acceptance` (with the latency gate) and
   `web` (with e2e).
3. **docker:** on pushes only. Builds the changed images and pushes them to
   `ghcr.io/<owner>/<repo>/qanat` and `/web`, tagged with the short SHA and the
   branch name, plus `latest` on `main`.
4. **deploy:** on pushes to `main` only, after the images are pushed, or when
   only `deploy/prod/` changed. It runs on a **self-hosted runner on the
   server**: it resets the server checkout to `origin/main`, then runs
   `deploy/prod/deploy.sh`, which pulls `latest`, recreates only the changed
   containers and checks the routes through nginx.

Pull requests run the tests only. Nothing is pushed or deployed.

### One-time setup

On GitHub:

- **Environment** `production` (Settings → Environments). Add required
  reviewers there if deploys should wait for approval.
- **Repository variable** `DEPLOY_PATH`: the absolute path of the checkout on
  the server, e.g. `/home/deploy/qanat`.

On the server:

1. Clone the repo to `DEPLOY_PATH`, then fill in `deploy/prod/.env` (§2) and
   the Keycloak client (§3). Untracked files survive the deploy job's
   `git reset --hard`, so `.env`, `secrets/` and `backups/` are safe.
2. Install a GitHub Actions runner (Settings → Actions → Runners → New
   self-hosted runner) as a service, under a user that owns `DEPLOY_PATH` and
   is in the `docker` group. The default `self-hosted` label is enough.
3. If the packages are private, log Docker in once as the runner user, using
   a token with only `read:packages`:
   `docker login ghcr.io -u <github-user>`.
4. Run the first deploy by hand (`make prod-deploy`), or start the workflow
   manually.

**Keep the repository private, or restrict the runner.** On a public repo, a
pull request from a fork could run its own code on a self-hosted runner. If
the repo is public, put the runner in a runner group limited to this
workflow on `main`, and require approval for fork pull requests.
