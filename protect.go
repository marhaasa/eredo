package main

import (
	"bufio"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Files in a repo that the host executes or obeys later: git hooks and
// config, hook managers that read the work tree, and the project settings
// Claude Code on the host loads. A write to any of them inside the sandbox
// would run code on the host the next time the user runs git or Claude
// there, so they are mounted read-only.
var (
	hookManagerFiles = []string{".husky", "lefthook.yml", "lefthook.yaml", ".lefthook.yml", ".lefthook.yaml", "lefthook-local.yml", ".pre-commit-config.yaml", ".pre-commit-config.yml"}
	claudeHostFiles  = []string{".claude/settings.json", ".claude/settings.local.json", ".claude/hooks", ".mcp.json"}
)

// protections is the set of extra mounts for one repo, deduplicated by target.
type protections struct {
	mounts  []string
	targets map[string]bool
	notes   []string
}

func (p *protections) tmpfs(target string) {
	if !p.targets[target] {
		p.targets[target] = true
		p.mounts = append(p.mounts, "--tmpfs", target+":ro,size=64k")
	}
}

func (p *protections) readOnly(src, target string) {
	if !p.targets[target] {
		p.targets[target] = true
		p.mounts = append(p.mounts, "--mount", "type=bind,source="+src+",target="+target+",readonly")
	}
}

func (p *protections) bind(src, target string) {
	if !p.targets[target] {
		p.targets[target] = true
		p.mounts = append(p.mounts, "--mount", "type=bind,source="+src+",target="+target)
	}
}

// under maps a host path inside src to its path inside the sandbox, or ""
// when it lies outside src.
func under(src, dst, path string) string {
	rel, err := filepath.Rel(src, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		if rel == "." {
			return dst
		}
		return ""
	}
	return dst + "/" + filepath.ToSlash(rel)
}

// repoProtections returns the mounts that keep a repo's host-executed files
// read-only. src is the host path, dst its path inside the sandbox.
func repoProtections(src, dst string) ([]string, []string) {
	p := &protections{targets: map[string]bool{}}
	gitDir, commonDir := resolveGitDirs(src)
	if gitDir != "" {
		// A linked worktree keeps its git data outside the mount; bring the
		// shared .git in at its host path so git works inside.
		inside := func(path string) string {
			if t := under(src, dst, path); t != "" {
				return t
			}
			if runtime.GOOS == "windows" {
				return ""
			}
			return containerPath(path)
		}
		if under(src, dst, commonDir) == "" {
			if runtime.GOOS == "windows" {
				p.notes = append(p.notes, "note: "+src+" is a linked worktree; git inside is not supported on Windows")
			} else {
				p.bind(commonDir, containerPath(commonDir))
			}
		}
		if t := inside(commonDir); t != "" {
			maskGitDir(p, commonDir, t)
			if gitDir != commonDir {
				if cw := filepath.Join(gitDir, "config.worktree"); isFile(cw) {
					p.readOnly(cw, inside(cw))
				}
			}
			// Submodules keep their git dirs in .git/modules, with their own
			// hooks and config.
			modules := filepath.Join(commonDir, "modules")
			n := 0
			_ = filepath.WalkDir(modules, func(path string, d fs.DirEntry, err error) error {
				if err != nil || !d.IsDir() || n > 500 {
					return nil
				}
				if isFile(filepath.Join(path, "HEAD")) && isFile(filepath.Join(path, "config")) {
					n++
					maskGitDir(p, path, inside(path))
				}
				return nil
			})
			for _, f := range gitIncludes(filepath.Join(commonDir, "config"), 3) {
				if t := under(src, dst, f); t != "" && isFile(f) {
					p.readOnly(f, t)
				}
			}
		}
		if hp := hooksPath(src); hp != "" {
			if t := under(src, dst, hp); t != "" && isDir(hp) {
				p.readOnly(hp, t)
			}
		}
	}
	for _, rel := range append(append([]string{}, hookManagerFiles...), claudeHostFiles...) {
		f := filepath.Join(src, filepath.FromSlash(rel))
		if _, err := os.Stat(f); err == nil {
			p.readOnly(f, under(src, dst, f))
		}
	}
	return p.mounts, p.notes
}

// maskGitDir hides a git dir's hooks behind an empty read-only tmpfs and
// makes its config read-only (core.hooksPath, fsmonitor, pager, filters...).
func maskGitDir(p *protections, dir, target string) {
	if target == "" {
		return
	}
	p.tmpfs(target + "/hooks")
	if cfg := filepath.Join(dir, "config"); isFile(cfg) {
		p.readOnly(cfg, target+"/config")
	}
}

// resolveGitDirs returns the repo's git dir and common dir. For a normal
// repo both are src/.git; for a linked worktree .git is a file pointing into
// the main repo's .git/worktrees/<name>, whose commondir is the main .git.
func resolveGitDirs(src string) (gitDir, commonDir string) {
	dotgit := filepath.Join(src, ".git")
	if isDir(dotgit) {
		return dotgit, dotgit
	}
	b, err := os.ReadFile(dotgit)
	if err != nil {
		return "", ""
	}
	line := strings.TrimSpace(string(b))
	rest, ok := strings.CutPrefix(line, "gitdir:")
	if !ok {
		return "", ""
	}
	gitDir = strings.TrimSpace(rest)
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(src, gitDir)
	}
	gitDir = filepath.Clean(gitDir)
	commonDir = gitDir
	if c, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		cd := strings.TrimSpace(string(c))
		if !filepath.IsAbs(cd) {
			cd = filepath.Join(gitDir, cd)
		}
		commonDir = filepath.Clean(cd)
	}
	if !isDir(gitDir) || !isDir(commonDir) {
		return "", ""
	}
	return gitDir, commonDir
}

