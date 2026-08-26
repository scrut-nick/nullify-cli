#!/bin/bash
# Seed ~/.nullify/credentials.json from NULLIFY_CREDENTIALS_JSON (resolved from
# 1Password by with-op-env.sh or the session-start hook).
#
# The CLI rewrites this file itself every time it refreshes, rotating the
# refresh token as it goes, so the on-disk copy can legitimately be NEWER than
# the one in 1Password — a blind overwrite would throw away working
# credentials. The old "seed only when the file is missing" rule avoided that
# but had the opposite failure: once a long-lived container's file went stale,
# a fresh copy in 1Password was never picked up, and every session died with
#
#   stored credentials: token expired and refresh failed ... status 401
#
# until the container was replaced. So: seed when there is nothing to lose, and
# otherwise replace only credentials that have been proven dead.
#
#   file missing or empty   -> seed
#   stored credentials work -> leave them alone (may be newer than 1Password)
#   stored credentials dead -> replace them, if 1Password holds something else
#
# Usage: seed-nullify-credentials.sh [--no-probe]
#   --no-probe  handle only the missing/empty case, skipping the CLI probe
#               (which costs a Go build on a cold container)
set -uo pipefail

# stdout belongs to the MCP stdio transport — diagnostics go to stderr.
log() { echo "seed-nullify-credentials: $*" >&2; }

probe=1
[ "${1:-}" = "--no-probe" ] && probe=0

creds_dir="$HOME/.nullify"
creds="$creds_dir/credentials.json"
project="${CLAUDE_PROJECT_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"

if [ -z "${NULLIFY_CREDENTIALS_JSON:-}" ]; then
  [ -s "$creds" ] || log "no NULLIFY_CREDENTIALS_JSON and no stored credentials"
  exit 0
fi

write_creds() {
  mkdir -p "$creds_dir"
  chmod 700 "$creds_dir"
  printf '%s' "$NULLIFY_CREDENTIALS_JSON" > "$creds"
  chmod 600 "$creds"
}

# Can the stored credentials still mint an access token? 'auth token' exits
# non-zero when they cannot, which covers an expired access token whose refresh
# is rejected, a malformed file, and a host with no credentials at all.
probe_creds() {
  (cd "$project" && go run ./cmd/cli auth token 2>&1 >/dev/null)
}

if [ ! -s "$creds" ]; then
  write_creds
  log "seeded credentials from 1Password"
  exit 0
fi

[ "$probe" -eq 1 ] || exit 0

if err="$(probe_creds)"; then
  exit 0
fi

if printf '%s' "$NULLIFY_CREDENTIALS_JSON" | cmp -s - "$creds"; then
  log "stored credentials rejected (${err%%$'\n'*})"
  log "1Password holds the same copy — run 'nullify auth login' and update" \
      "NULLIFY_CREDENTIALS_JSON in the vault item"
  exit 0
fi

write_creds
log "stored credentials rejected (${err%%$'\n'*}); replaced with the 1Password copy"

if err="$(probe_creds)"; then
  log "the 1Password copy authenticates — recovered"
else
  log "the 1Password copy is rejected too (${err%%$'\n'*}) — run" \
      "'nullify auth login' and update NULLIFY_CREDENTIALS_JSON in the vault item"
fi
