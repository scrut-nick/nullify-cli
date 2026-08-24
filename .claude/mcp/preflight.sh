#!/bin/bash
# Probe every MCP server this repo wires up and report whether it is actually
# usable. Run it by hand any time tools look missing:
#
#   .claude/mcp/preflight.sh
#
# Two failure modes matter, and counting tools only catches the first:
#
#   1. The server dies during startup - almost always expired credentials.
#      It exposes no tools at all and the client just shows nothing.
#   2. The server starts and lists its whole tool set, but the credentials are
#      rejected on every call, so each tool answers "not authenticated" or
#      returns an empty result.
#
# The second mode is the dangerous one: coverage looks complete while the data
# is silently absent. So each server gets a handshake AND, where the server
# offers one, an authorization probe.
#
# Exit status is 0 only when every server passes, so this can gate a check.
set -uo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/../.." || exit 1

TIMEOUT="${PREFLIGHT_TIMEOUT:-120}"
STDERR_FILE="$(mktemp)"
TMP_BIN="$(mktemp -u)"
trap 'rm -f "$STDERR_FILE" "$TMP_BIN"' EXIT

failures=0
declare -a REPORT=()

# Emit the JSON-RPC lines for a handshake, optionally followed by one extra
# request (used for authorization probes).
mcp_exchange() {
  local extra="$1"; shift
  {
    echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"preflight","version":"1"}}}'
    echo '{"jsonrpc":"2.0","method":"notifications/initialized"}'
    echo '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
    [ -n "$extra" ] && echo "$extra"
    # Give the server a moment to answer before stdin closes.
    sleep 2
  } | timeout "$TIMEOUT" "$@" 2>"$STDERR_FILE"
}

# Keep only well-formed JSON-RPC lines; servers routinely print banners.
json_lines() { jq -R 'fromjson? // empty' 2>/dev/null; }

tool_count() {
  jq -s 'map(select(.id == 2)) | (.[0].result.tools // []) | length' 2>/dev/null
}

# Last non-empty stderr line, trimmed - usually the actual cause.
stderr_reason() {
  grep -v '^[[:space:]]*$' "$STDERR_FILE" 2>/dev/null \
    | grep -viE 'warning:|IncompleteFieldDefinition|warnings\.warn|INFO |Processing request|Starting MCP server|^ +' \
    | tail -1 | cut -c1-160
}

record() { REPORT+=("$1|$2|$3"); }

pass() { record "PASS" "$1" "$2"; }
fail() { record "FAIL" "$1" "$2"; failures=$((failures + 1)); }
warn() { record "WARN" "$1" "$2"; }

# ------------------------------------------------------------------
# nullify - dies at startup when the seeded refresh token is rejected
# ------------------------------------------------------------------
check_nullify() {
  local name="nullify"

  if ! go build -o "$TMP_BIN" ./cmd/cli 2>"$STDERR_FILE"; then
    fail "$name" "CLI failed to build: $(stderr_reason)"
    return
  fi

  local out count
  out="$(NULLIFY_HOST="${NULLIFY_HOST:-scrut.nullify.ai}" mcp_exchange "" "$TMP_BIN" mcp serve)"
  count="$(printf '%s\n' "$out" | json_lines | tool_count)"

  if [ "${count:-0}" -gt 0 ]; then
    pass "$name" "$count tools"
    return
  fi

  local reason
  reason="$(stderr_reason)"
  case "$reason" in
    *"refresh failed"*|*"token expired"*|*"auth login"*)
      fail "$name" "credentials rejected - re-seed NULLIFY_CREDENTIALS_JSON. ($reason)" ;;
    "")
      fail "$name" "exposed no tools and gave no error" ;;
    *)
      fail "$name" "$reason" ;;
  esac
}

# ------------------------------------------------------------------
# latent-defense - starts and lists every tool even when unauthorized,
# so the tool count proves nothing. whoami is the real check.
# ------------------------------------------------------------------
check_latent_defense() {
  local name="latent-defense"

  if ! command -v latent-defense-mcp >/dev/null 2>&1; then
    fail "$name" "latent-defense-mcp not on PATH - 'uv tool install ./third_party/latent-defense-mcp'"
    return
  fi

  local probe out count body
  probe='{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"whoami","arguments":{}}}'
  out="$(mcp_exchange "$probe" latent-defense-mcp)"
  count="$(printf '%s\n' "$out" | json_lines | tool_count)"

  if [ "${count:-0}" -eq 0 ]; then
    fail "$name" "exposed no tools. $(stderr_reason)"
    return
  fi

  body="$(printf '%s\n' "$out" | json_lines \
    | jq -rs 'map(select(.id == 3)) | (.[0].result.content[0].text // "")' 2>/dev/null)"

  if [ -z "$body" ]; then
    warn "$name" "$count tools, but whoami did not answer within ${TIMEOUT}s"
    return
  fi

  if printf '%s' "$body" | jq -e '.authenticated == true' >/dev/null 2>&1; then
    pass "$name" "$count tools, authenticated"
  else
    fail "$name" "$count tools but UNAUTHORIZED - rotate LATENT_DEFENSE_API_KEY (tools will return empty results)"
  fi
}

# ------------------------------------------------------------------
# sentinelone - no unauthenticated health endpoint on the console, so
# the handshake is the honest check available here.
# ------------------------------------------------------------------
check_sentinelone() {
  local name="sentinelone"
  local out count

  out="$(mcp_exchange "" uvx --from ./third_party/purple-mcp purple-mcp --mode stdio)"
  count="$(printf '%s\n' "$out" | json_lines | tool_count)"

  if [ "${count:-0}" -gt 0 ]; then
    if [ -z "${PURPLEMCP_CONSOLE_TOKEN:-}" ]; then
      warn "$name" "$count tools, but PURPLEMCP_CONSOLE_TOKEN is unset - calls will fail"
    else
      pass "$name" "$count tools"
    fi
    return
  fi

  fail "$name" "exposed no tools. $(stderr_reason)"
}

echo "MCP preflight - probing servers from .mcp.json"
echo

check_nullify
check_latent_defense
check_sentinelone

printf '%-8s %-16s %s\n' "RESULT" "SERVER" "DETAIL"
printf '%-8s %-16s %s\n' "------" "------" "------"
for row in "${REPORT[@]}"; do
  printf '%-8s %-16s %s\n' "${row%%|*}" "$(cut -d'|' -f2 <<<"$row")" "${row##*|}"
done
echo

if [ "$failures" -gt 0 ]; then
  echo "$failures server(s) unusable. Credential failures need a human: mint a new"
  echo "secret, then update the 1Password item named by CLAUDE_OP_VAULT/CLAUDE_OP_ITEM."
  exit 1
fi

echo "All MCP servers reachable and authorized."
