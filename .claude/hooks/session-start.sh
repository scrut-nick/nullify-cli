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

echo "Session start hook completed: Go modules downloaded, build cache warmed, golangci-lint $GOLANGCI_LINT_VERSION installed."
