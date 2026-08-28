# Working in this repository

The Nullify CLI, plus the MCP wiring that gives a Claude Code session access to
this organization's security tools (`.mcp.json`, `.claude/mcp/`).

## Before reporting on security data, check coverage

**Read `.claude/mcp/health.json` first.** It is written by
`.claude/mcp/preflight.sh` on every session start, and again whenever you run
`make preflight`.

```jsonc
{ "healthy": false, "failures": 1,
  "servers": [ { "server": "nullify", "status": "FAIL", "detail": "...", "tools": 27 } ] }
```

A `FAIL` server does not error when you call it. It returns **empty results**,
which is indistinguishable from good news:

- an expired or wrong-typed credential still lets the server start and
  advertise its whole tool set, then fails every call;
- a rejected key often yields `[]` rather than a 4xx.

So an empty finding list from a `FAIL` server means **absent data**, never
"nothing found". Say so explicitly in any report rather than implying the
estate was covered. This has already gone wrong once: a weekly review was
published claiming coverage while two sources were dead, and a second review
declared a healthy platform dead for six days because the check probed the
wrong endpoint.

If `health.json` is missing or stale, run `make preflight` before relying on
any security tool.

## Adding an MCP server

1. Add it to `.mcp.json`.
2. Add an authorization probe to `.claude/mcp/health-probes.json`.

Step 2 is not optional. Preflight takes its server list from `.mcp.json`, so a
server without a probe is reported `WARN … no probe defined` rather than
skipped — but a `WARN` is an unverified server, which is the state this whole
mechanism exists to prevent.

Choose a probe endpoint that **returns data** and verify it answers
differently without the credential. Do not probe an identity endpoint without
checking it accepts the credential type you use: `latent-defense`'s `/auth/me`
serves login sessions and returns 401 to a perfectly valid API key, which is
exactly how the six-day false outage happened.

## Credentials

Secrets resolve from 1Password via `.claude/mcp/with-op-env.sh`, from the item
named by `CLAUDE_OP_VAULT` / `CLAUDE_OP_ITEM`. Mark a var optional with a `?`
prefix in `.mcp.json` when a server accepts more than one credential model, so
the unused alternative does not warn.

For Nullify specifically, prefer a long-lived `NULLIFY_TOKEN` over a seeded
`NULLIFY_CREDENTIALS_JSON`: a container is rebuilt every session, so refreshed
tokens are written to a filesystem that is then discarded, and the stored copy
only ages. Note that the `access_token` inside a `credentials.json` is a Cognito
**ID token** — it authenticates the process but the API rejects it, surfacing as
403 on every call. Preflight checks for both mistakes.

## Verifying a change

```sh
make build && make unit && make lint    # Go
bash -n .claude/mcp/*.sh                # shell
make preflight                          # MCP servers still usable
```

## Upstream

This repo tracks `Nullify-Platform/cli`. When touching auth or the refreshing
transport, check upstream first — its `internal/auth/login.go` documents the
real contract of `GET /auth/refresh_token` (no token in the JSON body; the
access token arrives only as a `Set-Cookie`, and the refresh token is **not**
rotated). This fork branched at upstream `#138`, so it is behind in the auth/client
area. #165's behaviour has been ported by hand (401-triggered forced refresh,
`ForceRefreshToken`, `RefreshNullifyToken`, `ErrTokenNotRefreshable`, and
separate backoff clocks for the TTL and 401 paths), but these upstream changes
are still missing:

- **#159** — the GitHub PAT is still sent in the URL query string by
  `internal/lib/get_token.go`, where it can land in access logs and proxies.
  Upstream moved it into a POST body.
- **#151** — an idempotency-aware retry: 5xx is only retried for idempotent
  methods, so a POST that may already have committed is never replayed. Ours
  replays any 5xx.
- **#152 / #158** — MCP tools rebuilt on the generated API client, and the RunE
  migration. Large behavioural changes; deliberately not adopted.

Do not cherry-pick from upstream blind: the histories are unrelated, so a pick
of any one commit pulls in dependencies this fork does not have.
