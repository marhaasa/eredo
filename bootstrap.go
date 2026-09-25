package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const trustScript = `import json, os, pathlib
ws = os.environ['EREDO_WS']
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
	primary := label(c, "eredo.workspace")
	cprimary := containerPath(primary)

	if err := dockerStdin([]byte(trustScript), "exec", "-i", "-e", "EREDO_WS="+cprimary, c, "python3", "-"); err != nil {
		return err
	}
	// Policy lives in read-only managed settings (see writeManagedSettings).
	// The user settings file inside only keeps preferences; a copy of the
	// policy left there by an older eredo would register every hook twice.
	resetUser := `f=/home/node/.claude/settings.json; if [ ! -s "$f" ] || grep -q eredo-audit-hook "$f"; then echo '{}' > "$f"; fi`
	if err := dockerRun("exec", c, "sh", "-c", resetUser); err != nil {
		return err
	}
	if err := writeRelayList(c, effectiveRelayNames(true)); err != nil {
		return err
	}
	if !relayHookRegistered() {
		info("%s", relayNotRegistered)
	}
	skill, _ := assets.ReadFile("skills/eredo/SKILL.md")
	if err := dockerStdin(skill, "exec", "-i", c, "sh", "-c", "mkdir -p /home/node/.claude/skills/eredo && cat > /home/node/.claude/skills/eredo/SKILL.md"); err != nil {
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
		out, _ := cmd.CombinedOutput()
		if s := strings.TrimSpace(string(out)); s != "" {
			fmt.Println(sanitize(s))
		}
	}

	if plugins, ok := configBytes("plugins.txt"); ok {
		text := string(plugins)
		if strings.Contains(text, "@claude-plugins-official") {
			dockerOK("exec", c, "claude", "plugin", "marketplace", "add", "anthropics/claude-plugins-official")
		}
		for _, m := range marketplaces(text) {
			dockerOK("exec", c, "claude", "plugin", "marketplace", "add", m)
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
				info("plugin: %s FAILED: %s", line, sanitize(lines[len(lines)-1]))
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

// marketplaces lists the plugin marketplaces to register, from lines like
// "# marketplace: owner/repo" in plugins.txt. A fresh config dir knows none,
// not even the official one.
func marketplaces(plugins string) []string {
	var out []string
	if strings.Contains(plugins, "@claude-plugins-official") {
		out = append(out, "anthropics/claude-plugins-official")
	}
	for _, line := range strings.Split(plugins, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "#"); ok {
			if m, ok := strings.CutPrefix(strings.TrimSpace(rest), "marketplace:"); ok {
				if m = strings.TrimSpace(m); m != "" {
					out = append(out, m)
				}
			}
		}
	}
	return out
}
