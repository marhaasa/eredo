# claude-containers

Run Claude Code against private repos in a Docker sandbox that can only see
the mounted repo and can only talk to an allowlist of domains.

```
 host ──docker exec──▶ claude-<project>            claude-<project>-proxy ──▶ internet
                       node user, cap-drop ALL     squid: CONNECT :443 to
                       no sudo, no iptables        allowlisted domains only
                       HTTPS_PROXY=http://proxy    (optional socat forward to one host port)
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
- The status line inside the sandbox reads "sandboxed · project · branch ·
  model · effort · permission mode · context", with "sandboxed" in deep red,
  rendered by Claude Code itself, so the cue is the same on every platform
  and terminal. The mode comes from the hook, which records it on every
  event, so it can lag one prompt behind a Shift+Tab toggle. Host sessions have no status line, so the contrast
  is immediate.
- `settings.json` starts Claude in auto mode, allows read-only commands and read-only git, denies
  `git push` and `git remote`, denies Read on `.env*`, `.pem` and `.key`
  files, denies `WebSearch` (it runs on Anthropic's servers, so the proxy
  never sees it), and logs every tool call through a PreToolUse hook to
  `~/.claude/audit.log` in the config volume. `WebFetch` stays: it fetches
  from inside the sandbox, so the allowlist governs it.
- `plugins.txt` lists plugins installed on first start. Plugins that talk to
  remote MCP servers need their domains in the allowlist; `mcp.context7.com`
  is there for the context7 plugin.
- An optional `mcp.json` (`{"mcpServers": {...}}`, Claude Code's own shape)
  registers MCP servers at user scope on first start. To reach a service on
  the host, set `CLAUDE_SANDBOX_FORWARD=LISTEN:host.docker.internal:PORT`
  before `up`; the proxy then forwards `proxy:LISTEN` to that one host port.

## Configuration

Shipped defaults live in this directory. Override them without editing the
repo, highest precedence first:

| Where | What |
| --- | --- |
| `<repo>/.claude-sandbox/allowlist.txt` | extra domains for that repo only, merged in |
| `~/.config/claude-sandbox/` (`CLAUDE_SANDBOX_CONFIG`) | `allowlist.txt` is merged; `settings.json`, `plugins.txt` and `mcp.json` replace the shipped file |
| `claude-containers/` | the defaults |

Merged allowlists are written per project under `~/.local/state/claude-sandbox/`
(`CLAUDE_SANDBOX_STATE`) and mounted into the proxy. `sandbox.sh reload`
regenerates them and makes squid re-read. A per-repo list only takes effect
when you run `up` or `reload` on the host, so an edit Claude makes to it
cannot widen its own sandbox.

What Claude starts as is set in `settings.json`: `model`, `effortLevel` and
`permissions.defaultMode` (shipped: fable[1m], high, auto). Override for
one run with `--model`, `--effort` and `--mode` on `up` or `attach`, or the
`CLAUDE_SANDBOX_MODEL`, `CLAUDE_SANDBOX_EFFORT` and `CLAUDE_SANDBOX_MODE`
variables; they become `--model`, `--effort` and `--permission-mode` on the
claude command inside.

Environment variables: `CLAUDE_SANDBOX_IMAGE` and `CLAUDE_PROXY_IMAGE` (image
names), `CLAUDE_SANDBOX_FORWARD` (one host port), `CLAUDE_CODE_OAUTH_TOKEN`
(skip the credential lookup).

## Linux and Windows

The Docker parts are the same everywhere. What differs is where the Claude
credential comes from and how the repo is mounted.

- **Credential.** The token is read from `CLAUDE_CODE_OAUTH_TOKEN` if set,
  else the macOS keychain, else `~/.claude/.credentials.json`, which is where
  Claude Code stores it on Linux and WSL. `claude setup-token` gives a
  long-lived token for the first form.
- **Linux.** Docker Engine works as is; `host.docker.internal` is provided
  through `host-gateway`.
- **Windows.** Run the scripts inside WSL2 with Docker Desktop's WSL
  integration. Keep repos on the WSL filesystem: bind mounts from `C:` are
  slow and show up root-owned inside the container, which is why bootstrap
  adds `safe.directory` entries for every mounted repo. `.gitattributes`
  keeps the scripts LF even when Git for Windows checks them out. Native
  PowerShell is not supported; that would be a port, not a fix.

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
| `lib.sh` | Shared by both scripts: config resolution, allowlist merge, credential lookup, identity |
| `.gitattributes` | LF line endings for everything here |
| `sandbox/Dockerfile` | Sandbox image: node 22, git, gh, rg, fd, jq, zsh, neovim, Claude Code (native installer) |
| `sandbox/audit-hook.sh` | PreToolUse hook that appends tool calls to `audit.log` |
| `sandbox/statusline.sh` | Status line marking the session as sandboxed |
| `proxy/Dockerfile`, `proxy/squid.conf`, `proxy/entrypoint.sh` | Proxy sidecar image |
| `proxy/allowlist.txt` | Default domains the sandbox may reach (squid `dstdomain` syntax); merged with overrides |
| `settings.json` | Claude Code settings copied into each sandbox |
| `docker-sandbox.sh` | Same commands on Docker's agent sandboxes, for comparison |
| `plugins.txt` | Plugins installed on first start (optional `mcp.json` registers MCP servers) |

## docker-sandbox.sh: the same setup on Docker Sandboxes

`docker-sandbox.sh` has the same commands as `sandbox.sh` and reads the same
`proxy/allowlist.txt`, `settings.json`, `plugins.txt` and audit hook, but runs
Claude in one of Docker Desktop's agent sandboxes (`docker sandbox`, a microVM
per project) instead of a container. It exists so the two can be compared
like for like; `ARCHITECTURE.md` draws the differences. Nothing in the zsh
functions points at it; call it directly.

```bash
claude-containers/docker-sandbox.sh up ~/Repos/github.com/marhaasa/Doll
claude-containers/docker-sandbox.sh audit Doll
claude-containers/docker-sandbox.sh clean
```

What the script adds on top of Docker's defaults, so the comparison is fair:
default-deny network policy generated from the allowlist with Docker's own
built-in allow rules blocked, the shared `settings.json` (prompts instead of
Docker's bypassPermissions), the audit hook, git identity from the host, and
removal of the agent user's passwordless sudo and Docker socket access.

| | `sandbox.sh` | `docker-sandbox.sh` |
| --- | --- | --- |
| Boundary | container in Docker Desktop's VM, `--cap-drop ALL` | one microVM per project |
| Egress | squid sidecar, CONNECT only, no decryption | Docker's host proxy, TLS-intercepting |
| Allowlist | yours alone | yours, plus Docker's built-ins blocked by port |
| Claude credential | host OAuth token as env var per attach | `/login` once inside; token stays on the host |
| `.git/hooks`, `.git/config` | masked read-only | writable: the sandbox refuses mounts over the workspace |
| Root inside | none | removed by the script; Docker gives it by default |
| Audit | proxy log per request plus tool log | Docker per-host counters plus tool log |
| Startup | seconds | about 20 s to create, seconds to reuse |
| Requirements | Docker Desktop | Docker Desktop with the sandbox plugin, Docker account for `sbx` |

The `.git` row is the one that matters for the commit-inside, push-on-host
workflow: with Docker's sandbox the only protection for hooks and config is
that Claude asks before running commands that write there.

## Not covered

- Anything you mount is readable, and what Claude reads is sent to the
  Anthropic API to produce responses. Keep secrets out of the mount.
- Allowed domains are exfiltration channels. GitHub is allowed for cloning and
  plugins; the sandbox has no GitHub credentials, keep it that way.
- The proxy sees only hostnames (CONNECT tunnels), not URLs or content.
- Docker Desktop shares `/Users` with its VM by default. Narrow it to `~/Repos`
  under Settings > Resources > File sharing.
