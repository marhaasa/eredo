---
name: moat
description: Configure the moat sandbox for Claude Code - allow domains, add plugins or MCP servers, change permissions, model, effort or permission mode, or explain why something is blocked. Use whenever the user mentions moat, the sandbox allowlist, sandbox plugins, sandbox settings, or a blocked connection inside the sandbox.
---

# moat configuration

moat runs Claude Code in a Docker sandbox whose proxy only lets listed domains
through. Configuration is layered. Edit the highest layer that fits and never
edit the moat repo itself for a personal change.

| Layer | File | Behaviour |
| --- | --- | --- |
| per repo | `<repo>/.moat/allowlist.txt` | extra domains for that repo, merged |
| user | `~/.config/moat/allowlist.txt` | merged |
| user | `~/.config/moat/settings.json`, `plugins.txt`, `mcp.json` | replace the shipped file entirely |
| shipped | the moat repo | defaults |

## First: where are you?

- If `/usr/local/bin/moat-audit-hook` exists you are **inside a sandbox**. You
  can only edit files in the workspace. Domains go in
  `/workspace/.moat/allowlist.txt`; then tell the user to run `moat reload` on
  the host. Everything else happens on the host, so give the exact command.
- Otherwise you are **on the host**. Run `moat config init` once: it creates
  `~/.config/moat` with copies of the shipped `settings.json` and
  `plugins.txt` and an empty `allowlist.txt`. Edit, then `moat reload`, which
  regenerates allowlists and copies settings into running sandboxes.
  `moat config show` prints which file is in effect for each setting.

## Recipes

**Allow a domain.** Append it to the allowlist. `example.com` matches exactly,
`.example.com` matches the domain and every subdomain. Only HTTPS on port 443
passes, so never suggest plain-HTTP hosts. Apply with `moat reload`. Verify with
`moat audit <project>`: `TCP_TUNNEL` lines were allowed, `TCP_DENIED` blocked.

**Add a plugin.** Append `name@marketplace` to `~/.config/moat/plugins.txt`.
Plugins install when a sandbox is created; for one already running, run
`docker exec moat-<project> claude plugin install name@marketplace`. A plugin
that talks to a remote MCP server needs that server's domain allowed; for
example context7 needs `mcp.context7.com`.

**Permissions.** Edit `permissions.allow` and `permissions.deny` in
`~/.config/moat/settings.json`. Rule syntax: `Bash(git commit:*)`,
`Read(./path)`, `WebFetch(domain:host)`, bare `WebSearch`. Keep the shipped
deny entries unless the user explicitly asks to drop one, and say what each
protects: `git push` and `git remote` (no credentials should leave through
git), `WebSearch` (runs on Anthropic's servers, bypasses the proxy),
`.env`/`.pem`/`.key` reads, and `Edit`/`Write` under `.git` (hooks or config
written there would run on the host). Apply with `moat reload`; hook and
status line changes need a new session inside.

**Model, effort, permission mode.** `model`, `effortLevel` and
`permissions.defaultMode` in `settings.json`. For one run:
`moat up --model opus --effort low --mode plan <dir>`.

**MCP servers.** `~/.config/moat/mcp.json` as `{"mcpServers": {...}}` in
Claude Code's own shape. Registered when a sandbox is created, or now with
`moat bootstrap moat-<project>`. A server running on the host needs
`MOAT_FORWARD=PORT:host.docker.internal:PORT` set before `moat up` and the
URL `http://proxy:PORT/...`; a remote server needs its domain allowed.

**Why is X blocked?** `moat audit <project>` shows the proxy decisions and
every tool call. `moat doctor <project>` runs the isolation checks.

Always show the exact edit and the command that applies it. Do not weaken
isolation silently: if a request needs a broader allowlist or a dropped deny,
say so in one sentence and let the user decide.
