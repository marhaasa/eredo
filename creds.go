package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(raw), &c); err != nil || c.ClaudeAiOauth.AccessToken == "" {
		return "", errors.New("could not read claudeAiOauth.accessToken from the Claude Code credentials")
	}
	return c.ClaudeAiOauth.AccessToken, nil
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
