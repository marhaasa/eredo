package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const trustScript = `import json, os, pathlib
ws = os.environ['MOAT_WS']
p = pathlib.Path('/home/node/.claude/.claude.json')
d = json.loads(p.read_text()) if p.exists() else {}
d['hasCompletedOnboarding'] = True
d.setdefault('trustedDirectories', [])
if ws not in d['trustedDirectories']:
    d['trustedDirectories'].append(ws)
d.setdefault('projects', {}).setdefault(ws, {})['hasTrustDialogAccepted'] = True
p.write_text(json.dumps(d))
`

const mcpScript = `jq -c ".mcpServers // {} | to_entries[]" | while read -r entry; do
  n=$(jq -r .key <<<"$entry"); v=$(jq -c .value <<<"$entry")
  claude mcp remove -s user "$n" >/dev/null 2>&1 || true
  claude mcp add-json -s user "$n" "$v" >/dev/null 2>&1 && echo "mcp: $n" || echo "mcp: failed to add $n" >&2
done`

// bootstrap configures a fresh sandbox: skip onboarding, trust the workspace,
// install settings, the skill, safe.directory entries, MCP servers, plugins,
// and a CLAUDE.md describing extra mounts. Idempotent.
func bootstrap(c string, rw bool, extras []string) error {
	primary := label(c, "moat.workspace")
	cprimary := containerPath(primary)

	if err := dockerStdin([]byte(trustScript), "exec", "-i", "-e", "MOAT_WS="+cprimary, c, "python3", "-"); err != nil {
		return err
	}
	settings, _ := configBytes("settings.json")
	if err := dockerStdin(settings, "exec", "-i", c, "sh", "-c", "cat > /home/node/.claude/settings.json"); err != nil {
		return err
	}
	skill, _ := assets.ReadFile("skills/moat/SKILL.md")
	if err := dockerStdin(skill, "exec", "-i", c, "sh", "-c", "mkdir -p /home/node/.claude/skills/moat && cat > /home/node/.claude/skills/moat/SKILL.md"); err != nil {
		return err
	}

	// Bind mounts from a Windows filesystem show up root-owned; tell git
	// inside that the mounted repos are ours.
	safe := []string{cprimary}
	for _, e := range extras {
		safe = append(safe, containerPath(e))
	}
	script := `for d in "$@"; do git config --global --get-all safe.directory 2>/dev/null | grep -qx "$d" || git config --global --add safe.directory "$d"; done`
	if err := dockerRun(append([]string{"exec", c, "sh", "-c", script, "_"}, safe...)...); err != nil {
		return err
	}

	if p := overridePath("mcp.json"); p != "" {
		b, _ := os.ReadFile(p)
		cmd := exec.Command("docker", "exec", "-i", c, "bash", "-c", mcpScript)
		cmd.Stdin = strings.NewReader(string(b))
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		_ = cmd.Run()
	}

	if plugins, ok := configBytes("plugins.txt"); ok {
		text := string(plugins)
		if strings.Contains(text, "@claude-plugins-official") {
			dockerOK("exec", c, "claude", "plugin", "marketplace", "add", "anthropics/claude-plugins-official")
		}
		if strings.Contains(text, "@ouroboros") {
			dockerOK("exec", c, "claude", "plugin", "marketplace", "add", "Q00/ouroboros")
		}
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if out, err := exec.Command("docker", "exec", c, "claude", "plugin", "install", line).CombinedOutput(); err == nil {
				fmt.Printf("plugin: %s\n", line)
			} else {
				lines := strings.Split(strings.TrimSpace(string(out)), "\n")
				info("plugin: %s FAILED: %s", line, lines[len(lines)-1])
			}
		}
	}

	if len(extras) > 0 {
		mode := "read-only"
		if rw {
			mode = "read-write"
		}
		var md strings.Builder
		fmt.Fprintf(&md, "# Multi-repo workspace\n\nPrimary workspace: %s (read-write)\n\nAdditional repos (%s):\n", cprimary, mode)
		for _, e := range extras {
			fmt.Fprintf(&md, "- %s\n", containerPath(e))
		}
		md.WriteString("\nUse absolute paths when referencing files across repos.\n")
		return dockerStdin([]byte(md.String()), "exec", "-i", c, "sh", "-c", "cat > /home/node/.claude/CLAUDE.md")
	}
	dockerOK("exec", c, "rm", "-f", "/home/node/.claude/CLAUDE.md")
	return nil
}
