#!/usr/bin/env bash
# Helper for the production server. Usage:
#   scripts/remote.sh run "docker ps"
#   scripts/remote.sh put local/file /remote/path
#   scripts/remote.sh sync local/dir /remote/dir
set -euo pipefail

KEY="${ZAPAS_SSH_KEY:?ZAPAS_SSH_KEY is not set}"
HOST="${ZAPAS_SSH_HOST:-85.198.64.102}"
USER="${ZAPAS_SSH_USER:-root}"

SSH_OPTS=(-i "$KEY" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR)

case "${1:-}" in
  run)  shift; ssh "${SSH_OPTS[@]}" "$USER@$HOST" "$@" ;;
  put)  shift; scp "${SSH_OPTS[@]}" "$1" "$USER@$HOST:$2" ;;
  sync) shift; scp -r "${SSH_OPTS[@]}" "$1"/. "$USER@$HOST:$2" ;;
  *) echo "usage: remote.sh {run|put|sync} ..." >&2; exit 2 ;;
esac
