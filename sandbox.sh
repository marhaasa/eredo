#!/usr/bin/env bash
# Claude Code sandbox.
#
# Per project, sandbox.sh creates an *internal* Docker network (no route to the
# outside), a proxy sidecar that is the only thing on that network with egress,
# and a sandbox container that runs Claude Code as an unprivileged user with
# every capability dropped. The sidecar owns the domain allowlist, so nothing
# Claude runs can widen it.
#
#   sandbox.sh up [--rw] [--rebuild] [--no-attach] <primary> [extra-repo ...]
#                     start (or reuse) the sandbox for <primary> and attach.
#                     Extra repos are mounted read-only under /home/node/<name>
#                     unless --rw is given. --no-attach just starts it.
#   sandbox.sh attach [name]     attach Claude Code to a running sandbox
#   sandbox.sh shell  [name]     open a zsh in a running sandbox
#   sandbox.sh restart [name]    restart a sandbox (re-resolves .git/config mask)
#   sandbox.sh ps                list sandboxes
#   sandbox.sh audit  [name]     show egress log and tool-call log
#   sandbox.sh reload            make running proxies re-read the allowlist
#   sandbox.sh clean [--volumes] remove all sandboxes, proxies and networks
#   sandbox.sh build [--pull]    (re)build both images
#   sandbox.sh bootstrap <container> [--rw] [extra-repo ...]
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SANDBOX_IMAGE="${CLAUDE_SANDBOX_IMAGE:-claude-sandbox:local}"
PROXY_IMAGE="${CLAUDE_PROXY_IMAGE:-claude-proxy:local}"
EGRESS_NET="claude-egress"
LABEL="claude.sandbox"
SETTINGS="$HERE/settings.json"
PLUGINS="$HERE/plugins.txt"
MCP="$HERE/mcp.json"
# Optional LISTEN:HOST:PORT the proxy forwards with socat, so the sandbox can
# reach exactly one host port as http://proxy:LISTEN (for a local MCP server or
# database). Off by default. Example: CLAUDE_SANDBOX_FORWARD=8765:host.docker.internal:8765
HOST_FORWARD="${CLAUDE_SANDBOX_FORWARD-}"
PROXY_URL="http://proxy:3128"

BG_ACTIVE='\033]11;#282828\033\\'
BG_NORMAL='\033]11;#1d2021\033\\'

die()  { printf 'sandbox: %s\n' "$*" >&2; exit 1; }
info() { printf '%s\n' "$*" >&2; }
need() { command -v "$1" >/dev/null 2>&1 || die "$1 not found"; }

sb()  { printf 'claude-%s' "$1"; }
px()  { printf 'claude-%s-proxy' "$1"; }
net() { printf 'claude-%s-net' "$1"; }

exists()  { docker container inspect "$1" >/dev/null 2>&1; }
running() { [ "$(docker container inspect -f '{{.State.Running}}' "$1" 2>/dev/null)" = "true" ]; }
label()   { docker container inspect -f "{{index .Config.Labels \"$2\"}}" "$1" 2>/dev/null; }

usage() { sed -n '2,/^set -euo/p' "$0" | sed '$d' | sed 's/^# \{0,1\}//'; }

ensure_docker() {
  need docker
  docker info >/dev/null 2>&1 || die "Docker daemon is not running"
}

ensure_images() {
  docker image inspect "$SANDBOX_IMAGE" >/dev/null 2>&1 &&
  docker image inspect "$PROXY_IMAGE"   >/dev/null 2>&1 || cmd_build
}

cmd_build() {
  local pull=""
  [ "${1:-}" = "--pull" ] && pull="--pull"
  info "Building $PROXY_IMAGE"
  docker build $pull -t "$PROXY_IMAGE" "$HERE/proxy"
  info "Building $SANDBOX_IMAGE"
  docker build $pull --build-arg "TZ=${TZ:-Europe/Oslo}" -t "$SANDBOX_IMAGE" "$HERE/sandbox"
}

# Print the project name of a running sandbox: the given one, the only one, or
# an fzf pick.
pick_name() {
  if [ -n "${1:-}" ]; then printf '%s' "$1"; return; fi
  local list
  list="$(docker ps --filter "label=claude.role=sandbox" \
    --format '{{.Label "claude.project"}}	{{.Status}}	{{.Label "claude.workspace"}}')"
  [ -n "$list" ] || die "no running sandboxes"
  if [ "$(printf '%s\n' "$list" | wc -l | tr -d ' ')" -eq 1 ]; then
    printf '%s' "${list%%	*}"
  else
    need fzf
    local pick
    pick="$(printf '%s\n' "$list" | fzf --header 'Select sandbox' --height 10 --reverse)" || return 1
    printf '%s' "${pick%%	*}"
  fi
}

