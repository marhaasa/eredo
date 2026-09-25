package main

import "embed"

// Everything a sandbox needs ships inside the binary: both image build
// contexts, the shipped Claude Code settings and plugin list, and the skill.
// A single file is the whole install.
//
//go:embed proxy sandbox settings.json plugins.txt relay.txt skills
var assets embed.FS
