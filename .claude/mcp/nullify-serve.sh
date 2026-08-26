#!/bin/bash
# Start the Nullify MCP server with auto-refreshing credentials.
#
# NULLIFY_CREDENTIALS_JSON (resolved from 1Password by with-op-env.sh) holds
# the contents of ~/.nullify/credentials.json from a machine where
# 'nullify auth login' was run. Seeding that file lets the CLI's built-in
# refresh flow mint fresh access tokens for the refresh token's lifetime
# (~30 days), instead of relying on a static hourly token.
#
# seed-nullify-credentials.sh decides whether to write the file: it keeps
# credentials the CLI has refreshed on its own, and replaces ones the CLI can
# no longer use. See that script for why both halves matter.
#
# NOTE: an env NULLIFY_TOKEN overrides stored credentials in the CLI - do not
# set it anywhere (environment settings or 1Password) when using this flow.
set -uo pipefail

"$(dirname "${BASH_SOURCE[0]}")/seed-nullify-credentials.sh"
unset NULLIFY_CREDENTIALS_JSON

exec go run ./cmd/cli mcp serve
