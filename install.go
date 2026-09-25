package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// cmdInstall makes `moat` available on PATH and gives Claude Code the moat
// skill (written from the copy inside this binary). Idempotent.
func cmdInstall(args []string) error {
	exe, err := os.Executable()
	if err == nil {
		exe, _ = filepath.EvalSymlinks(exe)
	}
	if runtime.GOOS != "windows" {
		bindir := filepath.Join(home(), ".local", "bin")
		if len(args) > 0 {
			bindir = args[0]
		}
		have, _ := exec.LookPath("moat")
		if have == "" || have == filepath.Join(bindir, "moat") {
			if err := os.MkdirAll(bindir, 0o755); err != nil {
				return err
			}
			link := filepath.Join(bindir, "moat")
			os.Remove(link)
			if err := os.Symlink(exe, link); err != nil {
				return err
			}
			info("linked moat into %s", bindir)
			if !onPath(bindir) {
				info("note: %s is not on your PATH", bindir)
			}
		} else {
			info("moat already on PATH: %s", have)
		}
	} else if have, _ := exec.LookPath("moat"); have == "" {
		info("put %s somewhere on your PATH, for example %%LOCALAPPDATA%%\\Programs\\moat", exe)
	}
	skillDir := filepath.Join(home(), ".claude", "skills", "moat")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return err
	}
	skill, _ := assets.ReadFile("skills/moat/SKILL.md")
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), skill, 0o644); err != nil {
		return err
	}
	info("skill written to %s: ask Claude to allow a domain or add a plugin", skillDir)
	info("next: cd into a repo and run: moat")
	return nil
}

func onPath(dir string) bool {
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if p == dir {
			return true
		}
	}
	return false
}

func fmtPath(p string) string { return fmt.Sprint(p) }
