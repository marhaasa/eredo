package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func cmdUp(args []string) error {
	claudeFlags, rest, err := parseClaudeOpts(args)
	if err != nil {
		return err
	}
	rw, rebuild, attach := false, false, true
	var paths []string
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch a {
		case "--rw":
			rw = true
		case "--rebuild":
			rebuild = true
		case "--no-attach":
			attach = false
		case "--":
			paths = append(paths, rest[i+1:]...)
			i = len(rest)
		default:
			if strings.HasPrefix(a, "-") {
				return fmt.Errorf("unknown option: %s", a)
			}
			paths = append(paths, a)
		}
	}
	if len(paths) == 0 {
		return fmt.Errorf("usage: eredo up [--rw] [--rebuild] [--no-attach] <primary> [extra-repo ...]")
	}
	primary, err := absDir(paths[0])
	if err != nil {
		return err
	}
	var extras []string
	for _, e := range paths[1:] {
		p, err := absDir(e)
		if err != nil {
			return err
		}
		extras = append(extras, p)
	}

	name := env("EREDO_NAME", filepath.Base(primary))
	sbx, prx, nw := sb(name), px(name), net(name)
	rwInt := 0
	if rw {
		rwInt = 1
	}
	if err := ensureDocker(); err != nil {
		return err
	}
	if rebuild {
		if err := cmdBuild(false); err != nil {
			return err
		}
	}
	if err := ensureImages(); err != nil {
		return err
	}
	spec := fmt.Sprintf("%s %s rw=%d img=%s %s", specVersion, primary, rwInt, imageIDs(), strings.Join(extras, " "))

	// Two repos with the same directory name would otherwise fight over one sandbox.
	if exists(sbx) {
		if other := label(sbx, "eredo.workspace"); other != primary {
			return fmt.Errorf("sandbox name '%s' is in use by %s; set EREDO_NAME=<name> or run: eredo clean %s", name, other, name)
		}
	}
	// Reuse an existing sandbox when its mounts are unchanged.
	if exists(sbx) && !rebuild && label(sbx, "eredo.spec") == spec {
		if !running(sbx) {
			info("Starting %s", sbx)
			if err := dockerRun("start", prx, sbx); err != nil {
				return err
			}
		}
		if attach {
			return attachTo(name, claudeFlags)
		}
		return nil
	}
	if exists(sbx) {
		info("Recreating %s (mounts or image changed)", sbx)
		removeProject(name)
	}

	if !dockerOK("network", "inspect", egressNet) {
		if err := dockerRun("network", "create", egressNet); err != nil {
			return err
		}
	}
	if !dockerOK("network", "inspect", nw) {
		if err := dockerRun("network", "create", "--internal", "--label", labelKey+"=1", nw); err != nil {
			return err
		}
	}

	adir, err := allowlistDir(name, primary)
	if err != nil {
		return err
	}
	sandboxImg, proxyImg := imageNames()
	info("Starting proxy %s (allowlist: %s)", prx, filepath.Join(adir, "allowlist.txt"))
	if err := dockerRun("create", "--name", prx,
		"--label", labelKey+"=1", "--label", "eredo.role=proxy", "--label", "eredo.project="+name,
		"--network", nw, "--network-alias", "proxy",
		"--add-host", "host.docker.internal:host-gateway",
		"--mount", "type=bind,source="+adir+",target=/etc/eredo,readonly",
		"-e", "FORWARD="+os.Getenv("EREDO_FORWARD"),
		proxyImg); err != nil {
		return err
	}
	if err := dockerRun("network", "connect", egressNet, prx); err != nil {
		return err
	}
	if err := dockerRun("start", prx); err != nil {
		return err
	}

	// Same paths inside as on the host (mapped on Windows), so Claude's cwd,
	// errors and file references read the same on both sides.
	cprimary := containerPath(primary)
	mounts := []string{"--mount", "type=bind,source=" + primary + ",target=" + cprimary}
	pm, notes := repoProtections(primary, cprimary)
	mounts = append(mounts, pm...)
	for _, e := range extras {
		ro := ",readonly"
		if rw {
			ro = ""
		}
		mounts = append(mounts, "--mount", "type=bind,source="+e+",target="+containerPath(e)+ro)
		if rw {
			em, en := repoProtections(e, containerPath(e))
			mounts = append(mounts, em...)
			notes = append(notes, en...)
		}
	}

	for _, n := range notes {
		info("%s", n)
	}
	// Policy (permissions, hooks, status line) goes in Claude Code's managed
	// settings, read-only, so nothing inside can rewrite its own rules.
	claudeDir, err := writeManagedSettings(name)
	if err != nil {
		return err
	}
	mounts = append(mounts, "--mount", "type=bind,source="+claudeDir+",target=/etc/claude-code,readonly")

	info("Starting sandbox %s", sbx)
	runArgs := []string{"run", "-d", "--name", sbx,
		"--label", labelKey + "=1", "--label", "eredo.role=sandbox", "--label", "eredo.project=" + name,
		"--label", "eredo.workspace=" + primary, "--label", "eredo.spec=" + spec,
		"--network", nw,
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--init", "--pids-limit", "4096",
		"--user", "node", "--workdir", cprimary}
	runArgs = append(runArgs, mounts...)
	runArgs = append(runArgs,
		"--mount", "type=volume,source=eredo-"+name+"-config,target=/home/node/.claude",
		"--mount", "type=volume,source=eredo-"+name+"-history,target=/commandhistory",
		"-e", "HTTP_PROXY="+proxyURL, "-e", "HTTPS_PROXY="+proxyURL,
		"-e", "http_proxy="+proxyURL, "-e", "https_proxy="+proxyURL,
		"-e", "NO_PROXY=proxy,localhost,127.0.0.1", "-e", "no_proxy=proxy,localhost,127.0.0.1",
		"-e", "CLAUDE_CONFIG_DIR=/home/node/.claude",
		"-e", "CLAUDE_PROJECT_NAME="+name,
		"-e", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"-e", "DISABLE_TELEMETRY=1", "-e", "DISABLE_ERROR_REPORTING=1", "-e", "DISABLE_AUTOUPDATER=1",
		"-e", "ENABLE_CLAUDEAI_MCP_SERVERS=false",
		"-e", "NODE_OPTIONS=--max-old-space-size=4096",
		sandboxImg, "sleep", "infinity")
	if err := dockerRun(runArgs...); err != nil {
		return err
	}

	// Wait until the proxy answers before bootstrapping (plugin installs need it).
	for i := 0; i < 10; i++ {
		if dockerOK("exec", sbx, "curl", "-s", "-o", "/dev/null", "--max-time", "3", "https://api.anthropic.com/") {
			break
		}
		time.Sleep(time.Second)
	}
	if err := bootstrap(sbx, rw, extras); err != nil {
		return err
	}
	if attach {
		return attachTo(name, claudeFlags)
	}
	return nil
}

func removeProject(name string) {
	dockerOK("rm", "-f", sb(name), px(name))
	dockerOK("network", "rm", net(name))
}
