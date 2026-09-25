package main

import (
	"fmt"
	"os"
	"path/filepath"
)

const version = "0.2.0-dev"

const usageText = `moat: a Docker sandbox for Claude Code.

Per project, moat creates an internal Docker network (no route to the
outside), a proxy sidecar that is the only thing on that network with egress,
and a sandbox container that runs Claude Code as an unprivileged user with
every capability dropped. The sidecar owns the domain allowlist, so nothing
Claude runs can widen it.

  moat                   same as "moat up ." when the current directory is a git repo
  moat install [bindir]  link moat into ~/.local/bin (unless already on PATH) and
                         write the skill to ~/.claude/skills, so Claude can configure moat
  moat up [--rw] [--rebuild] [--no-attach] [claude opts] <primary> [extra-repo ...]
                         start (or reuse) the sandbox for <primary> and attach.
                         Repos are mounted at their host paths; extras read-only
                         unless --rw is given. --no-attach just starts it.
  moat attach [name] [claude opts]
                         attach Claude Code. Claude opts: --model M, --effort E,
                         --mode P (default, acceptEdits, plan, auto, dontAsk,
                         bypassPermissions); persistent defaults are model,
                         effortLevel and permissions.defaultMode in settings.json
  moat shell   [name]    open a zsh inside
  moat restart [name]    restart a sandbox (re-resolves the .git/config mask)
  moat ps                list sandboxes
  moat audit   [name]    egress log and tool-call log
  moat reload            apply allowlist and settings changes to running sandboxes
  moat clean [--volumes] [name]  remove one or all sandboxes, proxies and networks
  moat build [--pull]    (re)build both images from the sources inside this binary
  moat config [path|init|show]   where overrides live; init copies the shipped files there
  moat doctor [name]     check the host, and a running sandbox
  moat selftest          start a scratch sandbox, run doctor, remove it
  moat version

Configuration: shipped defaults inside the binary, overrides in $MOAT_CONFIG
(default ~/.config/moat), and a per-repo <repo>/.moat/allowlist.txt that adds
domains for that repo. A sandbox is named after the repo directory; set
MOAT_NAME to choose another name.
`

func main() {
	args := os.Args[1:]
	var err error
	if len(args) == 0 {
		cwd, _ := os.Getwd()
		if isDir(filepath.Join(cwd, ".git")) {
			err = cmdUp([]string{cwd})
		} else {
			fmt.Print(usageText)
		}
		exitOn(err)
		return
	}
	rest := args[1:]
	switch args[0] {
	case "up":
		err = cmdUp(rest)
	case "attach":
		err = cmdAttach(rest)
	case "shell":
		err = cmdShell(rest)
	case "restart":
		err = cmdRestart(rest)
	case "ps":
		err = cmdPs()
	case "audit":
		err = cmdAudit(rest)
	case "reload":
		err = cmdReload()
	case "clean":
		err = cmdClean(rest)
	case "build":
		err = cmdBuild(len(rest) > 0 && rest[0] == "--pull")
	case "config":
		err = cmdConfig(rest)
	case "install":
		err = cmdInstall(rest)
	case "doctor":
		os.Exit(cmdDoctor(rest))
	case "selftest":
		err = cmdSelftest()
	case "bootstrap":
		if len(rest) == 0 {
			err = fmt.Errorf("usage: moat bootstrap <container> [--rw] [extra-repo ...]")
		} else {
			rw := len(rest) > 1 && rest[1] == "--rw"
			extras := rest[1:]
			if rw {
				extras = rest[2:]
			}
			err = bootstrap(rest[0], rw, extras)
		}
	case "version", "--version", "-v":
		fmt.Println("moat", version)
	case "help", "-h", "--help":
		fmt.Print(usageText)
	default:
		err = fmt.Errorf("unknown command: %s (see moat help)", args[0])
	}
	exitOn(err)
}

func exitOn(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "moat:", err)
		os.Exit(1)
	}
}