oauth_token() {
  security find-generic-password -s "Claude Code-credentials" -w 2>/dev/null \
    | python3 -c 'import sys, json; print(json.load(sys.stdin)["claudeAiOauth"]["accessToken"])' 2>/dev/null \
    || die "could not read the Claude Code OAuth token from the keychain (run claude on the host and log in)"
}

# Mask the two places under .git where a write would execute code on the host
# the next time the user runs git there: hooks (empty, read-only tmpfs) and
# config (read-only bind: core.hooksPath, fsmonitor, pager, filters, ...).
# Everything else under .git stays writable so commits work.
# Appends to the caller's `mounts` array.
add_git_masks() {
  local src="$1" dst="$2"
  if [ -d "$src/.git" ]; then
    mounts+=(--tmpfs "$dst/.git/hooks:ro,size=64k")
    if [ -f "$src/.git/config" ]; then
      mounts+=(--mount "type=bind,source=$src/.git/config,target=$dst/.git/config,readonly")
    fi
  elif [ -e "$src/.git" ]; then
    info "note: $src/.git is not a directory (worktree or submodule): hooks and config are not masked"
  fi
}

# Git identity for commits made inside the sandbox, resolved on the host from
# the workspace's effective config (repo, then global). Fills GIT_ENV.
GIT_ENV=()
git_identity_env() {
  local ws="$1" name email
  GIT_ENV=()
  name="$(git -C "$ws" config --get user.name 2>/dev/null || true)"
  email="$(git -C "$ws" config --get user.email 2>/dev/null || true)"
  [ -n "$name" ]  && GIT_ENV+=(-e "GIT_AUTHOR_NAME=$name"   -e "GIT_COMMITTER_NAME=$name")
  [ -n "$email" ] && GIT_ENV+=(-e "GIT_AUTHOR_EMAIL=$email" -e "GIT_COMMITTER_EMAIL=$email")
  return 0
}

remove_project() {
  local name="$1"
  docker rm -f "$(sb "$name")" "$(px "$name")" >/dev/null 2>&1 || true
  docker network rm "$(net "$name")" >/dev/null 2>&1 || true
}

cmd_up() {
  local rw=0 rebuild=0 attach=1
  while [ $# -gt 0 ]; do
    case "$1" in
      --rw) rw=1; shift ;;
      --rebuild) rebuild=1; shift ;;
      --no-attach) attach=0; shift ;;
      --) shift; break ;;
      -*) die "unknown option: $1" ;;
      *) break ;;
    esac
  done
  [ $# -ge 1 ] || die "usage: sandbox.sh up [--rw] [--rebuild] [--no-attach] <primary> [extra-repo ...]"

  local primary
  primary="$(cd "$1" 2>/dev/null && pwd -P)" || die "not a directory: $1"
  shift
  local extras=() e
  for e in "$@"; do
    extras+=("$(cd "$e" 2>/dev/null && pwd -P)") || die "not a directory: $e"
  done

  local name sbx prx nw
  name="$(basename "$primary")"
  sbx="$(sb "$name")"; prx="$(px "$name")"; nw="$(net "$name")"
  local spec="v3 $primary rw=$rw ${extras[*]-}"

  ensure_docker
  [ $rebuild -eq 1 ] && cmd_build
  ensure_images

  # Reuse an existing sandbox when its mounts are unchanged.
  if exists "$sbx" && [ $rebuild -eq 0 ] && [ "$(label "$sbx" claude.spec)" = "$spec" ]; then
    if ! running "$sbx"; then
      info "Starting $sbx"
      docker start "$prx" "$sbx" >/dev/null
    fi
    [ $attach -eq 1 ] && cmd_attach "$name"
    return
  fi
  if exists "$sbx"; then
    info "Recreating $sbx (mounts or image changed)"
    remove_project "$name"
  fi

  docker network inspect "$EGRESS_NET" >/dev/null 2>&1 || docker network create "$EGRESS_NET" >/dev/null
  docker network inspect "$nw" >/dev/null 2>&1 || docker network create --internal --label "$LABEL=1" "$nw" >/dev/null

  info "Starting proxy $prx"
  docker create --name "$prx" \
    --label "$LABEL=1" --label "claude.role=proxy" --label "claude.project=$name" \
    --network "$nw" --network-alias proxy \
    --add-host host.docker.internal:host-gateway \
    --mount "type=bind,source=$HERE/proxy,target=/etc/claude-proxy,readonly" \
    -e "FORWARD=$HOST_FORWARD" \
    "$PROXY_IMAGE" >/dev/null
  docker network connect "$EGRESS_NET" "$prx"
  docker start "$prx" >/dev/null

  local mounts=(--mount "type=bind,source=$primary,target=/workspace")
  add_git_masks "$primary" /workspace
  local ro=",readonly"
  [ $rw -eq 1 ] && ro=""
  for e in ${extras[@]+"${extras[@]}"}; do
    mounts+=(--mount "type=bind,source=$e,target=/home/node/$(basename "$e")$ro")
    [ $rw -eq 1 ] && add_git_masks "$e" "/home/node/$(basename "$e")"
  done

  info "Starting sandbox $sbx"
  docker run -d --name "$sbx" \
    --label "$LABEL=1" --label "claude.role=sandbox" --label "claude.project=$name" \
    --label "claude.workspace=$primary" --label "claude.spec=$spec" \
    --network "$nw" \
    --cap-drop ALL --security-opt no-new-privileges \
    --user node --workdir /workspace \
    "${mounts[@]}" \
    --mount "type=volume,source=claude-$name-config,target=/home/node/.claude" \
    --mount "type=volume,source=claude-$name-history,target=/commandhistory" \
    -e "HTTP_PROXY=$PROXY_URL"  -e "HTTPS_PROXY=$PROXY_URL" \
    -e "http_proxy=$PROXY_URL"  -e "https_proxy=$PROXY_URL" \
    -e "NO_PROXY=proxy,localhost,127.0.0.1" \
    -e "no_proxy=proxy,localhost,127.0.0.1" \
    -e CLAUDE_CONFIG_DIR=/home/node/.claude \
    -e CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 \
    -e DISABLE_TELEMETRY=1 \
    -e DISABLE_ERROR_REPORTING=1 \
    -e DISABLE_AUTOUPDATER=1 \
    -e NODE_OPTIONS=--max-old-space-size=4096 \
    -e EDITOR=nvim -e VISUAL=nvim \
    "$SANDBOX_IMAGE" sleep infinity >/dev/null

  # Wait until the proxy answers before bootstrapping (plugin installs need it).
  local i
  for i in 1 2 3 4 5 6 7 8 9 10; do
    docker exec "$sbx" curl -s -o /dev/null --max-time 3 https://api.anthropic.com/ && break
    sleep 1
  done

  local bargs=()
  [ $rw -eq 1 ] && bargs+=(--rw)
  cmd_bootstrap "$sbx" ${bargs[@]+"${bargs[@]}"} ${extras[@]+"${extras[@]}"}
  [ $attach -eq 1 ] && cmd_attach "$name"
  return 0
}

