#!/bin/bash
# PreToolUse hook: append one line per tool call to ~/.claude/audit.log.
# Reads the hook payload on stdin and prints nothing, so it never blocks a call.
set -u
log="${CLAUDE_CONFIG_DIR:-$HOME/.claude}/audit.log"
jq -r '[
    (now | strftime("%Y-%m-%dT%H:%M:%SZ")),
    .tool_name,
    ((.tool_input.command // .tool_input.file_path // .tool_input.url // .tool_input.pattern // "")
      | tostring | gsub("\n"; "\\n"))
  ] | join("  ")' >> "$log" 2>/dev/null || true
exit 0
