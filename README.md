# claude-containers

Run Claude Code against private repos in a Docker sandbox that can only see
the mounted repo and can only talk to an allowlist of domains.

```
 host ──docker exec──▶ claude-<project>            claude-<project>-proxy ──▶ internet
                       node user, cap-drop ALL     squid: CONNECT :443 to
                       no sudo, no iptables        allowlisted domains only
                       HTTPS_PROXY=http://proxy    socat :8765 -> host:8765 (MCP)
                       └─────── claude-<project>-net (internal, no gateway) ───────┘
```

The enforcement point (the proxy) lives outside the container Claude runs in,
so nothing Claude executes can widen the allowlist. The sandbox has no route to
the internet at all; without the proxy every connection fails.

## Usage

Shell functions (autoloaded from `.zsh_functions/`):

| Command | Does |
| --- | --- |
| `claude-dev [dir]` | Start or reuse the sandbox for `dir` (default: cwd) and attach Claude Code |
| `claude-multi [--rw] <primary> <extra>...` | Same, with extra repos under `/home/node/<name>`, read-only unless `--rw` |
| `claude-attach [name]` | Re-attach to a running sandbox (fzf picker when several) |
| `claude-ps` | List sandboxes |
| `claude-clean [--volumes]` | Remove sandboxes, proxies and networks; `--volumes` also drops config and history |

Everything is implemented in `sandbox.sh`, which also has `shell`, `restart`,
`audit`, `reload` and `build`:

```bash
claude-containers/sandbox.sh audit          # what left the sandbox, and every tool call
claude-containers/sandbox.sh reload         # after editing proxy/allowlist.txt
claude-containers/sandbox.sh build --pull   # update Claude Code (auto-update is off inside)
claude-containers/sandbox.sh up --rebuild . # rebuild image and recreate the sandbox
```

## What is in the sandbox

- Only the primary repo (read-write at `/workspace`) and any extras you name.
  Claude Code config and shell history live in per-project named volumes,
  never on the host.
- The host OAuth token is injected per attach via `docker exec -e`, so it is
  not stored in the container config. Processes Claude runs can read it from
  the environment (Claude Code's env scrub needs bubblewrap, which Docker's
  default seccomp profile blocks), but the proxy only lets it reach
  `api.anthropic.com`, where it goes anyway.
- Telemetry, error reporting, feature flags and auto-update are disabled, so
  the only destination Claude Code itself needs is `api.anthropic.com`.
- `settings.json` allows read-only commands and read-only git, denies
  `git push` and `git remote`, denies Read on `.env*`, `.pem` and `.key`
  files, and logs every tool call through a PreToolUse hook to
  `~/.claude/audit.log` in the config volume.
- `mcp.json` lists MCP servers to register at user scope. The default entry
  reaches the host's reminders server through the proxy's socat forward
  (`CLAUDE_MCP_FORWARD`, default `8765:host.docker.internal:8765`). Inside
  the sandbox the name `host.docker.internal` resolves to the proxy, so only
  that one forwarded port reaches the host and the server sees a Host header
  it accepts.
- `plugins.txt` lists plugins installed on first start. Plugins that talk to
  remote MCP servers need their domains added to the allowlist.

## Git from both sides

The repo is a live bind mount, so you keep working on the host as usual: nvim,
lazygit, push, pull, signing and credentials never enter the container. Claude
commits inside the sandbox and the commit is in your host `git log` at once.

- **Identity.** `attach` and `shell` inject `GIT_AUTHOR_*` and `GIT_COMMITTER_*`
  resolved on the host from the workspace's effective git config, so commits
  are authored as you. They are unsigned; sign on the host before pushing if
  you need that.
- **Masks.** Because you run git on the host against the same `.git`, the two
  places where a write there would execute code on your Mac are masked in the
  sandbox: `.git/hooks` is an empty read-only tmpfs and `.git/config` is a
  read-only bind. `git commit`, `add`, `diff`, `log`, `branch`, `stash` work;
  `git config`, `git remote add` and `push -u` fail inside. Repo hooks do not
  run for sandbox commits. Applied to the primary repo and to `--rw` extras.
- **Stale config.** git replaces `.git/config` by rename, and a file bind
  tracks the old inode. After `git config` or `git remote` changes on the
  host, run `sandbox.sh restart` so the sandbox sees the new file.
- **Prompts.** `git commit` is not on the allowlist, so Claude asks before each
  commit. Add `Bash(git add:*)` and `Bash(git commit:*)` to `settings.json`
  to let it commit without asking.

## Files

| File | Purpose |
| --- | --- |
| `sandbox.sh` | Orchestration: networks, proxy, sandbox, bootstrap, attach |
| `sandbox/Dockerfile` | Sandbox image: node 22, git, gh, rg, fd, jq, zsh, neovim, Claude Code (native installer) |
| `sandbox/audit-hook.sh` | PreToolUse hook that appends tool calls to `audit.log` |
| `proxy/Dockerfile`, `proxy/squid.conf`, `proxy/entrypoint.sh` | Proxy sidecar image |
| `proxy/allowlist.txt` | Domains the sandbox may reach (squid `dstdomain` syntax, bind-mounted read-only) |
| `settings.json` | Claude Code settings copied into each sandbox |
| `mcp.json`, `plugins.txt` | MCP servers and plugins registered on first start |

## Not covered

- Anything you mount is readable, and what Claude reads is sent to the
  Anthropic API to produce responses. Keep secrets out of the mount.
- Allowed domains are exfiltration channels. GitHub is allowed for cloning and
  plugins; the sandbox has no GitHub credentials, keep it that way.
- The proxy sees only hostnames (CONNECT tunnels), not URLs or content.
- Docker Desktop shares `/Users` with its VM by default. Narrow it to `~/Repos`
  under Settings > Resources > File sharing.
