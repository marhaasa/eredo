# eredo

A coding agent that can read your files, run commands and reach the internet
is only as safe as the worst instruction it ever follows, whether that comes
from you, from a file in the repo or from a page it fetched. eredo runs
Claude Code where a bad instruction cannot do lasting harm: a Docker sandbox
that sees only the repo you name, reaches only the domains you allow, and
cannot touch your credentials, your other projects or your machine. You keep
working on the host with your own editor, git and keys; Claude works on the
same files from inside.

```
 host ──docker exec──▶ eredo-<project>              eredo-<project>-proxy ──▶ internet
                       node user, cap-drop ALL     squid: CONNECT :443 to
                       no sudo, no iptables        allowlisted domains only
                       HTTPS_PROXY=http://proxy
                       └──────── eredo-<project>-net: internal, no gateway ──────┘
```

The enforcement point, the proxy, lives outside the container Claude runs in,
so nothing Claude executes can widen the allowlist. The sandbox has no route
to the internet at all; without the proxy every connection fails.

## Sungbo's Eredo

Sungbo's Eredo is a system of defensive walls and ditches around Ijebu Ode
in Nigeria, one of the largest earthworks ever built. This tool started life
as "moat"; an eredo is a moat at a different scale, and it makes the same
point: the boundary is dug around the thing it protects, not inside it. That
is where eredo puts its enforcement, in a proxy outside the container that
Claude cannot reach.

## Requirements

Docker Desktop (macOS, Windows) or Docker Engine (Linux), and git. Claude
Code logged in on the host: the token is read from the macOS keychain or
`~/.claude/.credentials.json`, or you export `CLAUDE_CODE_OAUTH_TOKEN` from
`claude setup-token`.

## Install

```bash
brew install marhaasa/tools/eredo        # macOS or Linux with Homebrew
```

or download the binary for your platform from the releases page, or build
it yourself with Go:

```bash
git clone https://github.com/marhaasa/eredo.git && cd eredo && go build -o eredo . && ./eredo install
```

eredo is a single binary with the image sources, default settings and the
Claude skill inside it. `eredo install` links it into `~/.local/bin` if it is
not already on your PATH and writes the eredo skill to `~/.claude/skills`, so
Claude Code on the host can configure eredo for you. Run it after a Homebrew
install too, for the skill. On Windows, put `eredo.exe` on your PATH and run
`eredo install` for the skill.

## Platforms

macOS and Linux run the repo at its host path inside the sandbox. On
Windows, `eredo.exe` runs natively from PowerShell or Windows Terminal with
Docker Desktop, and a repo at `C:\Users\me\src\app` appears inside as
`/c/Users/me/src/app`. Bind mounts from `C:` are slower than from the WSL
filesystem and show up root-owned inside the container, which is why eredo
adds `safe.directory` entries for every mounted repo. Running inside WSL2
with Docker Desktop's WSL integration works too, as Linux.

## Usage

```bash
cd ~/src/project && eredo           # first run builds the images (a few minutes), then starts and attaches
eredo up ~/src/project              # the same from anywhere
eredo up ~/src/app ~/src/lib        # extra repos, read-only at their host paths; --rw for read-write
eredo attach [name]                 # re-attach to a running sandbox
eredo up --model opus --effort low --mode plan ~/src/project   # per-run overrides
eredo shell [name]                  # a zsh inside
eredo audit [name]                  # every connection the proxy saw, every tool call Claude made
eredo config init                   # copy the shipped settings and plugins to ~/.config/eredo for editing
eredo reload                        # apply edited allowlists, settings and the relay list to running sandboxes
eredo relay [name]                  # commands Claude queued for your host logins (az, gh, ...)
eredo update                        # rebuild images with the newest Claude Code
eredo doctor [name]                 # check the host and a running sandbox
eredo clean [--volumes] [name]      # remove one or all sandboxes; --volumes drops config and history too
eredo build --pull                  # update Claude Code (auto-update is off inside)
```

Inside, the status line reads "sandboxed · project · branch · model · effort ·
mode · context" with "sandboxed" in red, so a sandboxed session is always
recognisable, on every platform and terminal.

## What is inside the eredo

