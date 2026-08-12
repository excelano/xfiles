package cli

import (
	"flag"
	"strings"
	"testing"
)

// xtreeFlags mirrors xtree's flag set: one flag that takes a value, several
// that do not. The reorder rules depend on that distinction, so the test has to
// exercise it against a realistic shape rather than a single bool.
func xtreeFlags() *flag.FlagSet {
	fs := flag.NewFlagSet("xtree", flag.ContinueOnError)
	fs.String("library", "", "")
	fs.Int("L", 0, "")
	fs.Bool("d", false, "")
	v := fs.Bool("version", false, "")
	fs.BoolVar(v, "V", false, "")
	return fs
}

func TestReorderPutsFlagsFirst(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			// The defect this function exists for: the flag came after the URL
			// and Go's parser stopped reading at the URL.
			name: "a flag after the positional is still a flag",
			in:   []string{"https://site", "-L", "2"},
			want: []string{"-L", "2", "https://site"},
		},
		{
			name: "a bool flag after the positional takes no value with it",
			in:   []string{"https://site", "-d"},
			want: []string{"-d", "https://site"},
		},
		{
			name: "flags already in front are left alone",
			in:   []string{"-L", "2", "https://site"},
			want: []string{"-L", "2", "https://site"},
		},
		{
			name: "flags on both sides of the positional gather in order",
			in:   []string{"-d", "https://site", "--library", "Docs"},
			want: []string{"-d", "--library", "Docs", "https://site"},
		},
		{
			name: "an attached value is not mistaken for a positional",
			in:   []string{"https://site", "--library=Docs"},
			want: []string{"--library=Docs", "https://site"},
		},
		{
			// xcp spells stdin `-`, and two positionals must keep their order
			// or the direction of the copy inverts.
			name: "a bare dash stays a positional and operand order holds",
			in:   []string{"-", "https://site/out.csv"},
			want: []string{"-", "https://site/out.csv"},
		},
		{
			name: "operand order survives a flag written between the operands",
			in:   []string{"./local", "--library", "Docs", "https://site"},
			want: []string{"--library", "Docs", "./local", "https://site"},
		},
		{
			// Without this, a dashed filename after -- would be hoisted into
			// the flag block and parsed as a flag.
			name: "everything after a terminator is left as operands",
			in:   []string{"-d", "--", "-weird.csv", "https://site"},
			want: []string{"-d", "--", "-weird.csv", "https://site"},
		},
		{
			name: "a flag written after the terminator is an operand too",
			in:   []string{"--", "-weird.csv", "--library", "Docs"},
			want: []string{"--", "-weird.csv", "--library", "Docs"},
		},
		{
			name: "no arguments reorder to nothing",
			in:   nil,
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Reorder(tc.in, xtreeFlags())
			if strings.Join(got, "\x00") != strings.Join(tc.want, "\x00") {
				t.Errorf("Reorder(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestReorderedArgsParse is the end the reordering exists for: after the walk,
// the flag package must actually see the flag and leave the operand behind.
func TestReorderedArgsParse(t *testing.T) {
	fs := xtreeFlags()
	level := fs.Lookup("L")
	if err := fs.Parse(Reorder([]string{"https://site", "-L", "2"}, fs)); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := level.Value.String(); got != "2" {
		t.Errorf("-L = %s, want 2", got)
	}
	if got := fs.Args(); len(got) != 1 || got[0] != "https://site" {
		t.Errorf("operands = %q, want [https://site]", got)
	}
}

func TestHelpRequested(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want bool
	}{
		{"--help is a request", []string{"--help"}, true},
		{"-h is a request", []string{"-h"}, true},
		{"single-dash help is a request", []string{"-help"}, true},
		{"help after the URL is still a request", []string{"https://site", "--help"}, true},
		{"no help flag", []string{"-L", "2", "https://site"}, false},
		{"nothing at all", nil, false},
		// A caller searching for files named "--help" is not asking us for help.
		{"a value that looks like help is not a request", []string{"--library", "--help"}, false},
		{"an attached value that looks like help is not a request", []string{"--library=--help"}, false},
		{"help after a terminator is an operand", []string{"--", "--help"}, false},
		{"an attached value on help is still a request", []string{"--help=true"}, true},
		{"a bare dash is not a request", []string{"-"}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HelpRequested(tc.in, xtreeFlags()); got != tc.want {
				t.Errorf("HelpRequested(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestBoolFlagsSeparatesValueTakers(t *testing.T) {
	got := BoolFlags(xtreeFlags())
	for _, name := range []string{"d", "version", "V"} {
		if !got[name] {
			t.Errorf("%q should be a bool flag", name)
		}
	}
	for _, name := range []string{"library", "L"} {
		if got[name] {
			t.Errorf("%q takes a value and should not be a bool flag", name)
		}
	}
}