# Configure a fresh sandbox: skip onboarding, trust /workspace, install
# settings, plugins and any MCP servers listed in an optional mcp.json,
# describe extra mounts in CLAUDE.md.
cmd_bootstrap() {
  local c="$1"; shift
  local rw=0
  [ "${1:-}" = "--rw" ] && { rw=1; shift; }

  docker exec -i "$c" python3 - <<'PY'
import json, pathlib
p = pathlib.Path('/home/node/.claude/.claude.json')
d = json.loads(p.read_text()) if p.exists() else {}
d['hasCompletedOnboarding'] = True
d.setdefault('trustedDirectories', [])
if '/workspace' not in d['trustedDirectories']:
    d['trustedDirectories'].append('/workspace')
d.setdefault('projects', {}).setdefault('/workspace', {})['hasTrustDialogAccepted'] = True
p.write_text(json.dumps(d))
PY

  docker exec -i "$c" sh -c 'cat > /home/node/.claude/settings.json' < "$SETTINGS"

  if [ -f "$MCP" ]; then
    docker exec -i "$c" bash -c '
      jq -c ".mcpServers // {} | to_entries[]" | while read -r entry; do
        n=$(jq -r .key <<<"$entry"); v=$(jq -c .value <<<"$entry")
        claude mcp remove -s user "$n" >/dev/null 2>&1 || true
        claude mcp add-json -s user "$n" "$v" >/dev/null 2>&1 && echo "mcp: $n" || echo "mcp: failed to add $n" >&2
      done' < "$MCP"
  fi

  if [ -f "$PLUGINS" ]; then
    # Marketplaces are not auto-registered in a fresh config dir.
    if grep -q '@claude-plugins-official' "$PLUGINS"; then
      docker exec "$c" claude plugin marketplace add anthropics/claude-plugins-official >/dev/null 2>&1 || true
    fi
    if grep -q '@ouroboros' "$PLUGINS"; then
      docker exec "$c" claude plugin marketplace add Q00/ouroboros >/dev/null 2>&1 || true
    fi
    local plugin out
    while IFS= read -r plugin; do
      case "$plugin" in ''|'#'*) continue ;; esac
      if out="$(docker exec "$c" claude plugin install "$plugin" 2>&1)"; then
        echo "plugin: $plugin"
      else
        echo "plugin: $plugin FAILED: $(printf '%s' "$out" | grep -v '^$' | tail -1)" >&2
      fi
    done < "$PLUGINS"
  fi

  if [ $# -gt 0 ]; then
    local mode="read-only" e
    [ $rw -eq 1 ] && mode="read-write"
    {
      printf '# Multi-repo workspace\n\nPrimary workspace: /workspace (read-write)\n\nAdditional repos (%s):\n' "$mode"
      for e in "$@"; do printf -- '- /home/node/%s\n' "$(basename "$e")"; done
      printf '\nUse absolute paths when referencing files across repos.\n'
    } | docker exec -i "$c" sh -c 'cat > /home/node/.claude/CLAUDE.md'
  else
    docker exec "$c" rm -f /home/node/.claude/CLAUDE.md
  fi
}

