package main

import (
	"fmt"
	"strings"
)

// parseClaudeOpts pulls --model, --effort and --mode (or MOAT_MODEL,
// MOAT_EFFORT, MOAT_MODE) out of args and returns the matching claude flags
// plus everything else untouched.
func parseClaudeOpts(args []string) (flags, rest []string, err error) {
	model, effort, mode := env("MOAT_MODEL", ""), env("MOAT_EFFORT", ""), env("MOAT_MODE", "")
	for i := 0; i < len(args); i++ {
		a := args[i]
		key, val, hasVal := a, "", false
		if k, v, ok := strings.Cut(a, "="); ok && strings.HasPrefix(k, "--") {
			key, val, hasVal = k, v, true
		}
		switch key {
		case "--model", "--effort", "--mode":
			if !hasVal {
				if i+1 >= len(args) {
					return nil, nil, fmt.Errorf("%s needs a value", key)
				}
				i++
				val = args[i]
			}
			switch key {
			case "--model":
				model = val
			case "--effort":
				effort = val
			case "--mode":
				mode = val
			}
		default:
			rest = append(rest, a)
		}
	}
	if model != "" {
		flags = append(flags, "--model", model)
	}
	if effort != "" {
		flags = append(flags, "--effort", effort)
	}
	if mode != "" {
		flags = append(flags, "--permission-mode", mode)
	}
	return flags, rest, nil
}
