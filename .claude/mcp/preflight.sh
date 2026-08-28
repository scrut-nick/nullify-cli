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

  # Name the credential model in use, so a failure points at the right secret
  # instead of leaving someone to guess which of the two is live.
  local mode
  if [ -n "${NULLIFY_TOKEN:-}" ]; then
    mode="NULLIFY_TOKEN"

    # A JWT pasted here carries its own verdict, so read it rather than
    # waiting for the API to answer 403. The common mistake is copying the
    # "access_token" out of a credentials.json, which on this provider is an
    # *ID* token that expires in an hour - it authenticates the process but is
    # rejected for API calls, which surfaces as a puzzling 403 on every tool.
    local claims exp token_use
    claims="$(printf '%s' "$NULLIFY_TOKEN" | jq -R 'split(".") | if length == 3 then .[1] | @base64d | fromjson else empty end' 2>/dev/null)"
    if [ -n "$claims" ]; then
      exp="$(printf '%s' "$claims" | jq -r '.exp // empty' 2>/dev/null)"
      token_use="$(printf '%s' "$claims" | jq -r '.token_use // empty' 2>/dev/null)"

      if [ -n "$exp" ] && [ "$exp" -lt "$(date +%s)" ] 2>/dev/null; then
        fail "$name" "NULLIFY_TOKEN expired on $(date -u -d "@$exp" '+%Y-%m-%d %H:%M UTC' 2>/dev/null) - it needs a token that does not expire, not one copied from a login session"
        return
      fi
      if [ "$token_use" = "id" ]; then
        fail "$name" "NULLIFY_TOKEN is an ID token (token_use=id), which the API rejects - use an API token, not the access_token field of a credentials.json"
        return
      fi
    fi
  elif [ -s "$HOME/.nullify/credentials.json" ]; then
    mode="seeded credentials"
  else
    fail "$name" "no credentials at all - set NULLIFY_TOKEN (preferred for cloud) or NULLIFY_CREDENTIALS_JSON"
    return
  fi

  local out count
  out="$(NULLIFY_HOST="${NULLIFY_HOST:-scrut.nullify.ai}" mcp_exchange "" "$TMP_BIN" mcp serve)"
  count="$(printf '%s\n' "$out" | json_lines | tool_count)"

  if [ "${count:-0}" -gt 0 ]; then
    pass "$name" "$count tools, via $mode"
    return
  fi

  local reason
  reason="$(stderr_reason)"
  case "$reason" in
    *"refresh failed"*|*"token expired"*|*"auth login"*)
      fail "$name" "$mode rejected - a seeded refresh token cannot be renewed from a container; set NULLIFY_TOKEN instead. ($reason)" ;;
    "")
      fail "$name" "exposed no tools and gave no error (using $mode)" ;;
    *)
      fail "$name" "$reason (using $mode)" ;;
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

  local out count
  out="$(mcp_exchange "" latent-defense-mcp)"
  count="$(printf '%s\n' "$out" | json_lines | tool_count)"

  if [ "${count:-0}" -eq 0 ]; then
    fail "$name" "exposed no tools. $(stderr_reason)"
    return
  fi

  # Probe a real data endpoint, not /auth/me and not the whoami tool.
  #
  # /auth/me serves device-flow sessions only: it answers 401 to a perfectly
  # good API key, so testing it reports a false failure. whoami is worse - it
  # calls /auth/me, reads the 401 as unauthenticated, and then blocks on the
  # interactive device flow until the timeout.
  #
  # /api/infra/records is the honest check: it discriminates cleanly, with a
  # valid key returning 200 while a missing or bogus key returns 401.
  if [ -z "${LATENT_DEFENSE_URL:-}" ]; then
    warn "$name" "$count tools, but LATENT_DEFENSE_URL is unset - cannot verify authorization"
    return
  fi
  if [ -z "${LATENT_DEFENSE_API_KEY:-}" ]; then
    fail "$name" "$count tools but LATENT_DEFENSE_API_KEY is unset - every call will be unauthorized"
    return
  fi

  local code
  code="$(curl -sS -o /dev/null -w '%{http_code}' -m 25 \
    -H "Authorization: Bearer ${LATENT_DEFENSE_API_KEY}" \
    -H "Accept: application/json" \
    "${LATENT_DEFENSE_URL%/}/api/infra/records" 2>/dev/null)"

  case "$code" in
    200)
      pass "$name" "$count tools, authenticated" ;;
    401|403)
      fail "$name" "$count tools but key REJECTED (HTTP $code) - rotate LATENT_DEFENSE_API_KEY; tools return empty results, not errors" ;;
    "")
      warn "$name" "$count tools, but ${LATENT_DEFENSE_URL} was unreachable" ;;
    *)
      warn "$name" "$count tools, unexpected HTTP $code from /auth/me" ;;
  esac
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
