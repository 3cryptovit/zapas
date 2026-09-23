#!/usr/bin/env bash
# Deploys Zapas to the production server.
#
# Builds the Linux binary and the static bundles, ships them, runs
# migrations and restarts the services. Safe to re-run.
#
#   ZAPAS_SSH_KEY=~/.ssh/zapas scripts/deploy.sh
set -euo pipefail

cd "$(dirname "$0")/.."

: "${ZAPAS_SSH_KEY:?ZAPAS_SSH_KEY is not set}"
HOST="${ZAPAS_SSH_HOST:-85.198.64.102}"
REMOTE="scripts/remote.sh"

echo "==> building linux binary"
# MSYS_NO_PATHCONV stops Git Bash from rewriting /src into a Windows path
# before docker sees it.
MSYS_NO_PATHCONV=1 docker run --rm \
  -v "$(pwd -W 2>/dev/null || pwd)":/src \
  -v zapas-gomod:/go/pkg/mod \
  -v zapas-gocache:/root/.cache/go-build \
  -w //src \
  -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=0 -e GOOS=linux -e GOARCH=amd64 \
  golang:1.26 go build -trimpath -ldflags="-s -w" -o //src/bin/app-linux ./cmd/app

echo "==> building web"
(cd web && npm run build >/dev/null 2>&1)

echo "==> building landing"
(cd landing && npm run build >/dev/null 2>&1)

echo "==> uploading"
$REMOTE put bin/app-linux /srv/zapas/bin/app.new
$REMOTE sync web/dist /srv/zapas/web
$REMOTE sync landing/dist /srv/zapas/landing

echo "==> migrating and restarting"
$REMOTE run "set -e
  chown zapas:zapas /srv/zapas/bin/app.new && chmod 755 /srv/zapas/bin/app.new
  chown -R zapas:zapas /srv/zapas/web /srv/zapas/landing
  set -a; . /etc/zapas/zapas.env; set +a
  /srv/zapas/bin/app.new migrate up 2>&1 | tail -2
  # The binary is swapped only after migrations succeed: a failed
  # migration leaves the running version untouched.
  # The previous build is kept so a rollback does not need a rebuild.
  [ -f /srv/zapas/bin/app ] && cp -p /srv/zapas/bin/app /srv/zapas/bin/app.prev
  mv /srv/zapas/bin/app.new /srv/zapas/bin/app
  systemctl restart zapas-api zapas-worker
  sleep 3
  systemctl is-active zapas-api zapas-worker"

echo "==> smoke"
BASE="https://zapas.$HOST.nip.io"

for path in /healthz /readyz /app/ /; do
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 20 "$BASE$path")
  echo "  $path: $code"
  [ "$code" = "200" ] || { echo "SMOKE FAILED"; exit 1; }
done

# A page that returns 200 but cannot load its own scripts is a white
# screen, and /healthz knows nothing about it. So pull the asset URLs
# out of the real HTML and fetch every one of them.
echo "  assets:"
assets=$(curl -s --max-time 20 "$BASE/app/"   | grep -oE '(src|href)="/app/assets/[^"]+"'   | cut -d'"' -f2 | sort -u)

[ -n "$assets" ] || { echo "SMOKE FAILED: no assets referenced by /app/"; exit 1; }

for a in $assets; do
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 20 "$BASE$a")
  echo "    $a: $code"
  [ "$code" = "200" ] || { echo "SMOKE FAILED"; exit 1; }
done

echo "==> deployed"