- Only the repo you name, read-write, plus any extras, each mounted at the
  same path it has on the host so Claude's working directory and every path
  it mentions match yours. Claude Code config and shell history live in
  per-project named volumes.
- The host OAuth token, injected per attach via `docker exec -e`; it is never
  stored in the container. Telemetry, error reporting, feature flags and
  auto-update are off, so `api.anthropic.com` is the only destination Claude
  Code itself needs.
- `settings.json`: read-only commands and read-only git allowed; `git push`,
  `git remote`, `WebSearch` (it runs on Anthropic's servers and would bypass
  the proxy), reads of `.env*`, `.pem` and `.key` files, and edits under
  `.git`, `.claude/` and `.mcp.json` (files that configure Claude on the host
  the next time you open the repo there) denied; a hook records every tool
  call to `~/.claude/audit.log`.
- Plugins from `plugins.txt` and MCP servers from an optional `mcp.json`,
  installed on first start.

## Configuration

Shipped defaults live in this repo. Override without editing it, highest
precedence first:

| Where | What |
| --- | --- |
| `<repo>/.eredo/allowlist.txt` | extra domains for that repo only, merged in |
| `~/.config/eredo/` (`EREDO_CONFIG`) | `allowlist.txt` is merged; `settings.json`, `plugins.txt`, `relay.txt` and `mcp.json` replace the shipped file |
| this repo | the defaults: Anthropic, GitHub and npm allowed, no plugins, default permission mode |

Merged allowlists are written per project under `~/.local/state/eredo/`
(`EREDO_STATE`) and mounted into the proxy. A per-repo list only takes effect
when you run `up` or `reload` on the host, so Claude editing it cannot widen
its own sandbox.

What Claude starts as: `model`, `effortLevel` and `permissions.defaultMode`
in `settings.json`, or `--model`, `--effort`, `--mode` on `up` and `attach`,
or `EREDO_MODEL`, `EREDO_EFFORT`, `EREDO_MODE`. Other variables: `EREDO_IMAGE`,
`EREDO_PROXY_IMAGE`, `EREDO_FORWARD` (forward one host port as `proxy:PORT`,
for a local MCP server or database), `CLAUDE_CODE_OAUTH_TOKEN`.

### With Claude's help

`skills/eredo/SKILL.md` teaches Claude Code the layering above and the
commands that apply a change. `eredo install` links it into
`~/.claude/skills/eredo` on the host, and "allow pypi in eredo" or "add the
context7 plugin to my sandbox" becomes a one-line request. Every sandbox gets the same skill, so inside one Claude
knows to put a domain in the repo's `.eredo/allowlist.txt` and ask you to run
`eredo reload`.

## Git from both sides

The repo is a live bind mount. Commit inside, push from the host with your
own credentials and signing. `attach` injects your git identity, resolved on
the host from the repo's effective config.

Because you run git on the host against the same `.git`, the two places
where a write would execute code on your machine are masked inside:
`.git/hooks` is an empty read-only tmpfs and `.git/config` a read-only bind.
`commit`, `add`, `diff`, `log`, `branch` and `stash` work; `git config`,
`git remote add` and `push -u` fail inside. Repo hooks do not run for sandbox
commits. After changing repo config on the host, `eredo restart` picks up the
new file. Claude asks before committing; allow `Bash(git add:*)` and
`Bash(git commit:*)` in your settings override to let it commit freely.

## Commands that need your credentials

Claude inside has no cloud logins, so `az`, `aws`, `gcloud`, `gh` and `kubectl`
cannot work there. When Claude runs one, eredo stops it before it runs, adds it
to a queue and tells Claude it is waiting for you. In a second terminal:

```bash
eredo relay              # the queue, oldest first
eredo relay --copy 3     # copy #3 to the clipboard (default: the latest)
eredo relay --watch      # copy each new command as it arrives, with a bell
eredo relay --clear
```

Read the command, run it yourself, and paste the output back if Claude needs
it. Nothing runs on your machine automatically. The list is `relay.txt`:
`eredo config init` copies it to `~/.config/eredo`, one command per line; an
empty file turns the relay off; `eredo reload` applies it. A `settings.json`
override must keep the PreToolUse group that runs `eredo-relay-hook`.

## Files the host runs later

