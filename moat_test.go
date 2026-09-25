package main

import (
	"reflect"
	"testing"
)

func TestDomainLines(t *testing.T) {
	good, bad := domainLines([]byte("# c\n api.anthropic.com  # api\n.github.com\n\nbad line!\nregistry.npmjs.org\n"))
	if !reflect.DeepEqual(good, []string{"api.anthropic.com", ".github.com", "registry.npmjs.org"}) {
		t.Fatalf("good = %v", good)
	}
	if !reflect.DeepEqual(bad, []string{"badline!"}) {
		t.Fatalf("bad = %v", bad)
	}
}

func TestContainerPath(t *testing.T) {
	cases := map[string]string{
		`C:\Users\me\src\app`: "/c/Users/me/src/app",
		`D:\repo`:             "/d/repo",
	}
	for in, want := range cases {
		if got := containerPathFor("windows", in); got != want {
			t.Errorf("%s -> %s, want %s", in, got, want)
		}
	}
	if got := containerPathFor("darwin", "/Users/me/src"); got != "/Users/me/src" {
		t.Errorf("unix path changed: %s", got)
	}
}

func TestParseClaudeOpts(t *testing.T) {
	flags, rest, err := parseClaudeOpts([]string{"--rw", "--model", "opus", "--effort=low", "--mode", "plan", "./x"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(flags, []string{"--model", "opus", "--effort", "low", "--permission-mode", "plan"}) {
		t.Fatalf("flags = %v", flags)
	}
	if !reflect.DeepEqual(rest, []string{"--rw", "./x"}) {
		t.Fatalf("rest = %v", rest)
	}
	if _, _, err := parseClaudeOpts([]string{"--model"}); err == nil {
		t.Fatal("expected an error for a missing value")
	}
}
