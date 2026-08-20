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

if [ "$missing" -eq 1 ] && [ -n "${OP_SERVICE_ACCOUNT_TOKEN:-}" ]; then
  # The session-start hook installs op asynchronously; wait briefly for it
  for _ in $(seq 1 45); do
    command -v op >/dev/null 2>&1 && break
    sleep 2
  done
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
for _ in $(seq 1 45); do
  command -v "$1" >/dev/null 2>&1 && break
  sleep 2
done

exec "$@"
