# Vendored MCP servers

Third-party MCP servers vendored so Claude Code cloud sessions can install
them without fetching code from external repositories at session start.
Installed by `.claude/hooks/session-start.sh`; wired up in `.mcp.json`.

| Directory | Upstream | Pinned commit | License |
|---|---|---|---|
| `purple-mcp` | https://github.com/Sentinel-One/purple-mcp | `7ac1eb3f948f555bc4f66581ae6d57734ea5919b` | MIT |
| `latent-defense-mcp` | https://github.com/latent-defense/latent-defense-mcp | `85917e7580db2c2f6b7129d6de20b8f3830b6b2c` | Apache-2.0 |

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
