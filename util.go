package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func info(format string, a ...any) { fmt.Fprintf(os.Stderr, format+"\n", a...) }

func home() string { h, _ := os.UserHomeDir(); return h }

// env returns the variable's value, or def when it is unset or empty.
func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func isDir(p string) bool  { st, err := os.Stat(p); return err == nil && st.IsDir() }
func isFile(p string) bool { st, err := os.Stat(p); return err == nil && !st.IsDir() }

// absDir canonicalises a directory the way `cd dir && pwd -P` would.
func absDir(p string) (string, error) {
	a, err := filepath.Abs(p)
	if err == nil {
		a, err = filepath.EvalSymlinks(a)
	}
	if err != nil || !isDir(a) {
		return "", fmt.Errorf("not a directory: %s", p)
	}
	return a, nil
}

var (
	configDir = env("EREDO_CONFIG", filepath.Join(env("XDG_CONFIG_HOME", filepath.Join(home(), ".config")), "eredo"))
	stateDir  = env("EREDO_STATE", filepath.Join(env("XDG_STATE_HOME", filepath.Join(home(), ".local", "state")), "eredo"))
)

// containerPath maps a host path to the path used inside the sandbox. On
// Linux and macOS they are identical, so Claude's working directory reads the
// same on both sides. On Windows, C:\Users\me\src becomes /c/Users/me/src.
func containerPath(host string) string { return containerPathFor(runtime.GOOS, host) }

func containerPathFor(goos, host string) string {
	if goos != "windows" {
		return host
	}
	p := strings.ReplaceAll(host, `\`, "/")
	if len(p) >= 2 && p[1] == ':' {
		p = "/" + strings.ToLower(p[:1]) + p[2:]
	}
	return p
}

// hostTZ: TZ, else /etc/localtime, else /etc/timezone, else UTC.
func hostTZ() string {
	if tz := os.Getenv("TZ"); tz != "" {
		return tz
	}
	if l, err := os.Readlink("/etc/localtime"); err == nil {
		if i := strings.Index(l, "zoneinfo/"); i >= 0 {
			return l[i+len("zoneinfo/"):]
		}
	}
	if b, err := os.ReadFile("/etc/timezone"); err == nil && strings.TrimSpace(string(b)) != "" {
		return strings.TrimSpace(string(b))
	}
	return "UTC"
}

// writeEmbeddedDir copies an embedded directory tree to disk.
func writeEmbeddedDir(src, dst string) error {
	return fs.WalkDir(assets, src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, filepath.FromSlash(path))
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		b, err := assets.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(out, b, 0o755)
	})
}

// ---- docker helpers -------------------------------------------------------

func dockerOut(args ...string) string {
	out, _ := exec.Command("docker", args...).Output()
	return strings.TrimSpace(string(out))
}

func dockerOK(args ...string) bool { return exec.Command("docker", args...).Run() == nil }

func dockerRun(args ...string) error {
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker %s: %s", args[0], strings.TrimSpace(string(out)))
	}
	return nil
}

// dockerTTY runs docker with the terminal attached (builds, interactive execs).
func dockerTTY(args ...string) error {
	cmd := exec.Command("docker", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// dockerStdin runs docker with the given bytes on stdin.
func dockerStdin(input []byte, args ...string) error {
	cmd := exec.Command("docker", args...)
	cmd.Stdin = strings.NewReader(string(input))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker %s: %s", args[0], strings.TrimSpace(string(out)))
	}
	return nil
}

func exists(c string) bool { return dockerOK("container", "inspect", c) }
func running(c string) bool {
	return dockerOut("container", "inspect", "-f", "{{.State.Running}}", c) == "true"
}
func label(c, key string) string {
	return dockerOut("container", "inspect", "-f", `{{index .Config.Labels "`+key+`"}}`, c)
}

// execIn runs a shell snippet inside a container.
func execIn(c, script string) (string, error) {
	out, err := exec.Command("docker", "exec", c, "sh", "-c", script).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
func execInOK(c, script string) bool { _, err := execIn(c, script); return err == nil }

func ensureDocker() error {
	if _, err := exec.LookPath("docker"); err != nil {
		return errors.New("docker not found")
	}
	if !dockerOK("info") {
		return errors.New("Docker daemon is not running")
	}
	return nil
}

func sb(name string) string  { return "eredo-" + name }
func px(name string) string  { return "eredo-" + name + "-proxy" }
func net(name string) string { return "eredo-" + name + "-net" }

const (
	egressNet    = "eredo-egress"
	labelKey     = "eredo.sandbox"
	proxyURL     = "http://proxy:3128"
	specVersion  = "v7"
	sandboxImage = "eredo-sandbox:local"
	proxyImage   = "eredo-proxy:local"
)

func imageNames() (string, string) {
	return env("EREDO_IMAGE", sandboxImage), env("EREDO_PROXY_IMAGE", proxyImage)
}
