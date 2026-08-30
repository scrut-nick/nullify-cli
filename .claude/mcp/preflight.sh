#!/bin/bash
# Verify every MCP server in .mcp.json is not just running but usable, and
# leave a machine-readable record of the answer.
#
#   .claude/mcp/preflight.sh          human table + health.json
#   make preflight                    same
#
# Why this exists: an MCP server almost never fails loudly. It either dies
# during startup and shows up in the client as a server with no tools, or it
# starts, advertises its whole tool set, and answers every call with an empty
# result because its credential is rejected. Both look like "nothing to report"
# rather than "outage", and the second is worse - work gets published against
# data that was never there.
#
# So each server gets two checks:
#   1. handshake     does it start and list tools?
#   2. authorization is its credential actually accepted?
#
# The server list comes from .mcp.json, never from a hand-kept list here. A
# server with no probe in health-probes.json is reported UNCHECKED, so adding
# one cannot quietly add a blind spot.
#
# Exit status: 0 only when every server passes, so this can gate a check.
set -uo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/../.." || exit 1

MCP_CONFIG="${PREFLIGHT_MCP_CONFIG:-.mcp.json}"
PROBE_CONFIG="${PREFLIGHT_PROBE_CONFIG:-.claude/mcp/health-probes.json}"
HEALTH_OUT="${PREFLIGHT_OUT:-.claude/mcp/health.json}"
TIMEOUT="${PREFLIGHT_TIMEOUT:-120}"

STDERR_FILE="$(mktemp)"
trap 'rm -f "$STDERR_FILE"' EXIT

for dep in jq curl; do
  command -v "$dep" >/dev/null 2>&1 || { echo "preflight: $dep is required" >&2; exit 2; }
done
[ -f "$MCP_CONFIG" ] || { echo "preflight: $MCP_CONFIG not found" >&2; exit 2; }

failures=0
declare -a ROWS=()      # status|server|detail
declare -a JSON_ROWS=()

record() {
  ROWS+=("$1|$2|$3")
  JSON_ROWS+=("$(jq -nc --arg s "$1" --arg n "$2" --arg d "$3" --argjson t "${4:-null}" \
    '{server:$n, status:$s, detail:$d, tools:$t}')")
  [ "$1" = "FAIL" ] && failures=$((failures + 1))
  return 0
}

# ---------------------------------------------------------------- handshake

# Expand the ${VAR:-default} form that MCP client env blocks use.
expand_env_value() {
  local raw="$1"
  if [[ "$raw" =~ ^\$\{([A-Za-z_][A-Za-z0-9_]*):-(.*)\}$ ]]; then
    local name="${BASH_REMATCH[1]}" fallback="${BASH_REMATCH[2]}"
    printf '%s' "${!name:-$fallback}"
  elif [[ "$raw" =~ ^\$\{?([A-Za-z_][A-Za-z0-9_]*)\}?$ ]]; then
    printf '%s' "${!BASH_REMATCH[1]:-}"
  else
    printf '%s' "$raw"
  fi
}

# Start the server exactly as the client would - through the same wrapper and
# arguments - so the wiring itself is under test, not just the binary.
handshake_tool_count() {
  local server="$1"
  local -a cmd=() envs=()
  local key val

  mapfile -t cmd < <(jq -r --arg s "$server" \
    '.mcpServers[$s] | ([.command] + (.args // []))[]' "$MCP_CONFIG" 2>/dev/null)
  [ "${#cmd[@]}" -gt 0 ] || { echo ""; return; }

  while IFS=$'\t' read -r key val; do
    [ -n "$key" ] && envs+=("$key=$(expand_env_value "$val")")
  done < <(jq -r --arg s "$server" \
    '(.mcpServers[$s].env // {}) | to_entries[] | "\(.key)\t\(.value)"' "$MCP_CONFIG" 2>/dev/null)

  {
    echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"preflight","version":"1"}}}'
    echo '{"jsonrpc":"2.0","method":"notifications/initialized"}'
    echo '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
    sleep 2
  } | timeout "$TIMEOUT" env "${envs[@]}" "${cmd[@]}" 2>"$STDERR_FILE" \
    | jq -R 'fromjson? // empty' 2>/dev/null \
    | jq -s 'map(select(.id == 2)) | (.[0].result.tools // []) | length' 2>/dev/null
}

