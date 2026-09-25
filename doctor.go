package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// cmdDoctor verifies the host, then every isolation property of a running
// sandbox: the given one, or the first one running. Returns the number of
// failed checks.
func cmdDoctor(args []string) int {
	fails := 0
	ok := func(f string, a ...any) { fmt.Printf("  ok    "+f+"\n", a...) }
	bad := func(f string, a ...any) { fmt.Printf("  FAIL  "+f+"\n", a...); fails++ }
	warn := func(f string, a ...any) { fmt.Printf("  warn  "+f+"\n", a...) }

	fmt.Println("host")
	if _, err := exec.LookPath("docker"); err == nil {
		ok("docker cli %s", dockerOut("version", "--format", "{{.Client.Version}}"))
	} else {
		bad("docker cli missing")
	}
	if dockerOK("info") {
		ok("docker daemon running")
	} else {
		bad("docker daemon not running")
	}
	sandboxImg, proxyImg := imageNames()
	for _, img := range []string{sandboxImg, proxyImg} {
		if dockerOK("image", "inspect", img) {
			ok("image %s", img)
		} else {
			warn("image %s not built (eredo build)", img)
		}
	}
	if _, err := exec.LookPath("git"); err == nil {
		ok("git")
	} else {
		bad("git missing")
	}
	if tok, err := oauthToken(); err == nil {
		ok("claude credential found (%d chars)", len(tok))
	} else {
		warn("no Claude credential: needed to attach, not to run checks")
	}
	if isDir(configDir) {
		ok("config overrides in %s", configDir)
	} else {
		ok("no config overrides, shipped defaults apply")
	}

	name := first(args)
	if name == "" {
		name = first(strings.Fields(dockerOut("ps", "--filter", "label=eredo.role=sandbox", "--format", "{{.Label \"eredo.project\"}}")))
	}
	if name == "" {
		fmt.Println("\nno running sandbox to check (start one: eredo up <dir>)")
		return fails
	}
	sbx, prx := sb(name), px(name)
	fmt.Printf("\nsandbox %s\n", name)
	if !running(sbx) {
		bad("container not running")
		fmt.Println()
		return fails
	}
	ok("container running")
	if running(prx) {
		ok("proxy running")
	} else {
		bad("proxy not running")
	}
	X := func(s string) bool { return execInOK(sbx, s) }
	out := func(s string) string { o, _ := execIn(sbx, s); return o }
	if out("id -u") != "0" {
		ok("unprivileged user %s", out("id -un"))
	} else {
		bad("running as root")
	}
	if f := strings.Fields(out("grep CapEff /proc/self/status")); len(f) == 2 && f[1] == "0000000000000000" {
		ok("no capabilities")
	} else {
		bad("capabilities present")
	}
	if X("command -v sudo") {
		bad("sudo present")
	} else {
		ok("no sudo")
	}
	if X(`awk 'NR>1 && $2=="00000000"{f=1} END{exit !f}' /proc/net/route`) {
		bad("default route present")
	} else {
		ok("no default route")
	}
	if X(`curl --noproxy "*" -s --max-time 5 -o /dev/null https://api.anthropic.com/`) {
		bad("direct egress possible")
	} else {
		ok("direct egress blocked")
	}
	if X(`curl -s --max-time 15 -o /dev/null https://api.anthropic.com/`) {
		ok("api.anthropic.com reachable through the proxy")
	} else {
		bad("api.anthropic.com not reachable through the proxy")
	}
	if X(`curl -s --max-time 15 -o /dev/null https://example.com/`) {
		bad("unlisted domain reachable (allowlist not enforced)")
	} else {
		ok("unlisted domain blocked")
	}
	if X(`curl -s --max-time 10 -o /dev/null https://10.255.255.1/`) {
		bad("private address reachable through the proxy")
	} else {
		ok("private addresses blocked")
	}
	ws := containerPath(label(sbx, "eredo.workspace"))
	if out("pwd") == ws {
		ok("workspace at its host path %s", ws)
	} else {
		bad("workspace path differs from host")
	}
	if X("test -d '" + ws + "/.git'") {
		if X("touch '" + ws + "/.git/hooks/.doctor'") {
			bad(".git/hooks writable")
			X("rm -f '" + ws + "/.git/hooks/.doctor'")
		} else {
			ok(".git/hooks read-only")
		}
		if X("git -C '" + ws + "' config doctor.probe 1") {
			bad(".git/config writable")
			X("git -C '" + ws + "' config --unset doctor.probe")
		} else {
			ok(".git/config read-only")
		}
	}
	if X("test -s /home/node/.claude/settings.json") {
		ok("settings.json installed")
	} else {
		bad("settings.json missing")
	}
	if X("test -x /usr/local/bin/eredo-audit-hook && test -x /usr/local/bin/eredo-statusline") {
		ok("audit hook and status line installed")
	} else {
		bad("audit hook or status line missing")
	}
	if v := out("claude --version"); v != "" && X("claude --version") {
		ok("claude %s", strings.Fields(v)[0])
	} else {
		bad("claude not runnable")
	}
	fmt.Println()
	if fails == 0 {
		fmt.Println("all checks passed")
	} else {
		fmt.Printf("%d check(s) failed\n", fails)
	}
	return fails
}

// cmdSelftest brings up a sandbox on a scratch repo with no plugins, runs
// doctor against it, and removes it again. Used by CI.
func cmdSelftest() error {
	if err := ensureDocker(); err != nil {
		return err
	}
	if err := ensureImages(); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "eredo-selftest")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	tmp, _ = filepath.EvalSymlinks(tmp)
	repo, cfg := filepath.Join(tmp, "selftest"), filepath.Join(tmp, "config")
	os.MkdirAll(repo, 0o755)
	os.MkdirAll(cfg, 0o755)
	if err := exec.Command("git", "-C", repo, "init", "-q").Run(); err != nil {
		return fmt.Errorf("git init: %w", err)
	}
	if err := exec.Command("git", "-C", repo, "-c", "user.name=selftest", "-c", "user.email=selftest@example.invalid", "commit", "-q", "--allow-empty", "-m", "init").Run(); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}
	os.WriteFile(filepath.Join(cfg, "plugins.txt"), []byte("# selftest: no plugins\n"), 0o644)
	configDir, stateDir = cfg, filepath.Join(tmp, "state")
	defer cleanProject("selftest", true)
	if err := cmdUp([]string{"--no-attach", repo}); err != nil {
		return err
	}
	if n := cmdDoctor([]string{"selftest"}); n > 0 {
		return fmt.Errorf("%d check(s) failed", n)
	}
	return nil
}
