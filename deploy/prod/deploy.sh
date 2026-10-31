#!/usr/bin/env bash
# deploy.sh: pull Qanat images from GHCR and restart the server stack.
# Run from the repository root (CI does; see .github/workflows/ci.yml).
#
#   deploy/prod/deploy.sh             # deploy `latest`
#   deploy/prod/deploy.sh abc1234     # deploy (or roll back to) a commit's images
#   deploy/prod/deploy.sh --no-pull   # restart with images already on the server
#   deploy/prod/deploy.sh --dry-run   # show what would be deployed; change nothing
#   IMAGE_TAG=abc1234 deploy/prod/deploy.sh
#
# Server requirements: Docker with compose v2, make, openssl, curl,
# deploy/prod/.env (from .env.example), and `docker login ghcr.io` when the
# packages are private (docs/deploy.md).
#
# Control applies database migrations when it starts. They only move forward,
# so rolling back across a migration can leave an old image on a newer schema.

set -euo pipefail

cd "$(dirname "$0")/../.."
ENV_FILE=deploy/prod/.env
COMPOSE="docker compose -f deploy/prod/compose.yml"

NO_PULL=false
DRY_RUN=false
for arg in "$@"; do
  case "$arg" in
    --no-pull) NO_PULL=true ;;
    --dry-run) DRY_RUN=true ;;
    --*) echo "Unknown flag: $arg"; exit 1 ;;
    *) TAG_ARG="$arg" ;;
  esac
done

CALLER_TAG="${IMAGE_TAG:-}"
[ -f "$ENV_FILE" ] || { echo "✗ $ENV_FILE is missing: copy deploy/prod/.env.example and fill it in"; exit 1; }
set -a
# shellcheck source=/dev/null
. "$ENV_FILE"
set +a
: "${QANAT_DOMAIN:?set QANAT_DOMAIN in $ENV_FILE}"
PORT="${QANAT_HTTP_PORT:-80}"
# Precedence: argument, then the IMAGE_TAG the caller exported, then .env.
export IMAGE_TAG="${TAG_ARG:-${CALLER_TAG:-${IMAGE_TAG:-latest}}}"

echo ""
echo "▶ Deploying Qanat: tag ${IMAGE_TAG}, domain ${QANAT_DOMAIN}"
$COMPOSE config --images | sed 's/^/    /'
echo ""

if [ "$DRY_RUN" = true ]; then
  echo "✓ Dry run: nothing pulled or restarted"
  exit 0
fi

# Secrets that don't exist yet are generated; existing ones are never touched.
make -s prod-secrets

# Pull first: a failed pull leaves the running version untouched. Images
# only come from GHCR; the server never builds them.
if [ "$NO_PULL" = false ]; then
  echo "▶ Pulling images from GHCR..."
  if ! $COMPOSE pull --quiet; then
    echo "✗ Pull failed. Check that CI published tag ${IMAGE_TAG} (Actions → ci → docker)"
    echo "  and, for private packages, run once: docker login ghcr.io -u <github-user>"
    echo "  with a token that has read:packages. The running version is untouched."
    exit 1
  fi
else
  echo "▶ Skipping pull (--no-pull)"
fi

# Only containers whose image or config changed are recreated. PostgreSQL and
# Redis keep running; the gateway drains open streams first (stop_grace_period).
echo "▶ Starting services..."
$COMPOSE up -d --no-build --remove-orphans --wait --wait-timeout 900

# Smoke test through nginx, the way users reach it.
echo "▶ Checking routes through nginx..."
check() { # path, expected status
  local code
  code=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: ${QANAT_DOMAIN}" "http://127.0.0.1:${PORT}$1" || true)
  if [ "$code" = "$2" ]; then
    echo "  ✓ $1 → $code"
  else
    echo "  ✗ $1 → $code (want $2)"
    return 1
  fi
}
ok=true
check /sign-in 200 || ok=false
check /api/auth/methods 200 || ok=false
check /gateway/readyz 200 || ok=false
check /gateway/metrics 404 || ok=false
if [ "$ok" = false ]; then
  $COMPOSE ps
  $COMPOSE logs --tail=30 nginx control gateway web
  exit 1
fi

echo "▶ Pruning unused images..."
docker image prune -f > /dev/null

echo ""
echo "✓ Deployed ${IMAGE_TAG}"
echo "  Dashboard  https://${QANAT_DOMAIN}"
echo "  Gateway    https://${QANAT_DOMAIN}/gateway"
echo ""