# Last stderr line that looks like a cause rather than a banner.
stderr_reason() {
  local lines cause
  lines="$(grep -v '^[[:space:]]*$' "$STDERR_FILE" 2>/dev/null \
    | grep -viE 'warning:|IncompleteFieldDefinition|warnings\.warn|INFO |Processing request|Starting MCP server|^ +')"
  # "exit status N" is the runner's epilogue and says nothing about why. Prefer
  # the last line that names a cause, and fall back to it only when the process
  # died without explaining itself.
  cause="$(printf '%s\n' "$lines" | grep -vE '^exit status [0-9]+$' | tail -1)"
  [ -z "$cause" ] && cause="$(printf '%s\n' "$lines" | tail -1)"
  printf '%s' "$cause" | cut -c1-150
}

# ------------------------------------------------------------------- probes

probe_field() {
  jq -r --arg s "$1" --arg f "$2" '.probes[$s][$f] // empty' "$PROBE_CONFIG" 2>/dev/null
}

# Inspect a seeded ~/.nullify/credentials.json. Without this, the fallback path
# reports only that the file exists, which is how an expired access token and a
# refresh token the server has already invalidated both read as "using
# credentials.json" - right up until every call comes back empty.
# echoes "<verdict>|<detail>"
probe_credentials_file() {
  local server="$1" file="$2" base host entry access refresh expires now when use reject
  base="$(basename "$file")"
  host="${NULLIFY_HOST:-}"; host="${host#api.}"

  entry="$(jq -c --arg h "$host" \
    'if ($h != "" and has($h)) then .[$h] else (to_entries | .[0].value // empty) end' \
    "$file" 2>/dev/null)"
  if [ -z "$entry" ] || [ "$entry" = "null" ]; then
    echo "bad|$base has no credentials for ${host:-any host}"; return
  fi

  access="$(printf '%s' "$entry"  | jq -r '.access_token // empty')"
  refresh="$(printf '%s' "$entry" | jq -r '.refresh_token // empty')"
  expires="$(printf '%s' "$entry" | jq -r '.expires_at // empty')"
  now="$(date +%s)"

  if [ -z "$access" ]; then echo "bad|$base has no access_token"; return; fi

  if [ -n "$expires" ] && [ "$expires" -lt "$now" ] 2>/dev/null; then
    when="$(date -u -d "@$expires" '+%Y-%m-%d %H:%M UTC' 2>/dev/null)"
    if [ -n "$refresh" ]; then
      # Whether the refresh token is still accepted cannot be read offline, and
      # spending it here would consume a single-use token the server process
      # needs moments later. Report the expiry and let the handshake decide.
      echo "unknown|$base access token expired $when; refresh token present but unverified"
    else
      echo "bad|$base access token expired $when and carries no refresh token"
    fi
    return
  fi

  # An unexpired access token still has to be the right kind of token: a
  # Cognito ID token authenticates the process and is then rejected by the API.
  use="$(printf '%s' "$access" \
    | jq -R 'split(".") | if length == 3 then .[1] | @base64d | fromjson else empty end' 2>/dev/null \
    | jq -r '.token_use // empty' 2>/dev/null)"
  reject="$(jq -r --arg s "$server" '(.probes[$s].reject_token_use // [])[]' "$PROBE_CONFIG" 2>/dev/null | tr '\n' ' ')"
  if [ -n "$use" ] && [[ " $reject " == *" $use "* ]]; then
    echo "bad|$base access_token is a ${use} token, which the API rejects"; return
  fi

  echo "ok|seeded credentials valid${refresh:+ (refresh token present)}"
}

