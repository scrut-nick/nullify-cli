# Claude Code cloud session setup

What makes this repo work in a Claude Code on the web session: a session-start
hook, three MCP servers, and secrets resolved from 1Password at launch.

| File | Role |
|---|---|
| `hooks/session-start.sh` | Resolves secrets, installs the MCP servers, warms the Go toolchain. Runs async with a 600s budget. |
| `mcp/with-op-env.sh` | Wraps each MCP server, filling in any env vars the hook hasn't resolved yet. |
| `mcp/seed-nullify-credentials.sh` | Decides when to write `~/.nullify/credentials.json` from the vault copy. |
| `mcp/nullify-serve.sh` | Seeds credentials, then starts `nullify mcp serve`. |
| `settings.json` | Registers the hook. Servers themselves are wired up in `../.mcp.json`. |

## Secrets

Set **one** environment secret on the environment: `OP_SERVICE_ACCOUNT_TOKEN`.
Everything else is read from a 1Password item whose field names match the env
var names — vault `Claude`, item `cloud-session-env` by default, overridable
with `CLAUDE_OP_VAULT` / `CLAUDE_OP_ITEM`.

**Check those two variables before editing anything in the vault** — this
environment overrides both, so the defaults above are not where secrets are
read from, and a value saved to the default location has no effect:

```sh
echo "${CLAUDE_OP_VAULT:-Claude} / ${CLAUDE_OP_ITEM:-cloud-session-env}"
op vault list          # the service account may only see one vault
```

Fields read: `NULLIFY_HOST`, `NULLIFY_CREDENTIALS_JSON`,
`PURPLEMCP_CONSOLE_TOKEN`, `PURPLEMCP_CONSOLE_BASE_URL`, `LATENT_DEFENSE_URL`,
`LATENT_DEFENSE_API_KEY`. Field labels must match those names exactly — a
field named something else is simply never read, and the variable falls back
to whatever `.mcp.json` defaults it to (or stays empty).

`NULLIFY_TOKEN` is deliberately **not** resolved: setting it would override the
stored credentials and disable the CLI's refresh flow.

## Network egress

The environment's network policy has to allow these, or the matching feature
fails with an error that looks like a bug in the server:

| Host | Needed for | Symptom when blocked |
|---|---|---|
| `cache.agilebits.com` | 1Password CLI download | No secrets resolve; every server starts unauthenticated |
| `cve.circl.lu` | SentinelOne `cve_*` tools (cve-search) | `Error communicating with CVE API`, while console tools work fine |
| `www.virustotal.com` | SentinelOne `threat_intel_*` tools | Threat-intel lookups fail (also needs a VirusTotal API key) |

Plus each vendor's own endpoint: `NULLIFY_HOST`, `PURPLEMCP_CONSOLE_BASE_URL`,
`LATENT_DEFENSE_URL`.

## Nullify credentials go stale — how that is handled

`NULLIFY_CREDENTIALS_JSON` carries a refresh token with roughly a 30-day life,
and the CLI rotates it as it refreshes, rewriting the local file. That makes
seeding a two-sided problem: overwrite blindly and you throw away credentials
newer than the vault's; never overwrite and a container that has gone stale
stays broken forever. `seed-nullify-credentials.sh` resolves it by probing:

- file missing or empty → seed from the vault
- stored credentials still mint a token → leave them alone
- stored credentials rejected → replace them, if the vault holds something else

When the vault copy is dead too, it says so on stderr and the MCP server fails
to start. That is the one case a person has to fix:

```sh
nullify auth login --host <your-instance>.nullify.ai   # on a machine with a browser
# then paste the new ~/.nullify/credentials.json into the vault item's
# NULLIFY_CREDENTIALS_JSON field
```

No container rebuild needed afterwards — the next MCP server start picks it up.

## Servers that are not configured here

`Atlassian`, `Slack`, `Gmail`, `Notion` and the other hosted servers are
claude.ai connectors, not entries in `.mcp.json`. They are authorized per user
under claude.ai → Settings → Connectors; a remote session cannot run the OAuth
flow. `MCP server not allowed` from one of them means the connector has not
been authorized for this account.

## Checking on a server

Per-server logs, newest last:

```sh
ls -t ~/.cache/claude-cli-nodejs/*/mcp-logs-<server>/
```

Cold starts of the two Python servers take tens of seconds — the wrapper is
waiting on `uv` to build their environments — so a slow first connection is
expected, not a fault.
