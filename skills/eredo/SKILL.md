---
name: eredo
description: Configure the eredo sandbox for Claude Code - allow domains, add plugins or MCP servers, change permissions, model, effort or permission mode, relay host-only commands (az, aws, gcloud, gh, kubectl), or explain why something is blocked. Use whenever the user mentions eredo, eredo relay, the sandbox allowlist, sandbox plugins, sandbox settings, or a blocked connection or command inside the sandbox.
---

# eredo configuration

eredo runs Claude Code in a Docker sandbox whose proxy only lets listed domains
through. Configuration is layered. Edit the highest layer that fits and never
edit the eredo repo itself for a personal change.

| Layer | File | Behaviour |
| --- | --- | --- |
| per repo | `<repo>/.eredo/allowlist.txt` | extra domains for that repo, merged |
| user | `~/.config/eredo/allowlist.txt` | merged |
| user | `~/.config/eredo/settings.json`, `plugins.txt`, `relay.txt`, `mcp.json` | replace the shipped file entirely |
| shipped | the eredo repo | defaults |

## First: where are you?

- If `/usr/local/bin/eredo-audit-hook` exists you are **inside a sandbox**. You
  can only edit files in the mounted repo, which sits at the same path as on
  the host. Domains go in the repo's `.eredo/allowlist.txt`; then tell the user
  to run `eredo reload` on the host. Everything else happens on the host, so give the exact command.
- Otherwise you are **on the host**. Run `eredo config init` once: it creates
  `~/.config/eredo` with copies of the shipped `settings.json` and
  `plugins.txt` and an empty `allowlist.txt`. Edit, then `eredo reload`, which
  regenerates allowlists and copies settings into running sandboxes.
  `eredo config show` prints which file is in effect for each setting.

A Bash call blocked with `eredo relay: not run` was queued for the host, where
the user's logins are. Inside, give the user the number and
`eredo relay <project> --copy <n>`, ask for the output if you need it, and never
retry it another way. You cannot change the relay list from inside; give the
user the host edit.

## Recipes

**Host-only commands (relay).** The sandbox has no cloud logins. Commands whose
program is on the relay list (shipped: az aws gcloud gh kubectl) are blocked
inside before they run and queued for the host. On the host: `eredo relay
[project]` lists the queue, `--copy [n]` copies one (default the latest),
`--watch` copies each new one as it arrives, `--clear` empties it. The user
reads each command and runs it themselves; nothing runs automatically. To relay
another command: `eredo config init` (creates `~/.config/eredo/relay.txt`), add
one name per line, `eredo reload`; it applies at once. Remove a name to let it
run inside; an empty file turns the relay off. A `settings.json` override must
keep the PreToolUse group whose command runs `eredo-relay-hook`.


**Allow a domain.** Append it to the allowlist. `example.com` matches exactly,
`.example.com` matches the domain and every subdomain. Only HTTPS on port 443
passes, so never suggest plain-HTTP hosts. Apply with `eredo reload`. Verify with
`eredo audit <project>`: `TCP_TUNNEL` lines were allowed, `TCP_DENIED` blocked.

**Add a plugin.** Append `name@marketplace` to `~/.config/eredo/plugins.txt`.
Plugins install when a sandbox is created; for one already running, run
`docker exec eredo-<project> claude plugin install name@marketplace`. A plugin
that talks to a remote MCP server needs that server's domain allowed; for
example context7 needs `mcp.context7.com`.

**Permissions.** Edit `permissions.allow` and `permissions.deny` in
`~/.config/eredo/settings.json`. Rule syntax: `Bash(git commit:*)`,
`Read(./path)`, `WebFetch(domain:host)`, bare `WebSearch`. Keep the shipped
deny entries unless the user explicitly asks to drop one, and say what each
protects: `git push` and `git remote` (no credentials should leave through
git), `WebSearch` (runs on Anthropic's servers, bypasses the proxy),
`.env`/`.pem`/`.key` reads, and `Edit` under `.git` (hooks or config
written there would run on the host). Apply with `eredo reload`; hook and
status line changes need a new session inside.

**Model, effort, permission mode.** `model`, `effortLevel` and
`permissions.defaultMode` in `settings.json`. For one run:
`eredo up --model opus --effort low --mode plan <dir>`.

**MCP servers.** `~/.config/eredo/mcp.json` as `{"mcpServers": {...}}` in
Claude Code's own shape. Registered when a sandbox is created, or now with
`eredo bootstrap eredo-<project>`. A server running on the host needs
`EREDO_FORWARD=PORT:host.docker.internal:PORT` set before `eredo up` and the
URL `http://proxy:PORT/...`; a remote server needs its domain allowed.

**eredo or Docker Sandboxes?** For code that is not the user's, unattended
runs, or tasks that need root or Docker inside, suggest Docker's own sandboxes
(`docker sandbox` / `sbx`) instead; the README section "eredo and Docker
Sandboxes" has the hardening commands. eredo is for interactive work on the
user's own repos.

**Why is X blocked?** `eredo audit <project>` shows the proxy decisions and
every tool call. `eredo doctor <project>` runs the isolation checks.

Always show the exact edit and the command that applies it. Do not weaken
isolation silently: if a request needs a broader allowlist or a dropped deny,
say so in one sentence and let the user decide.