// hooksPath resolves core.hooksPath as git would: relative to the top of the
// work tree, with ~ expanded.
func hooksPath(src string) string {
	out, err := exec.Command("git", "-C", src, "config", "--get", "core.hooksPath").Output()
	if err != nil {
		return ""
	}
	hp := strings.TrimSpace(string(out))
	if hp == "" {
		return ""
	}
	if strings.HasPrefix(hp, "~/") {
		hp = filepath.Join(home(), hp[2:])
	}
	if !filepath.IsAbs(hp) {
		hp = filepath.Join(src, hp)
	}
	return filepath.Clean(hp)
}

// gitIncludes lists files a git config includes (include.path and
// includeIf.*.path), following includes up to depth levels. Relative paths
// are relative to the including file.
func gitIncludes(cfg string, depth int) []string {
	if depth == 0 {
		return nil
	}
	out, err := exec.Command("git", "config", "--file", cfg, "--get-regexp", `^include(if\..*)?\.path$`).Output()
	if err != nil {
		return nil
	}
	var files []string
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		_, v, ok := strings.Cut(sc.Text(), " ")
		if !ok || v == "" {
			continue
		}
		if strings.HasPrefix(v, "~/") {
			v = filepath.Join(home(), v[2:])
		}
		if !filepath.IsAbs(v) {
			v = filepath.Join(filepath.Dir(cfg), v)
		}
		v = filepath.Clean(v)
		files = append(files, v)
		files = append(files, gitIncludes(v, depth-1)...)
	}
	return files
}

// missingProtected lists host-executed files that do not exist yet in the
// repo, so they cannot be mounted read-only. If one appears during a
// session it was created inside, and the user is told before trusting it.
func missingProtected(src string) []string {
	var missing []string
	for _, rel := range append(append([]string{}, hookManagerFiles...), claudeHostFiles...) {
		f := filepath.Join(src, filepath.FromSlash(rel))
		if _, err := os.Stat(f); err != nil {
			missing = append(missing, f)
		}
	}
	return missing
}

func reportCreated(missing []string) {
	for _, f := range missing {
		if _, err := os.Stat(f); err == nil {
			info("warning: %s was created inside the sandbox. It is read by git hooks or Claude Code on the host: review it before running git or claude there.", f)
		}
	}
}
