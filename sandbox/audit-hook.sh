#!/bin/bash
# Hook for SessionStart, UserPromptSubmit and PreToolUse. Two jobs:
#   1. record the current permission mode, which the status line shows
#      (the status line's own input does not include it);
#   2. on tool calls, append one line to ~/.claude/audit.log.
# Reads the hook payload on stdin and prints nothing, so it never blocks a call.
set -u
dir="${CLAUDE_CONFIG_DIR:-$HOME/.claude}"
input="$(cat)"

mode="$(jq -r '.permission_mode // empty' <<<"$input" 2>/dev/null)"
[ -n "$mode" ] && printf '%s\n' "$mode" > "$dir/permission_mode" 2>/dev/null

if [ "$(jq -r '.tool_name // empty' <<<"$input" 2>/dev/null)" != "" ]; then
  jq -r '[
      (now | strftime("%Y-%m-%dT%H:%M:%SZ")),
      .tool_name,
      ((.tool_input.command // .tool_input.file_path // .tool_input.url // .tool_input.pattern // "")
        | tostring | gsub("\n"; "\\n"))
    ] | join("  ")' <<<"$input" >> "$dir/audit.log" 2>/dev/null || true
fi
exit 0
