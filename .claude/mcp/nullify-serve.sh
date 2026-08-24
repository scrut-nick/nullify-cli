#!/bin/bash
# Start the Nullify MCP server with auto-refreshing credentials.
#
# NULLIFY_CREDENTIALS_JSON (resolved from 1Password by with-op-env.sh) holds
# the contents of ~/.nullify/credentials.json from a machine where
# 'nullify auth login' was run. Seeding that file lets the CLI's built-in
# refresh flow mint fresh access tokens for the refresh token's lifetime
# (~30 days), instead of relying on a static hourly token.
#
# NOTE: an env NULLIFY_TOKEN overrides stored credentials in the CLI - do not
# set it anywhere (environment settings or 1Password) when using this flow.
set -uo pipefail

if [ -n "${NULLIFY_CREDENTIALS_JSON:-}" ] && [ ! -s "$HOME/.nullify/credentials.json" ]; then
  mkdir -p "$HOME/.nullify"
  chmod 700 "$HOME/.nullify"
  printf '%s' "$NULLIFY_CREDENTIALS_JSON" > "$HOME/.nullify/credentials.json"
  chmod 600 "$HOME/.nullify/credentials.json"
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