Some files in a repo are executed or obeyed by the host after the session:
git hooks and config, submodule git dirs, `core.hooksPath` targets and
`include.path` files, hook managers (`.husky/`, `lefthook*.yml`,
`.pre-commit-config.yaml`), and Claude Code's project settings
(`.claude/settings*.json`, `.claude/hooks/`, `.mcp.json`). eredo mounts every
one that exists read-only inside, and warns when a session creates one that did
not exist. Claude's own policy (permissions, hooks, status line) lives in
read-only managed settings, so nothing inside can rewrite its rules. Linked
worktrees work: the shared `.git` is mounted at its host path with the same
masks.

## eredo and Docker Sandboxes

Docker Desktop ships its own agent sandboxes (`docker sandbox`, and the newer
standalone `sbx` CLI): a microVM per project with a host-side proxy that keeps
the credential outside the VM. eredo does not wrap them, but they are the right
tool for some jobs, and a good option alongside eredo.

**Use eredo** for interactive work on your own repos: commit inside, push from
the host, with `.git` and other host-executed files read-only, a policy Claude
cannot edit, an allowlist you own, the relay for host-only commands, and a log
of every connection.

**Use Docker Sandboxes** when the code is not yours, when nobody is watching,
or when the task needs root or Docker inside. The microVM is a stronger
boundary than a container, and the credential never enters it. The trade-off:
it refuses mounts over the workspace, so it cannot protect `.git` or other
files the host runs later, and it ships with open egress, root inside and
permission prompts off. Review the diff before you run git on the host.

To get closer to eredo's defaults there (this is what eredo 0.2's
`eredo-docker` twin automated):

```bash
docker sandbox create --name claude-myrepo claude ~/src/myrepo
# default deny; allow what you need; Docker's built-in allows only yield to host:port blocks
docker sandbox network proxy claude-myrepo --policy deny \
  --allow-host api.anthropic.com --allow-host platform.claude.com \
  --allow-host github.com --allow-host '*.githubusercontent.com' --allow-host registry.npmjs.org \
  --block-host pypi.org:443 --block-host ports.ubuntu.com:80 --block-host '*.docker.com:443'
# take root and the Docker socket away from the agent user
docker sandbox exec -u root claude-myrepo sh -c 'rm -f /etc/sudoers.d/*; gpasswd -d agent sudo; gpasswd -d agent docker'
# restore permission prompts: copy eredo's settings.json in (run `eredo config init` for a copy)
docker sandbox exec -i claude-myrepo sh -c 'cat > ~/.claude/settings.json' < ~/.config/eredo/settings.json
docker sandbox run claude-myrepo      # /login once inside; Docker keeps the token on the host
docker sandbox network log            # what it reached
```

`ARCHITECTURE.md` draws both designs and lists the differences, measured with
that twin.

## Not covered

- Anything you mount is readable, and what Claude reads is sent to the
  Anthropic API to produce responses. Keep secrets out of the mount.
- Allowed domains are exfiltration channels. GitHub is allowed for cloning
  and plugins; the sandbox has no GitHub credentials, keep it that way.
- Relayed commands are written by Claude, and the queue lives inside the
  sandbox. Read each one before running it on the host, where your
  credentials are. `eredo relay` shows control characters escaped and refuses
  to copy them.
- The proxy sees only hostnames, never URLs or content. It refuses private,
  loopback and link-local destinations even for allowlisted names, so
  `EREDO_FORWARD` is the only way to reach a host service.
- The tool-call log lives inside the sandbox and Claude can edit it. The proxy
  log is outside and is the authoritative record of what left.
- A repo's own `.eredo/allowlist.txt` is applied only by you running `up` or
  `reload`, and eredo prints what it adds each time. Read it before starting a
  sandbox on a repo you did not write.
- Sandboxes are named after the repo directory. Two repos with the same name
  need `EREDO_NAME` to tell them apart; eredo refuses to reuse the name.
- The container shares the Docker VM's kernel with your other containers.
  Docker Desktop shares `/Users` with that VM by default; narrow it under
  Settings > Resources > File sharing.

## Development

`eredo selftest` starts a scratch sandbox, runs every `doctor` check against
it and removes it. CI runs that plus shellcheck on every push.

MIT license.
