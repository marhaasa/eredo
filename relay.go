package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// The relay: commands on the relay list (az, gh, kubectl...) need the user's
// logins, which only exist on the host. Inside, eredo-relay-hook stops such a
// Bash call, queues it in the config volume and tells Claude; on the host,
// `eredo relay` lists, copies and follows that queue. Nothing runs on the host
// automatically.

const relayDir = "/home/node/.claude/relay"

// relayEntry is one queued command, as written by sandbox/relay-hook.py.
type relayEntry struct {
	V       int      `json:"v"`
	ID      string   `json:"id"`
	Time    string   `json:"time"`
	Session string   `json:"session"`
	Cwd     string   `json:"cwd"`
	Cmds    []string `json:"cmds"`
	Command string   `json:"command"`
}

var relayNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)

// relayNames parses relay.txt: command names, whitespace separated, #
// comments. Invalid names are returned separately so they can be reported.
func relayNames(b []byte) (good, bad []string) {
	seen := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		for _, w := range strings.Fields(line) {
			switch {
			case !relayNameRe.MatchString(w):
				bad = append(bad, w)
			case !seen[w]:
				seen[w] = true
				good = append(good, w)
			}
		}
	}
	return
}

// effectiveRelayNames is the relay list in effect (override or shipped).
func effectiveRelayNames(warn bool) []string {
	b, _ := configBytes("relay.txt")
	good, bad := relayNames(b)
	if warn {
		for _, w := range bad {
			info("warning: ignored invalid command name in relay.txt: %s", w)
		}
	}
	return good
}

// relayHookRegistered reports whether the effective settings.json runs the
// relay hook. A settings override replaces the shipped file entirely, so an
// older override silently turns the relay off.
func relayHookRegistered() bool {
	s, _ := configBytes("settings.json")
	return bytes.Contains(s, []byte("eredo-relay-hook"))
}

const relayNotRegistered = "warning: your settings.json override does not register eredo-relay-hook, so the relay is off (see README, \"Commands that need your credentials\")"

// writeRelayList delivers the relay list into a sandbox. The hook reads it on
// every call, so a reload applies without a new Claude session.
func writeRelayList(c string, names []string) error {
	list := strings.Join(names, "\n")
	if list != "" {
		list += "\n"
	}
	return dockerStdin([]byte(list), "exec", "-i", c, "sh", "-c",
		"mkdir -p "+relayDir+" && cat > "+relayDir+"/commands.tmp && mv "+relayDir+"/commands.tmp "+relayDir+"/commands")
}

