# eredo

Docker sandbox for Claude Code: a container with no root and no privileges on an
internal network, a squid sidecar that owns the domain allowlist, `.git`
masks, and Claude's token injected per attach. `README.md` is the user guide,
`ARCHITECTURE.md` the design and its comparison with Docker's own sandbox.

## Layout

- `eredo` is a Go program (`*.go`, one package). `go build -o eredo .` produces
  the command; `proxy/`, `sandbox/`, `settings.json`, `plugins.txt` and
  `skills/` are embedded into the binary, so it is the whole install and
  `eredo build` writes them to a temp dir to build the images. Windows is
  supported natively: host paths are mapped to `/c/...` inside the sandbox.
- `proxy/`: squid image, `allowlist.txt` shipped defaults.
- `sandbox/`: Claude Code image, audit hook, status line.
- `settings.json`, `plugins.txt`: shipped Claude Code defaults; users
  override them in `~/.config/eredo`.
- `skills/eredo/SKILL.md`: the skill that teaches Claude how to configure
  eredo. `.claude/skills/eredo` links to it so it is active in this repo too.

## Checks

- `go vet ./... && go test ./...` for the unit tests (allowlist parsing,
  path mapping, option parsing).
- `./eredo selftest`: starts a scratch sandbox, runs every `doctor` check,
  removes it. Needs Docker running. This is what CI runs.
- shellcheck for the remaining bash without installing it:
  `docker run --rm -v "$PWD:/mnt" -w /mnt koalaman/shellcheck:stable -x sandbox/*.sh proxy/entrypoint.sh`
- `./eredo doctor <project>` against a real sandbox after changing isolation.

## Conventions

- Shipped defaults stay generic: Anthropic, GitHub and npm allowed, no
  plugins, no model pinned, default permission mode. Personal choices belong
  in an override directory, never here.
- Any change to what the sandbox can reach or write needs a matching
  `doctor` check.
- `specVersion` in util.go is part of every sandbox's spec label; bump it
  when mounts or container env change so existing sandboxes are recreated.
- `sandbox/relay-hook.py` is Python on purpose (the image has python3);
  `relay_test.go` runs its scanner table on the host.

## Release

Bump `version` in main.go, tag `vX.Y.Z` and push the tag: the release
workflow builds binaries for five targets and attaches them. Then update `tag`
and `revision` in `Formula/eredo.rb` of marhaasa/homebrew-tools. Images carry
a hash of the embedded `proxy/` and `sandbox/` sources (`eredo.assets`
label), so a binary with changed sources rebuilds them on the next `up`.
