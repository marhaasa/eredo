package main

import (
	"fmt"
	"os"
	"path/filepath"
)

const version = "0.3.0"

const usageText = `eredo: a Docker sandbox for Claude Code.

Per project, eredo creates an internal Docker network (no route to the
outside), a proxy sidecar that is the only thing on that network with egress,
and a sandbox container that runs Claude Code as an unprivileged user with
every capability dropped. The sidecar owns the domain allowlist, so nothing
Claude runs can widen it.

  eredo                   same as "eredo up ." when the current directory is a git repo
  eredo install [bindir]  link eredo into ~/.local/bin (unless already on PATH) and
                         write the skill to ~/.claude/skills, so Claude can configure eredo
  eredo up [--rw] [--rebuild] [--no-attach] [claude opts] <primary> [extra-repo ...]
                         start (or reuse) the sandbox for <primary> and attach.
                         Repos are mounted at their host paths; extras read-only
                         unless --rw is given. --no-attach just starts it.
  eredo attach [name] [claude opts]
                         attach Claude Code. Claude opts: --model M, --effort E,
                         --mode P (default, acceptEdits, plan, auto, dontAsk,
                         bypassPermissions); persistent defaults are model,
                         effortLevel and permissions.defaultMode in settings.json
  eredo shell   [name]    open a zsh inside
  eredo restart [name]    restart a sandbox (re-resolves the .git/config mask)
  eredo ps                list sandboxes
  eredo audit   [name]    egress log and tool-call log
  eredo reload            apply allowlist and settings changes to running sandboxes
  eredo clean [--volumes] [name]  remove one or all sandboxes, proxies and networks
  eredo relay [name] [--copy [n] | --watch | --clear]
                         commands Claude queued for your host logins (az, gh, ...):
                         list them, copy one, follow the queue, or empty it
  eredo update           pull base images and rebuild, which also updates Claude Code
  eredo build [--pull]    (re)build both images from the sources inside this binary
  eredo config [path|init|show]   where overrides live; init copies the shipped files there
  eredo doctor [name]     check the host, and a running sandbox
  eredo selftest          start a scratch sandbox, run doctor, remove it
  eredo version

Configuration: shipped defaults inside the binary, overrides in $EREDO_CONFIG
(default ~/.config/eredo), and a per-repo <repo>/.eredo/allowlist.txt that adds
domains for that repo. A sandbox is named after the repo directory; set
EREDO_NAME to choose another name.
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
	case "relay":
		err = cmdRelay(rest)
	case "update":
		err = cmdUpdate()
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
			err = fmt.Errorf("usage: eredo bootstrap <container> [--rw] [extra-repo ...]")
		} else {
			rw := len(rest) > 1 && rest[1] == "--rw"
			extras := rest[1:]
			if rw {
				extras = rest[2:]
			}
			err = bootstrap(rest[0], rw, extras)
		}
	case "version", "--version", "-v":
		fmt.Println("eredo", version)
	case "help", "-h", "--help":
		fmt.Print(usageText)
	default:
		err = fmt.Errorf("unknown command: %s (see eredo help)", args[0])
	}
	exitOn(err)
}

func exitOn(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "eredo:", err)
		os.Exit(1)
	}
}
