#!/bin/bash
# Claude Code status line inside the sandbox: the always-visible cue that this
# session is sandboxed, plus model, effort and permission mode. Claude Code
# pipes session JSON to stdin and shows whatever is printed. Works on every
# platform and terminal, unlike a terminal background tint.
input="$(cat)"
dir="${CLAUDE_CONFIG_DIR:-$HOME/.claude}"
model="$(jq -r '.model.display_name // empty' <<<"$input")"
effort="$(jq -r '.effort.level // empty' <<<"$input")"
cwd="$(jq -r '.workspace.current_dir // .cwd // empty' <<<"$input")"
name="${CLAUDE_PROJECT_NAME:-${cwd##*/}}"
branch="$(git -C "$cwd" branch --show-current 2>/dev/null || true)"
ctx="$(jq -r '.context_window.used_percentage // empty' <<<"$input")"

# Permission mode: written by the hook on every event; before the first event
# fall back to the configured default.
mode="$(cat "$dir/permission_mode" 2>/dev/null || true)"
[ -n "$mode" ] || mode="$(jq -r '.permissions.defaultMode // "default"' "$dir/settings.json" 2>/dev/null || echo default)"

red=$'\033[1;38;5;124m'; reset=$'\033[0m'
out="${red}sandboxed${reset} · $name"
[ -n "$branch" ] && out="$out · $branch"
[ -n "$model" ]  && out="$out · $model"
[ -n "$effort" ] && out="$out · $effort effort"
case "$mode" in
  bypassPermissions|dontAsk) out="$out · ${red}$mode mode${reset}" ;;
  *) out="$out · $mode mode" ;;
esac
[ -n "$ctx" ] && out="$out · ${ctx%.*}% ctx"
printf '%s\n' "$out"