# echoes "<verdict>|<detail>"; verdict is ok, bad or unknown
run_probe() {
  local server="$1" kind
  kind="$(probe_field "$server" kind)"

  case "$kind" in
    http)
      local url_env key_env path ok base key code
      url_env="$(probe_field "$server" url_env)"; key_env="$(probe_field "$server" key_env)"
      path="$(probe_field "$server" path)";       ok="$(probe_field "$server" ok_status)"
      base="${!url_env:-}"; key="${!key_env:-}"
      [ -n "$base" ] || { echo "unknown|$url_env is unset"; return; }
      [ -n "$key" ]  || { echo "bad|$key_env is unset"; return; }
      code="$(curl -sS -o /dev/null -w '%{http_code}' -m 25 \
        -H "Authorization: Bearer ${key}" -H "Accept: application/json" \
        "${base%/}${path}" 2>/dev/null)"
      if [ "$code" = "$ok" ]; then echo "ok|authorized"
      elif [ -z "$code" ] || [ "$code" = "000" ]; then echo "unknown|${base} unreachable"
      else echo "bad|$key_env rejected (HTTP $code)"; fi
      ;;

    jwt-env)
      # A JWT states its own validity, so read it instead of waiting for the
      # API to answer 403 on every call.
      local var tok claims exp use reject file
      var="$(probe_field "$server" var)"; tok="${!var:-}"
      if [ -z "$tok" ]; then
        file="$(probe_field "$server" fallback_file)"; file="${file/#\~/$HOME}"
        if [ -n "$file" ] && [ -s "$file" ]; then probe_credentials_file "$server" "$file"
        else echo "bad|$var unset and no fallback credentials"; fi
        return
      fi
      claims="$(printf '%s' "$tok" | jq -R 'split(".") | if length == 3 then .[1] | @base64d | fromjson else empty end' 2>/dev/null)"
      if [ -z "$claims" ]; then echo "ok|opaque token present"; return; fi
      exp="$(printf '%s' "$claims" | jq -r '.exp // empty')"
      use="$(printf '%s' "$claims" | jq -r '.token_use // empty')"
      if [ -n "$exp" ] && [ "$exp" -lt "$(date +%s)" ] 2>/dev/null; then
        echo "bad|$var expired $(date -u -d "@$exp" '+%Y-%m-%d %H:%M UTC' 2>/dev/null)"; return
      fi
      reject="$(jq -r --arg s "$server" '(.probes[$s].reject_token_use // [])[]' "$PROBE_CONFIG" 2>/dev/null | tr '\n' ' ')"
      if [ -n "$use" ] && [[ " $reject " == *" $use "* ]]; then
        echo "bad|$var is a ${use} token, which the API rejects"; return
      fi
      echo "ok|token valid"
      ;;

    env-present)
      local missing=() v
      while read -r v; do
        [ -n "$v" ] && [ -z "${!v:-}" ] && missing+=("$v")
      done < <(jq -r --arg s "$server" '(.probes[$s].vars // [])[]' "$PROBE_CONFIG" 2>/dev/null)
      if [ "${#missing[@]}" -gt 0 ]; then echo "bad|unset: ${missing[*]}"
      else echo "ok|credentials present"; fi
      ;;

    *) echo "unknown|no probe defined" ;;
  esac
}

# -------------------------------------------------------------------- main

echo "MCP preflight - $(jq -r '.mcpServers | keys | length' "$MCP_CONFIG") server(s) in $MCP_CONFIG"
echo

while read -r server; do
  [ -n "$server" ] || continue

  count="$(handshake_tool_count "$server")"
  count="${count:-0}"
  hint="$(probe_field "$server" hint)"

  if [ "$count" -eq 0 ]; then
    reason="$(stderr_reason)"
    # A dead server usually explains itself on stderr; if the probe knows why,
    # say that instead of the raw message.
    verdict="$(run_probe "$server")"
    if [ "${verdict%%|*}" = "bad" ]; then
      record "FAIL" "$server" "no tools - ${verdict#*|}${hint:+; $hint}"
    elif [ -n "$reason" ]; then
      record "FAIL" "$server" "no tools - $reason"
    else
      record "FAIL" "$server" "no tools and no error reported"
    fi
    continue
  fi

  verdict="$(run_probe "$server")"
  case "${verdict%%|*}" in
    ok)      record "PASS" "$server" "${verdict#*|}" "$count" ;;
    bad)     record "FAIL" "$server" "${count} tools but ${verdict#*|}${hint:+; $hint} - calls return empty results, not errors" "$count" ;;
    unknown) record "WARN" "$server" "${count} tools; ${verdict#*|}" "$count" ;;
  esac
done < <(jq -r '.mcpServers | keys[]' "$MCP_CONFIG")

printf '%-8s %-16s %s\n' "RESULT" "SERVER" "DETAIL"
printf '%-8s %-16s %s\n' "------" "------" "------"
for row in "${ROWS[@]}"; do
  printf '%-8s %-16s %s\n' "${row%%|*}" "$(cut -d'|' -f2 <<<"$row")" "${row##*|}"
done
echo

mkdir -p "$(dirname "$HEALTH_OUT")"
printf '%s\n' "${JSON_ROWS[@]}" | jq -s \
  --arg ts "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" \
  --argjson failures "$failures" \
  '{checked_at:$ts, healthy:($failures == 0), failures:$failures, servers:.}' \
  > "$HEALTH_OUT" 2>/dev/null && echo "Wrote $HEALTH_OUT"

if [ "$failures" -gt 0 ]; then
  echo
  echo "$failures server(s) unusable. Treat their data as ABSENT, not as empty:"
  echo "a rejected credential returns no findings, which looks identical to good news."
  exit 1
fi

echo "All MCP servers reachable and authorized."
