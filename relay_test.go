package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func needPython(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
}

func relayHeads(t *testing.T, cmd string) []string {
	t.Helper()
	out, err := exec.Command("python3", "sandbox/relay-hook.py", "--heads", cmd).Output()
	if err != nil {
		t.Fatalf("heads %q: %v", cmd, err)
	}
	return strings.Fields(string(out))
}

func hasAny(heads []string, names ...string) bool {
	for _, h := range heads {
		for _, n := range names {
			if h == n {
				return true
			}
		}
	}
	return false
}

func TestRelayHeads(t *testing.T) {
	needPython(t)
	intercepted := []string{
		"az",
		"az account show",
		"cd infra && az deployment group create -g rg --template-file main.bicep",
		`FOO=1 BAR="a b" az login`,
		"az login && az account show",
		"az account show -o json | jq -r .id",
		"npm test; gh pr create --fill",
		`echo "sub: $(az account show)"`,
		"id=`gh api user`",
		"bash -c 'az login'",
		`sh -lc "kubectl get pods"`,
		`eval "az login"`,
		"timeout 30 kubectl get pods",
		"env -i PATH=/x az version",
		"command az version",
		"nohup az x &",
		"xargs -n 1 az group delete -n",
		"/usr/bin/gh auth status",
		"(cd x && az y)",
		"{ az y; }",
		"if az group exists -n rg; then echo y; fi",
		"! az x",
		`for g in a b; do az group show -n "$g"; done`,
		"diff <(az x) <(az y)",
		"az rest --body @- <<'EOF'\n{\"a\": 1}\nEOF",
		"az x 2>&1 | tee out.txt",
		"sudo -u root az x",
	}
	for _, c := range intercepted {
		if !hasAny(relayHeads(t, c), "az", "gh", "kubectl") {
			t.Errorf("should intercept: %q (heads %v)", c, relayHeads(t, c))
		}
	}
	passed := []string{
		"echo az login",
		"man az",
		"which az",
		"command -v az",
		`git commit -m "fix: az login; then gh auth"`,
		"grep -rn 'az account' .",
		`rg "kubectl apply"`,
		"cat > notes.md <<'EOF'\naz login\ngh pr list\nEOF",
		"git commit -m \"$(cat <<'EOF'\nci: run az login first\nEOF\n)\"",
		"ls # then az login",
		"echo '$(az login)'",
		"azure-thing",
		"ghq get",
		"npm run az",
		"./gh-tools/run",
		"echo x > az",
		"cat < gh",
	}
	for _, c := range passed {
		if hasAny(relayHeads(t, c), "az", "gh", "kubectl") {
			t.Errorf("should not intercept: %q (heads %v)", c, relayHeads(t, c))
		}
	}
}

func runRelayHook(t *testing.T, dir, input string) string {
	t.Helper()
	cmd := exec.Command("python3", "sandbox/relay-hook.py")
	cmd.Env = append(os.Environ(), "EREDO_RELAY_DIR="+dir, "CLAUDE_PROJECT_NAME=app")
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("hook: %v", err)
	}
	return string(out)
}

func TestRelayHook(t *testing.T) {
	needPython(t)
	dir := t.TempDir()
	in := func(c string) string {
		b, _ := json.Marshal(map[string]any{"tool_name": "Bash", "tool_input": map[string]string{"command": c}, "cwd": "/w/app", "session_id": "s1", "tool_use_id": "toolu_1"})
		return string(b)
	}
	if out := runRelayHook(t, dir, in("az login")); out != "" {
		t.Fatalf("no list means relay off, got %q", out)
	}
	os.WriteFile(filepath.Join(dir, "commands"), []byte("# relay\naz\ngh\n"), 0o644)
	if out := runRelayHook(t, dir, in("ls -la")); out != "" {
		t.Fatalf("unrelated command must pass, got %q", out)
	}
	out := runRelayHook(t, dir, in("cd infra && az account show"))
	var r struct {
		HookSpecificOutput struct {
			PermissionDecision       string `json:"permissionDecision"`
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("bad hook output %q: %v", out, err)
	}
	if r.HookSpecificOutput.PermissionDecision != "deny" || !strings.Contains(r.HookSpecificOutput.PermissionDecisionReason, "queued for the user as #1") ||
		!strings.Contains(r.HookSpecificOutput.PermissionDecisionReason, "eredo relay app --copy 1") {
		t.Fatalf("unexpected decision: %+v", r)
	}
	runRelayHook(t, dir, in("cd infra && az account show")) // immediate retry: no new entry
	q, _ := os.ReadFile(filepath.Join(dir, "queue.jsonl"))
	lines := strings.Split(strings.TrimSpace(string(q)), "\n")
	if len(lines) != 1 {
		t.Fatalf("want 1 queue line, got %d: %s", len(lines), q)
	}
	var e relayEntry
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil || e.Command != "cd infra && az account show" || e.Cwd != "/w/app" || e.ID != "toolu_1" || len(e.Cmds) != 1 || e.Cmds[0] != "az" {
		t.Fatalf("bad entry %s (%v)", lines[0], err)
	}
}

func TestRelayNames(t *testing.T) {
	good, bad := relayNames([]byte("# c\naz gh  # two\nkubectl\nrm -rf\naz\n"))
	if strings.Join(good, " ") != "az gh kubectl rm" || strings.Join(bad, " ") != "-rf" {
		t.Fatalf("good=%v bad=%v", good, bad)
	}
}

func TestPrintable(t *testing.T) {
	if s, found := printable("ok\tline\n"); found || s != "ok\tline\n" {
		t.Fatalf("plain text changed: %q", s)
	}
	if s, found := printable("a\x1b]52;c;x\x07b\u202e"); !found || strings.ContainsAny(s, "\x1b\x07\u202e") {
		t.Fatalf("controls not escaped: %q", s)
	}
}

func TestHostPathFor(t *testing.T) {
	if got := hostPathFor("windows", `C:\Users\me\app`, "/c/Users/me/app/infra"); got != `C:\Users\me\app\infra` {
		t.Fatalf("windows: %s", got)
	}
	if got := hostPathFor("darwin", "/Users/me/app", "/Users/me/app/x"); got != "/Users/me/app/x" {
		t.Fatalf("unix: %s", got)
	}
}

func TestExpiryWarning(t *testing.T) {
	now := mustTime("2026-09-25T10:00:00Z")
	if expiryWarning(0, now) != "" || expiryWarning(now.Add(2*3600e9).UnixMilli(), now) != "" {
		t.Fatal("no warning expected")
	}
	if !strings.Contains(expiryWarning(now.Add(-60e9).UnixMilli(), now), "expired") {
		t.Fatal("expired not reported")
	}
	if !strings.Contains(expiryWarning(now.Add(10*60e9).UnixMilli(), now), "expires in") {
		t.Fatal("soon not reported")
	}
}

func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}
