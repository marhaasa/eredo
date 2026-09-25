# moat

Run Claude Code against a private repo inside a Docker sandbox that can only
see that repo and can only talk to the domains you list. You keep working on
the host as usual; Claude works on the same files from inside the moat.

```
 host ──docker exec──▶ moat-<project>              moat-<project>-proxy ──▶ internet
                       node user, cap-drop ALL     squid: CONNECT :443 to
                       no sudo, no iptables        allowlisted domains only
                       HTTPS_PROXY=http://proxy
                       └──────── moat-<project>-net: internal, no gateway ──────┘
```

The enforcement point, the proxy, lives outside the container Claude runs in,
so nothing Claude executes can widen the allowlist. The sandbox has no route
to the internet at all; without the proxy every connection fails.

## Requirements

Docker Desktop (macOS, Windows via WSL2) or Docker Engine (Linux), bash, git,
and jq or python3. Claude Code logged in on the host: the token is read from
the macOS keychain or `~/.claude/.credentials.json`, or you export
`CLAUDE_CODE_OAUTH_TOKEN` from `claude setup-token`.

## Install

```bash
brew install marhaasa/tools/moat        # macOS or Linux with Homebrew
```

or, without Homebrew:

```bash
git clone https://github.com/marhaasa/moat.git && ./moat/moat install
```

`moat install` links the command into `~/.local/bin` if it is not already on
your PATH, and links the moat skill into `~/.claude/skills` so Claude Code on
the host can configure moat for you. Run it after a Homebrew install too, for
the skill.

## Usage

```bash
cd ~/src/project && moat           # first run builds the images (a few minutes), then starts and attaches
moat up ~/src/project              # the same from anywhere
moat up ~/src/app ~/src/lib        # extra repos, read-only at their host paths; --rw for read-write
moat attach [name]                 # re-attach to a running sandbox
moat up --model opus --effort low --mode plan ~/src/project   # per-run overrides
moat shell [name]                  # a zsh inside
moat audit [name]                  # every connection the proxy saw, every tool call Claude made
moat config init                   # copy the shipped settings and plugins to ~/.config/moat for editing
moat reload                        # apply edited allowlists and settings to running sandboxes
moat doctor [name]                 # check the host and a running sandbox
moat clean [--volumes] [name]      # remove one or all sandboxes; --volumes drops config and history too
moat build --pull                  # update Claude Code (auto-update is off inside)
```

Inside, the status line reads "sandboxed · project · branch · model · effort ·
mode · context" with "sandboxed" in red, so a sandboxed session is always
recognisable, on every platform and terminal.

## What is inside the moat

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
  `.git` denied; a hook records every tool call to `~/.claude/audit.log`.
- Plugins from `plugins.txt` and MCP servers from an optional `mcp.json`,
  installed on first start.

## Configuration

Shipped defaults live in this repo. Override without editing it, highest
precedence first:

| Where | What |
| --- | --- |
| `<repo>/.moat/allowlist.txt` | extra domains for that repo only, merged in |
| `~/.config/moat/` (`MOAT_CONFIG`) | `allowlist.txt` is merged; `settings.json`, `plugins.txt` and `mcp.json` replace the shipped file |
| this repo | the defaults: Anthropic, GitHub and npm allowed, no plugins, default permission mode |

Merged allowlists are written per project under `~/.local/state/moat/`
(`MOAT_STATE`) and mounted into the proxy. A per-repo list only takes effect
when you run `up` or `reload` on the host, so Claude editing it cannot widen
its own sandbox.

What Claude starts as: `model`, `effortLevel` and `permissions.defaultMode`
in `settings.json`, or `--model`, `--effort`, `--mode` on `up` and `attach`,
or `MOAT_MODEL`, `MOAT_EFFORT`, `MOAT_MODE`. Other variables: `MOAT_IMAGE`,
`MOAT_PROXY_IMAGE`, `MOAT_FORWARD` (forward one host port as `proxy:PORT`,
for a local MCP server or database), `CLAUDE_CODE_OAUTH_TOKEN`.

### With Claude's help

`skills/moat/SKILL.md` teaches Claude Code the layering above and the
commands that apply a change. `moat install` links it into
`~/.claude/skills/moat` on the host, and "allow pypi in moat" or "add the
context7 plugin to my sandbox" becomes a one-line request. Every sandbox gets the same skill, so inside one Claude
knows to put a domain in the repo's `.moat/allowlist.txt` and ask you to run
`moat reload`.

## Git from both sides

The repo is a live bind mount. Commit inside, push from the host with your
own credentials and signing. `attach` injects your git identity, resolved on
the host from the repo's effective config.

Because you run git on the host against the same `.git`, the two places
where a write would execute code on your machine are masked inside:
`.git/hooks` is an empty read-only tmpfs and `.git/config` a read-only bind.
`commit`, `add`, `diff`, `log`, `branch` and `stash` work; `git config`,
`git remote add` and `push -u` fail inside. Repo hooks do not run for sandbox
commits. After changing repo config on the host, `moat restart` picks up the
new file. Claude asks before committing; allow `Bash(git add:*)` and
`Bash(git commit:*)` in your settings override to let it commit freely.

## moat-docker

`moat-docker` runs the same commands, allowlist, settings and plugins on
Docker Desktop's own agent sandboxes (`docker sandbox`), a microVM per project
with a host-side proxy and the credential kept outside the VM. It also
removes the agent user's root and Docker access and restores permission
prompts. What it cannot do is mask `.git`, since the sandbox refuses mounts
over the workspace. `ARCHITECTURE.md` draws both designs and lists the
differences.

## Not covered

- Anything you mount is readable, and what Claude reads is sent to the
  Anthropic API to produce responses. Keep secrets out of the mount.
- Allowed domains are exfiltration channels. GitHub is allowed for cloning
  and plugins; the sandbox has no GitHub credentials, keep it that way.
- The proxy sees only hostnames, never URLs or content.
- The container shares the Docker VM's kernel with your other containers.
  Docker Desktop shares `/Users` with that VM by default; narrow it under
  Settings > Resources > File sharing.

## Development

`moat selftest` starts a scratch sandbox, runs every `doctor` check against
it and removes it. CI runs that plus shellcheck on every push.

MIT license.