// hostPathFor maps a path inside the sandbox back to the host, the inverse of
// containerPathFor for the primary workspace.
func hostPathFor(goos, hostWS, p string) string {
	if goos != "windows" {
		return p
	}
	cws := containerPathFor(goos, hostWS)
	if p == cws || strings.HasPrefix(p, cws+"/") {
		return hostWS + strings.ReplaceAll(p[len(cws):], "/", `\`)
	}
	return p
}

func readRelayQueue(sbx string) ([]relayEntry, error) {
	out, err := execIn(sbx, "cat "+relayDir+"/queue.jsonl 2>/dev/null; true")
	if err != nil {
		return nil, err
	}
	return parseRelayQueue(out), nil
}

func parseRelayQueue(s string) []relayEntry {
	var q []relayEntry
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e relayEntry
		if json.Unmarshal([]byte(line), &e) == nil && e.Command != "" {
			q = append(q, e)
		}
	}
	return q
}

func cmdRelay(args []string) error {
	var nameArg, action string
	copyN := 0
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--copy":
			action = "copy"
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil {
					copyN = n
					i++
				}
			}
		case "--watch", "--clear":
			action = a[2:]
		default:
			if strings.HasPrefix(a, "-") {
				return fmt.Errorf("unknown option: %s (eredo relay [name] [--copy [n] | --watch | --clear])", a)
			}
			nameArg = a
		}
	}
	name, err := pickName(nameArg)
	if err != nil {
		return err
	}
	sbx := sb(name)
	if !running(sbx) {
		return fmt.Errorf("%s is not running", sbx)
	}
	ws := label(sbx, "eredo.workspace")
	switch action {
	case "watch":
		return relayWatch(name, sbx, ws)
	case "clear":
		_, err := execIn(sbx, `f=`+relayDir+`/queue.jsonl; [ -e "$f" ] && : > "$f"; true`)
		if err == nil {
			info("cleared the relay queue for %s", name)
		}
		return err
	}
	q, err := readRelayQueue(sbx)
	if err != nil {
		return err
	}
	if action == "copy" {
		if len(q) == 0 {
			return fmt.Errorf("the relay queue for %s is empty", name)
		}
		if copyN == 0 {
			copyN = len(q)
		}
		if copyN < 1 || copyN > len(q) {
			return fmt.Errorf("no #%d: the queue has %d entr%s", copyN, len(q), map[bool]string{true: "y", false: "ies"}[len(q) == 1])
		}
		return relayCopy(copyN, q[copyN-1], true)
	}
	if len(q) == 0 {
		names, _ := execIn(sbx, "cat "+relayDir+"/commands 2>/dev/null; true")
		fmt.Printf("relay queue for %s is empty (relaying: %s)\n", name, strings.Join(strings.Fields(names), " "))
		return nil
	}
	fmt.Printf("relay queue for %s, oldest first:\n", name)
	for i, e := range q {
		printRelayEntry(i+1, e, ws)
	}
	fmt.Printf("eredo relay %s --copy [n] copies one (default the latest); --watch follows; --clear empties\n", name)
	return nil
}

func printRelayEntry(n int, e relayEntry, ws string) {
	when := e.Time
	if t, err := time.Parse(time.RFC3339, e.Time); err == nil {
		t = t.Local()
		if y, m, d := t.Date(); y == time.Now().Year() && m == time.Now().Month() && d == time.Now().Day() {
			when = t.Format("15:04")
		} else {
			when = t.Format("Jan 2 15:04")
		}
	}
	cmd, _ := printable(e.Command)
	fmt.Printf("  %d  %s  %s\n", n, when, sanitize(hostPathFor(runtime.GOOS, ws, e.Cwd)))
	for _, line := range strings.Split(cmd, "\n") {
		fmt.Printf("     %s\n", line)
	}
}

// relayCopy puts one queued command on the clipboard. Queued text is written
// by Claude; an entry with control characters is shown escaped and never
// copied, so nothing invisible reaches the user's shell.
func relayCopy(n int, e relayEntry, printRaw bool) error {
	if _, found := printable(e.Command); found {
		return fmt.Errorf("#%d contains control characters (shown escaped in the list); copy it by hand if you trust it", n)
	}
	if printRaw {
		fmt.Println(e.Command)
	}
	tool, err := clipboardCopy(e.Command)
	if err != nil {
		info("no clipboard tool found (pbcopy, wl-copy, xclip, xsel, clip.exe); the command is printed above")
		return nil
	}
	info("copied #%d to the clipboard (%s)", n, tool)
	return nil
}

func isWSL() bool {
	if os.Getenv("WSL_DISTRO_NAME") != "" {
		return true
	}
	_, err := os.Stat("/proc/sys/fs/binfmt_misc/WSLInterop")
	return err == nil
}

// clipboardCopy copies s without a trailing newline. Stdout and stderr stay
// unset: xclip and wl-copy fork a child that would keep pipes open.
func clipboardCopy(s string) (string, error) {
	var candidates [][]string
	switch {
	case runtime.GOOS == "darwin":
		candidates = [][]string{{"pbcopy"}}
	case runtime.GOOS == "windows":
		candidates = [][]string{{"clip.exe"}}
	case isWSL():
		candidates = [][]string{{"clip.exe"}}
	default:
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			candidates = append(candidates, []string{"wl-copy"})
		}
		candidates = append(candidates, []string{"xclip", "-selection", "clipboard"}, []string{"xsel", "--clipboard", "--input"})
	}
	for _, c := range candidates {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Stdin = strings.NewReader(s)
		if err := cmd.Run(); err == nil {
			return c[0], nil
		}
	}
	return "", fmt.Errorf("no clipboard tool")
}

// notify shows a desktop notification. The text is Claude's, so it is only
// ever passed as an argument, never spliced into a script.
func notify(text string) {
	switch runtime.GOOS {
	case "darwin":
		_ = exec.Command("osascript", "-e", "on run argv", "-e", `display notification (item 1 of argv) with title "eredo relay"`, "-e", "end run", text).Run()
	case "linux":
		if _, err := exec.LookPath("notify-send"); err == nil {
			_ = exec.Command("notify-send", "eredo relay", text).Run()
		}
	}
}

// relayWatch follows the queue and copies each new command as it arrives. It
// refreshes a marker inside the sandbox every tick, so the hook can tell
// Claude the command is already on the user's clipboard.
func relayWatch(name, sbx, ws string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	tick := "mkdir -p " + relayDir + ` && echo "copy $(date +%s)" > ` + relayDir + "/watching; cat " + relayDir + "/queue.jsonl 2>/dev/null; true"
	out, err := execIn(sbx, tick)
	if err != nil {
		return err
	}
	seen := len(parseRelayQueue(out))
	fmt.Printf("watching %s: %d queued earlier; each new command is copied to the clipboard. Ctrl-C stops.\n", name, seen)
	for {
		select {
		case <-ctx.Done():
			_, _ = execIn(sbx, "rm -f "+relayDir+"/watching")
			fmt.Println()
			return nil
		case <-time.After(time.Second):
		}
		out, err := execIn(sbx, tick)
		if err != nil {
			if !running(sbx) {
				return fmt.Errorf("%s is not running; stopped watching", sbx)
			}
			continue
		}
		q := parseRelayQueue(out)
		if len(q) < seen {
			seen = 0
		}
		for i := seen; i < len(q); i++ {
			fmt.Print("\a")
			printRelayEntry(i+1, q[i], ws)
			notify(fmt.Sprintf("#%d %s", i+1, strings.Join(q[i].Cmds, " ")))
		}
		if len(q) > seen {
			_ = relayCopy(len(q), q[len(q)-1], false)
			seen = len(q)
		}
	}
}
