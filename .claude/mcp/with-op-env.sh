#!/bin/bash
# Launch an MCP server, first resolving missing env vars from 1Password.
#
# Usage: with-op-env.sh VAR1 [VAR2 ...] -- command [args...]
#
# The SessionStart hook resolves the same secrets asynchronously, but MCP
# servers can launch before it finishes; this wrapper makes each server
# self-sufficient. Vars already set in the environment are left untouched.
# Vault/item default to "Claude"/"cloud-session-env" and can be overridden
# with CLAUDE_OP_VAULT / CLAUDE_OP_ITEM (item ID works too).
set -uo pipefail

vars=()
while [ $# -gt 0 ] && [ "$1" != "--" ]; do
  vars+=("$1")
  shift
done
shift || true

missing=0
for var in "${vars[@]}"; do
  [ -z "${!var:-}" ] && missing=1
done

# Wait for a command the session-start hook installs asynchronously. Polling
# finely matters here: MCP startup is measured in seconds, and a 2s tick spent
# most of its time waiting on something that had already arrived.
wait_for() { # wait_for <command> <max-seconds>
  local ticks=$(( $2 * 2 ))
  while [ "$ticks" -gt 0 ]; do
    command -v "$1" >/dev/null 2>&1 && return 0
    sleep 0.5
    ticks=$(( ticks - 1 ))
  done
  command -v "$1" >/dev/null 2>&1
}

if [ "$missing" -eq 1 ] && [ -n "${OP_SERVICE_ACCOUNT_TOKEN:-}" ]; then
  # The session-start hook installs op asynchronously; give it a moment, then
  # install it ourselves rather than missing the MCP startup window.
  if ! wait_for op 6; then
    OP_CLI_VERSION="v2.31.1"
    # Unpack elsewhere and rename into place: the hook may be installing the
    # same binary right now, and a half-written op on PATH is worse than a
    # missing one.
    curl -sSfLo /tmp/op-mcp.zip \
      "https://cache.agilebits.com/dist/1P/op2/pkg/${OP_CLI_VERSION}/op_linux_amd64_${OP_CLI_VERSION}.zip" \
      && unzip -oq /tmp/op-mcp.zip -d /tmp/op-mcp op \
      && install -m 0755 "/tmp/op-mcp/op" "/usr/local/bin/.op.$$" \
      && mv -f "/usr/local/bin/.op.$$" /usr/local/bin/op \
      || true
  fi
  if command -v op >/dev/null 2>&1; then
    vault="${CLAUDE_OP_VAULT:-Claude}"
    item="${CLAUDE_OP_ITEM:-cloud-session-env}"
    for var in "${vars[@]}"; do
      if [ -z "${!var:-}" ]; then
        val="$(op read "op://${vault}/${item}/${var}" 2>/dev/null || true)"
        if [ -n "$val" ]; then
          export "$var=$val"
        fi
      fi
    done
  else
    echo "with-op-env: op CLI not available; starting without 1Password lookups" >&2
  fi
fi

# The target command may also be installed by the async hook; wait briefly
wait_for "$1" 90 || true

exec "$@"
