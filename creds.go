package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// oauthToken finds the Claude Code token: CLAUDE_CODE_OAUTH_TOKEN, then the
// macOS keychain, then the credentials file Claude Code writes on Linux,
// WSL and Windows.
func oauthToken() (string, error) {
	if t := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); t != "" {
		return t, nil
	}
	var raw []byte
	if runtime.GOOS == "darwin" {
		if out, err := exec.Command("security", "find-generic-password", "-s", "Claude Code-credentials", "-w").Output(); err == nil {
			raw = out
		}
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		file := filepath.Join(env("CLAUDE_CONFIG_DIR", filepath.Join(home(), ".claude")), ".credentials.json")
		raw, _ = os.ReadFile(file)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return "", errors.New("no Claude Code credentials found: run claude on the host and log in, or export CLAUDE_CODE_OAUTH_TOKEN from 'claude setup-token'")
	}
	var c struct {
		ClaudeAiOauth struct {
			AccessToken string `json:"accessToken"`
			ExpiresAt   int64  `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(raw), &c); err != nil || c.ClaudeAiOauth.AccessToken == "" {
		return "", errors.New("could not read claudeAiOauth.accessToken from the Claude Code credentials")
	}
	if w := expiryWarning(c.ClaudeAiOauth.ExpiresAt, time.Now()); w != "" {
		info("%s", w)
	}
	return c.ClaudeAiOauth.AccessToken, nil
}

// expiryWarning explains an OAuth access token that is expired or about to
// expire. The token inside the sandbox cannot refresh itself, so a stale one
// shows up as a confusing 401 later. expiresAt is in milliseconds.
func expiryWarning(expiresAt int64, now time.Time) string {
	if expiresAt <= 0 {
		return ""
	}
	left := time.UnixMilli(expiresAt).Sub(now)
	switch {
	case left <= 0:
		return "warning: the Claude Code login on this machine has expired; run claude on the host once to refresh it, or use a long-lived token from 'claude setup-token' (export CLAUDE_CODE_OAUTH_TOKEN)"
	case left < 30*time.Minute:
		return fmt.Sprintf("warning: the Claude Code login on this machine expires in %d minutes; the sandbox cannot refresh it (run claude on the host, or use 'claude setup-token')", int(left.Minutes())+1)
	}
	return ""
}

// gitIdentityEnv resolves the author identity on the host from the
// workspace's effective git config and returns docker -e arguments.
func gitIdentityEnv(ws string) []string {
	var envs []string
	get := func(key string) string {
		out, _ := exec.Command("git", "-C", ws, "config", "--get", key).Output()
		return strings.TrimSpace(string(out))
	}
	if n := get("user.name"); n != "" {
		envs = append(envs, "-e", "GIT_AUTHOR_NAME="+n, "-e", "GIT_COMMITTER_NAME="+n)
	}
	if e := get("user.email"); e != "" {
		envs = append(envs, "-e", "GIT_AUTHOR_EMAIL="+e, "-e", "GIT_COMMITTER_EMAIL="+e)
	}
	return envs
}