cmd_attach() {
  local name; name="$(pick_name "${1:-}")" || return 0
  local sbx; sbx="$(sb "$name")"
  running "$sbx" || die "$sbx is not running (try: sandbox.sh up <dir>)"
  local token; token="$(oauth_token)"
  git_identity_env "$(label "$sbx" claude.workspace)"
  printf "$BG_ACTIVE"
  docker exec -it \
    -e "CLAUDE_CODE_OAUTH_TOKEN=$token" \
    -e "CLAUDE_PROJECT_NAME=$name" \
    ${GIT_ENV[@]+"${GIT_ENV[@]}"} \
    "$sbx" bash -c 'clear; exec claude' || true
  printf "$BG_NORMAL"
}

cmd_shell() {
  local name; name="$(pick_name "${1:-}")" || return 0
  local sbx; sbx="$(sb "$name")"
  git_identity_env "$(label "$sbx" claude.workspace)"
  docker exec -it ${GIT_ENV[@]+"${GIT_ENV[@]}"} "$sbx" zsh
}

# A bind-mounted file tracks an inode. git rewrites .git/config by rename, so
# after `git config` or `git remote` on the host, restart to see the new file.
cmd_restart() {
  local name; name="$(pick_name "${1:-}")" || return 0
  docker restart "$(px "$name")" "$(sb "$name")" >/dev/null && info "restarted $name"
}

cmd_ps() {
  docker ps -a --filter "label=claude.role=sandbox" \
    --format 'table {{.Label "claude.project"}}\t{{.Status}}\t{{.Label "claude.workspace"}}'
}

cmd_audit() {
  local name; name="$(pick_name "${1:-}")" || return 0
  printf '== egress via %s (TCP_TUNNEL = allowed, TCP_DENIED = blocked) ==\n' "$(px "$name")"
  docker logs "$(px "$name")" 2>/dev/null | grep -E 'TCP_[A-Z_]+/[0-9]+' || echo "(none)"
  printf '\n== tool calls in %s ==\n' "$(sb "$name")"
  docker exec "$(sb "$name")" cat /home/node/.claude/audit.log 2>/dev/null || echo "(none)"
}

cmd_reload() {
  local c n=0
  for c in $(docker ps -q --filter "label=claude.role=proxy"); do
    docker kill -s HUP "$c" >/dev/null && n=$((n + 1))
  done
  info "reloaded allowlist in $n prox(y|ies)"
}

cmd_clean() {
  local c
  for c in $(docker ps -aq --filter "label=$LABEL=1"); do docker rm -f "$c" >/dev/null; done
  # Containers from the previous devcontainer-based setup
  for c in $(docker ps -aq --filter "label=devcontainer.local_folder") \
           $(docker ps -aq --filter "name=claude-multi-"); do docker rm -f "$c" >/dev/null; done
  for c in $(docker network ls -q --filter "label=$LABEL=1"); do docker network rm "$c" >/dev/null; done
  if [ "${1:-}" = "--volumes" ]; then
    # Current volumes plus those of the previous devcontainer-based setup
    for c in $(docker volume ls -q | grep -E '^claude-(.*-(config|history)|code-config-.*|code-bashhistory-.*|multi-config-.*)$' || true); do
      docker volume rm "$c" >/dev/null
    done
    info "removed sandboxes, networks and volumes"
  else
    info "removed sandboxes and networks (volumes kept; --volumes removes them)"
  fi
}

case "${1:-help}" in
  up)        shift; cmd_up "$@" ;;
  attach)    shift; cmd_attach "$@" ;;
  shell)     shift; cmd_shell "$@" ;;
  restart)   shift; cmd_restart "$@" ;;
  ps)        cmd_ps ;;
  audit)     shift; cmd_audit "$@" ;;
  reload)    cmd_reload ;;
  clean)     shift; cmd_clean "$@" ;;
  build)     shift; cmd_build "$@" ;;
  bootstrap) shift; cmd_bootstrap "$@" ;;
  help|-h|--help) usage ;;
  *) die "unknown command: $1 (see sandbox.sh help)" ;;
esac
