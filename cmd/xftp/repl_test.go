// Author: David M. Anderson
// Built with AI assistance (Claude, Anthropic)

package main

import (
	"reflect"
	"testing"
)

func TestResolveRemote(t *testing.T) {
	cases := []struct {
		cwd, arg, want string
	}{
		{"", "", ""},
		{"Docs", "", "Docs"},
		{"", "Docs", "Docs"},
		{"Docs", "Reports", "Docs/Reports"},
		{"Docs", "/Reports", "Reports"},
		{"Docs/Sub", "..", "Docs"},
		{"Docs/Sub", "../..", ""},
		{"Docs", ".", "Docs"},
		{"Docs", "/", ""},
		{"Docs", "Sub/../Other", "Docs/Other"},
		{"", "/a/b/c", "a/b/c"},
	}
	for _, c := range cases {
		if got := resolveRemote(c.cwd, c.arg); got != c.want {
			t.Errorf("resolveRemote(%q, %q) = %q, want %q", c.cwd, c.arg, got, c.want)
		}
	}
}

func TestTokenize(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"ls", []string{"ls"}},
		{"  ls   Docs ", []string{"ls", "Docs"}},
		{`get "Q1 Plan.xlsx"`, []string{"get", "Q1 Plan.xlsx"}},
		{`mv "a b" "c d"`, []string{"mv", "a b", "c d"}},
		{`put report.txt "Shared Docs/report.txt"`, []string{"put", "report.txt", "Shared Docs/report.txt"}},
		{`cd 'Phase 2'`, []string{"cd", "Phase 2"}},
		{`cd Phase\ 2`, []string{"cd", "Phase 2"}},
		{`cd "Phase 2"`, []string{"cd", "Phase 2"}},
		{`mv 'a b' c\ d`, []string{"mv", "a b", "c d"}},
		{`echo "a'b"`, []string{"echo", "a'b"}},
		{`echo 'a"b'`, []string{"echo", `a"b`}},
		{`cd ""`, []string{"cd", ""}},
	}
	for _, c := range cases {
		got := tokenize(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("tokenize(%q) = %#v, want %#v", c.in, got, c.want)
		}
	}
}

// A piped script learns from the exit code whether every command worked, so a
// command that only prints its error must still mark the session failed.
func TestFailedCommandSetsExitCode(t *testing.T) {
	s := &session{}
	if s.exitCode() != 0 {
		t.Fatalf("fresh session exit = %d, want 0", s.exitCode())
	}
	s.dispatch(nil, []string{"rm", "-r"})
	if s.exitCode() != 1 {
		t.Errorf("after a usage error, exit = %d, want 1", s.exitCode())
	}
}

func TestUnknownCommandFails(t *testing.T) {
	s := &session{}
	s.dispatch(nil, []string{"frobnicate"})
	if !s.failed {
		t.Error("unknown command did not mark the session failed")
	}
}
