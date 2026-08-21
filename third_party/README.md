# Vendored MCP servers

Third-party MCP servers vendored so Claude Code cloud sessions can install
them without fetching code from external repositories at session start.
Installed by `.claude/hooks/session-start.sh`; wired up in `.mcp.json`.

| Directory | Upstream | Pinned commit | License | Local patches |
|---|---|---|---|---|
| `purple-mcp` | https://github.com/Sentinel-One/purple-mcp | `7ac1eb3f948f555bc4f66581ae6d57734ea5919b` | MIT | none |
| `latent-defense-mcp` | https://github.com/latent-defense/latent-defense-mcp | `85917e7580db2c2f6b7129d6de20b8f3830b6b2c` | Apache-2.0 | `patches/latent-defense-mcp/` |

Notes:
- `.git` directories are stripped; to update, re-clone upstream, review the
  diff, and replace the directory (update the pinned commit above).
- `purple-mcp`'s upstream `CLAUDE.md`/`AGENTS.md` and `latent-defense-mcp`'s
  upstream `.claude/` directory (CLAUDE.md, a broad tool-permission allowlist,
  and slash-command skills) are removed so third-party agent instructions and
  permission grants are not auto-loaded into sessions working in this repo.
  The Latent Defense runtime skills the MCP server serves ship inside the
  Python package (`latent_defense_mcp/skills/`) and are unaffected; re-run
  upstream's `latent-defense-mcp-init` if you ever want their interactive
  scaffolding.

## Local patches

Patches under `patches/<directory>/` are already applied to the vendored tree
and kept as files so a re-vendor can re-apply them. Re-vendoring is
replace-the-directory, which silently drops them — so after replacing a
directory, re-apply every patch still listed for it:

    git apply third_party/patches/<directory>/*.patch

Each patch header records the upstream commit it came from and the condition
that retires it. Drop a patch once the pin moves past that commit.

## Holding `latent-defense-mcp` at 85917e7

Upstream `main` has moved on (`a663cec`, "Added Cursor support."), and we are
deliberately not following it yet. That commit:

- relicenses the package from Apache-2.0 to Proprietary — we vendor a full
  source copy, so this needs the vendor's explicit agreement;
- builds its skill bundle with a `force-include` of `.claude/skills` and
  `.cursor/skills`, which we strip when vendoring, so `uv tool install` fails
  with `FileNotFoundError: Forced include not found`;
- retires 36 MCP tools, including all remediation ticketing and the whole
  threat-model workspace, with no wire-level replacement.

Both security fixes in that commit are backported instead — see
`patches/latent-defense-mcp/`. Don't bump this pin without an answer on the
licence and the build.
