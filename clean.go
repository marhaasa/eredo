package main

import (
	"fmt"
	"regexp"
	"strings"
)

// cmdClean removes one project's sandbox, proxy and network, or all of them,
// including the claude-* generation this tool grew out of. --volumes drops
// the config and history volumes too.
func cmdClean(args []string) error {
	volumes, name := false, ""
	for _, a := range args {
		switch {
		case a == "--volumes":
			volumes = true
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("unknown option: %s", a)
		default:
			name = a
		}
	}
	if name != "" {
		cleanProject(name, volumes)
		info("removed %s", name)
		return nil
	}
	ids := func(kind string, filters ...string) []string {
		var all []string
		for _, f := range filters {
			all = append(all, strings.Fields(dockerOut(kind, "ls", "-q", "--filter", f))...)
		}
		return all
	}
	for _, c := range ids("container", "label="+labelKey+"=1", "label=claude.sandbox=1", "label=devcontainer.local_folder", "name=claude-multi-") {
		dockerOK("rm", "-f", c)
	}
	for _, n := range ids("network", "label="+labelKey+"=1", "label=claude.sandbox=1") {
		dockerOK("network", "rm", n)
	}
	if volumes {
		re := regexp.MustCompile(`^(moat|claude)-.*-(config|history)$|^claude-(code-config|code-bashhistory|multi-config)-`)
		for _, v := range strings.Fields(dockerOut("volume", "ls", "-q")) {
			if re.MatchString(v) {
				dockerOK("volume", "rm", v)
			}
		}
		info("removed sandboxes, networks and volumes")
	} else {
		info("removed sandboxes and networks (volumes kept; --volumes removes them)")
	}
	return nil
}

func cleanProject(name string, volumes bool) {
	removeProject(name)
	dockerOK("rm", "-f", "claude-"+name, "claude-"+name+"-proxy")
	dockerOK("network", "rm", "claude-"+name+"-net")
	if volumes {
		for _, v := range []string{"moat-" + name + "-config", "moat-" + name + "-history", "claude-" + name + "-config", "claude-" + name + "-history"} {
			dockerOK("volume", "rm", v)
		}
	}
}
