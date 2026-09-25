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
- `eredo-docker` is still bash and sources `lib.sh`; it is the Docker
  Sandboxes comparison twin, not the product.
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
  `docker run --rm -v "$PWD:/mnt" -w /mnt koalaman/shellcheck:stable -x eredo-docker lib.sh sandbox/*.sh proxy/entrypoint.sh`
- `./eredo doctor <project>` against a real sandbox after changing isolation.

## Conventions

- Shipped defaults stay generic: Anthropic, GitHub and npm allowed, no
  plugins, no model pinned, default permission mode. Personal choices belong
  in an override directory, never here.
- Any change to what the sandbox can reach or write needs a matching
  `doctor` check.
- `spec` in `cmd_up` is versioned; bump it when mounts or container env
  change so existing sandboxes are recreated.

## Release

Tag `vX.Y.Z`, push the tag, then update `url`/`tag`/`revision` in
`Formula/eredo.rb` of marhaasa/homebrew-tools. The formula installs the tree
into `libexec` and symlinks `bin/eredo`, which the symlink resolver at the top
of `eredo` follows to find `lib.sh`.
