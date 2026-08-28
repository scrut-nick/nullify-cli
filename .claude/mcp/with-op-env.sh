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

# A var may be marked optional by prefixing it with "?". Optional vars are
# still resolved from 1Password, but their absence is not reported: they exist
# for servers that accept more than one credential model, where exactly one of
# several vars is expected to be present and the server itself decides which
# it got. Everything without the prefix is required, as before.
vars=()
required=()
while [ $# -gt 0 ] && [ "$1" != "--" ]; do
  case "$1" in
    '?'*) vars+=("${1#\?}") ;;
    *)    vars+=("$1"); required+=("$1") ;;
  esac
  shift
done
shift || true

missing=0
for var in "${vars[@]}"; do
  [ -z "${!var:-}" ] && missing=1
done

if [ "$missing" -eq 1 ] && [ -n "${OP_SERVICE_ACCOUNT_TOKEN:-}" ]; then
  # The session-start hook installs op asynchronously; wait briefly, then
  # install it ourselves rather than missing the MCP startup window.
  for _ in $(seq 1 10); do
    command -v op >/dev/null 2>&1 && break
    sleep 2
  done
  if ! command -v op >/dev/null 2>&1; then
    OP_CLI_VERSION="v2.31.1"
    curl -sSfLo /tmp/op-mcp.zip \
      "https://cache.agilebits.com/dist/1P/op2/pkg/${OP_CLI_VERSION}/op_linux_amd64_${OP_CLI_VERSION}.zip" \
      && unzip -oq /tmp/op-mcp.zip -d /usr/local/bin op \
      && chmod +x /usr/local/bin/op \
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

# Report anything still unresolved before handing over. A server started
# without its credentials does not fail in a way anyone notices: it connects
# and then exposes no tools, or answers every call "not authenticated". Naming
# the gap here is the difference between a five-minute fix and an outage that
# survives until someone audits tool coverage by hand.
still_missing=()
for var in "${required[@]}"; do
  [ -z "${!var:-}" ] && still_missing+=("$var")
done

if [ "${#still_missing[@]}" -gt 0 ]; then
  {
    echo "with-op-env: WARNING starting '${1:-<none>}' with unresolved secrets: ${still_missing[*]}"
    if [ -z "${OP_SERVICE_ACCOUNT_TOKEN:-}" ]; then
      echo "with-op-env:   cause: OP_SERVICE_ACCOUNT_TOKEN is not set, so 1Password was never consulted."
    else
      echo "with-op-env:   looked up: op://${CLAUDE_OP_VAULT:-Claude}/${CLAUDE_OP_ITEM:-cloud-session-env}/<VAR>"
      echo "with-op-env:   cause: those fields are missing from the item, or the service account cannot read them."
    fi
    echo "with-op-env:   expect this server to start but expose no working tools."
  } >&2
fi

# The target command may also be installed by the async hook; wait briefly
for _ in $(seq 1 45); do
  command -v "$1" >/dev/null 2>&1 && break
  sleep 2
done

if ! command -v "$1" >/dev/null 2>&1; then
  echo "with-op-env: WARNING '$1' is not on PATH after waiting 90s; exec will fail." >&2
fi

exec "$@"
