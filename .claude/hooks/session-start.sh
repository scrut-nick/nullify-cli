#!/bin/bash
set -euo pipefail

# Only run in Claude Code on the web (remote) sessions
if [ "${CLAUDE_CODE_REMOTE:-}" != "true" ]; then
  exit 0
fi

# Run in the background while the session starts
echo '{"async": true, "asyncTimeout": 600000}'

cd "$CLAUDE_PROJECT_DIR"

# Download Go modules (also fetches the toolchain pinned in go.mod on first run)
go mod download

# Warm the build cache so builds, tests, and lint runs are fast
go build ./... || true

# Install the golangci-lint version pinned in the Dockerfile (same as CI).
# Installed via the Go module proxy; GitHub release downloads are blocked here.
GOLANGCI_LINT_VERSION="$(sh scripts/get_golangci_lint_version.sh)"
# Build it with the same toolchain the module targets, otherwise golangci-lint
# refuses to lint code whose Go version is newer than the one it was built with.
GO_VERSION="$(go mod edit -json | grep -oP '"Go":\s*"\K[0-9.]+')"
if ! golangci-lint version 2>/dev/null | grep -q "version ${GOLANGCI_LINT_VERSION#v}.*go${GO_VERSION}"; then
  GOTOOLCHAIN="go${GO_VERSION}" GOBIN=/usr/local/bin \
    go install "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_LINT_VERSION}"
fi

# --- MCP servers (vendored in third_party/, wired up in .mcp.json) ---

# Latent Defense: install the vendored package into an isolated uv tool env
if ! command -v latent-defense-mcp >/dev/null 2>&1; then
  uv tool install ./third_party/latent-defense-mcp \
    || echo "warning: failed to install latent-defense-mcp" >&2
fi

# SentinelOne Purple AI: pre-build the uvx environment for the vendored package
uvx --from ./third_party/purple-mcp purple-mcp --help >/dev/null 2>&1 \
  || echo "warning: failed to pre-build purple-mcp env" >&2

# --- Secrets via 1Password ---
# Set OP_SERVICE_ACCOUNT_TOKEN as the single environment secret; the hook
# resolves the rest from a 1Password item whose field names match the env
# var names. Defaults: vault "Claude", item "cloud-session-env" (override
# with CLAUDE_OP_VAULT / CLAUDE_OP_ITEM). Vars already set in the
# environment are left untouched.
if [ -n "${OP_SERVICE_ACCOUNT_TOKEN:-}" ]; then
  if ! command -v op >/dev/null 2>&1; then
    OP_CLI_VERSION="v2.31.1"
    curl -sSfLo /tmp/op.zip \
      "https://cache.agilebits.com/dist/1P/op2/pkg/${OP_CLI_VERSION}/op_linux_amd64_${OP_CLI_VERSION}.zip" \
      && unzip -oq /tmp/op.zip -d /usr/local/bin op \
      && chmod +x /usr/local/bin/op \
      || echo "warning: could not install 1Password CLI — allow cache.agilebits.com in the environment's network policy" >&2
  fi
  if command -v op >/dev/null 2>&1; then
    OP_VAULT="${CLAUDE_OP_VAULT:-Claude}"
    OP_ITEM="${CLAUDE_OP_ITEM:-cloud-session-env}"
    for var in NULLIFY_HOST NULLIFY_TOKEN \
               PURPLEMCP_CONSOLE_TOKEN PURPLEMCP_CONSOLE_BASE_URL \
               LATENT_DEFENSE_URL LATENT_DEFENSE_API_KEY; do
      if [ -z "${!var:-}" ]; then
        val="$(op read "op://${OP_VAULT}/${OP_ITEM}/${var}" 2>/dev/null || true)"
        if [ -n "$val" ]; then
          printf 'export %s=%q\n' "$var" "$val" >> "$CLAUDE_ENV_FILE"
        fi
      fi
    done
  fi
fi

echo "Session start hook completed: Go toolchain ready, MCP servers installed, secrets resolved."
