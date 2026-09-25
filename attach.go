package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// pickName returns the given name, the only running sandbox, or an fzf pick.
func pickName(name string) (string, error) {
	if name != "" {
		return name, nil
	}
	list := dockerOut("ps", "--filter", "label=eredo.role=sandbox", "--format", "{{.Label \"eredo.project\"}}\t{{.Status}}\t{{.Label \"eredo.workspace\"}}")
	if list == "" {
		return "", fmt.Errorf("no running sandboxes")
	}
	lines := strings.Split(list, "\n")
	if len(lines) == 1 {
		return strings.SplitN(lines[0], "\t", 2)[0], nil
	}
	if _, err := exec.LookPath("fzf"); err != nil {
		var names []string
		for _, l := range lines {
			names = append(names, strings.SplitN(l, "\t", 2)[0])
		}
		return "", fmt.Errorf("several sandboxes running (%s): pass a name", strings.Join(names, ", "))
	}
	cmd := exec.Command("fzf", "--header", "Select sandbox", "--height", "10", "--reverse")
	cmd.Stdin = strings.NewReader(list + "\n")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return "", fmt.Errorf("no sandbox selected")
	}
	return strings.SplitN(strings.TrimSpace(string(out)), "\t", 2)[0], nil
}

func first(a []string) string {
	if len(a) > 0 {
		return a[0]
	}
	return ""
}

func cmdAttach(args []string) error {
	flags, rest, err := parseClaudeOpts(args)
	if err != nil {
		return err
	}
	name, err := pickName(first(rest))
	if err != nil {
		return err
	}
	return attachTo(name, flags)
}

func attachTo(name string, claudeFlags []string) error {
	sbx := sb(name)
	if !running(sbx) {
		return fmt.Errorf("%s is not running (try: eredo up <dir>)", sbx)
	}
	token, err := oauthToken()
	if err != nil {
		return err
	}
	args := []string{"exec", "-it", "-e", "CLAUDE_CODE_OAUTH_TOKEN=" + token}
	args = append(args, gitIdentityEnv(label(sbx, "eredo.workspace"))...)
	args = append(args, sbx, "bash", "-c", `clear; exec claude "$@"`, "claude")
	args = append(args, claudeFlags...)
	_ = dockerTTY(args...)
	return nil
}

func cmdShell(args []string) error {
	name, err := pickName(first(args))
	if err != nil {
		return err
	}
	sbx := sb(name)
	a := []string{"exec", "-it"}
	a = append(a, gitIdentityEnv(label(sbx, "eredo.workspace"))...)
	a = append(a, sbx, "zsh")
	_ = dockerTTY(a...)
	return nil
}

// A bind-mounted file tracks an inode. git rewrites .git/config by rename, so
// after `git config` or `git remote` on the host, restart to see the new file.
func cmdRestart(args []string) error {
	name, err := pickName(first(args))
	if err != nil {
		return err
	}
	if err := dockerRun("restart", px(name), sb(name)); err != nil {
		return err
	}
	info("restarted %s", name)
	return nil
}

func cmdPs() error {
	return dockerTTY("ps", "-a", "--filter", "label=eredo.role=sandbox",
		"--format", "table {{.Label \"eredo.project\"}}\t{{.Status}}\t{{.Label \"eredo.workspace\"}}")
}

func cmdAudit(args []string) error {
	name, err := pickName(first(args))
	if err != nil {
		return err
	}
	fmt.Printf("== egress via %s (TCP_TUNNEL = allowed, TCP_DENIED = blocked) ==\n", px(name))
	logs, _ := exec.Command("docker", "logs", px(name)).CombinedOutput()
	n := 0
	for _, l := range strings.Split(string(logs), "\n") {
		if strings.Contains(l, "TCP_") {
			fmt.Println(l)
			n++
		}
	}
	if n == 0 {
		fmt.Println("(none)")
	}
	fmt.Printf("\n== tool calls in %s ==\n", sb(name))
	if out, err := execIn(sb(name), "cat /home/node/.claude/audit.log"); err == nil && out != "" {
		fmt.Println(out)
	} else {
		fmt.Println("(none)")
	}
	return nil
}

// cmdReload regenerates each running project's merged allowlist, makes squid
// re-read it, and copies the current settings.json into the sandbox.
func cmdReload() error {
	n := 0
	settings, _ := configBytes("settings.json")
	for _, c := range strings.Fields(dockerOut("ps", "-q", "--filter", "label=eredo.role=sandbox")) {
		name, ws := label(c, "eredo.project"), label(c, "eredo.workspace")
		if _, err := allowlistDir(name, ws); err != nil {
			return err
		}
		dockerOK("kill", "-s", "HUP", px(name))
		if err := dockerStdin(settings, "exec", "-i", c, "sh", "-c", "cat > /home/node/.claude/settings.json"); err != nil {
			return err
		}
		n++
	}
	info("reloaded allowlist and settings in %d sandbox(es)", n)
	return nil
}
