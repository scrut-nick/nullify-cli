#!/bin/bash
# Start the Nullify MCP server, accepting either credential model.
#
# NULLIFY_TOKEN (preferred for cloud sessions)
#   A long-lived API token. The CLI uses it directly, so there is no refresh
#   cycle to go stale and nothing to mint through a browser. This is the right
#   model for an ephemeral container: the token in the secret store is the only
#   state, and it stays valid until it is deliberately rotated.
#
# NULLIFY_CREDENTIALS_JSON (fallback)
#   The contents of ~/.nullify/credentials.json from a machine where
#   'nullify auth login' was run. Seeding it lets the CLI's refresh flow mint
#   access tokens for the refresh token's lifetime (~30 days). The catch is
#   that refreshes are written to the container's filesystem and lost when it
#   exits, so the stored copy ages out and eventually stops working - at which
#   point a human has to re-run an interactive browser login.
#
# NULLIFY_TOKEN wins when both are present: the CLI would prefer it anyway
# (it outranks stored credentials), so seeding a file we would not read only
# creates a second thing to keep in sync.
set -uo pipefail

if [ -n "${NULLIFY_TOKEN:-}" ]; then
  echo "nullify-serve: authenticating with NULLIFY_TOKEN" >&2
else
  if [ -n "${NULLIFY_CREDENTIALS_JSON:-}" ] && [ ! -s "$HOME/.nullify/credentials.json" ]; then
    mkdir -p "$HOME/.nullify"
    chmod 700 "$HOME/.nullify"
    printf '%s' "$NULLIFY_CREDENTIALS_JSON" > "$HOME/.nullify/credentials.json"
    chmod 600 "$HOME/.nullify/credentials.json"
  fi

  if [ -s "$HOME/.nullify/credentials.json" ]; then
    echo "nullify-serve: authenticating with seeded credentials file" >&2
  else
    {
      echo "nullify-serve: WARNING no Nullify credentials available."
      echo "nullify-serve:   set NULLIFY_TOKEN (preferred) or NULLIFY_CREDENTIALS_JSON"
      echo "nullify-serve:   in the 1Password item named by CLAUDE_OP_VAULT/CLAUDE_OP_ITEM."
      echo "nullify-serve:   the server will start but expose no tools."
    } >&2
  fi
fi
unset NULLIFY_CREDENTIALS_JSON

# Seed config.json too. 'mcp serve' reads NULLIFY_HOST directly, but leaving
# config.json absent means anyone debugging inside the container gets
# "Not configured" from 'nullify auth status' even though the credentials are
# right there - which is exactly the wrong signal when auth is what's broken.
if [ -n "${NULLIFY_HOST:-}" ] && [ ! -s "$HOME/.nullify/config.json" ]; then
  mkdir -p "$HOME/.nullify"
  chmod 700 "$HOME/.nullify"
  printf '{\n  "host": "%s"\n}\n' "$NULLIFY_HOST" > "$HOME/.nullify/config.json"
  chmod 600 "$HOME/.nullify/config.json"
fi

exec go run ./cmd/cli mcp serve
