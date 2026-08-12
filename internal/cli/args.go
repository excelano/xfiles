// Package cli holds the argument-handling conventions the five binaries share.
//
// Each of xtree, xfind, xcp, xsync, and xftp builds its own flag.FlagSet, but
// they answer a caller the same way: an explicit --help is a success printed on
// stdout, a flag is honoured wherever it appears on the line, and the exit code
// says whether the fault was in the command or in the data. Keeping that here
// means the five agree by construction rather than by five people remembering.
package cli

import (
	"flag"
	"strings"
)

// ExitCodes is the contract every usage block in this repo ends with. A caller
// branching on the number needs to know which side of the line a failed sign-in
// falls on: the command was well-formed, so it is 1, not 2.
const ExitCodes = `Exit codes:
  0  success
  1  bad input — an unreachable site, a missing file, a sign-in that could not
     be completed, a transfer that failed partway
  2  bad invocation — unknown flag, missing argument, contradictory options`

// BoolFlags reports which of fs's flags take no value, so an argument walk can
// tell `--library Docs` (flag plus value) from `--dry-run ./out` (flag, then a
// positional).
func BoolFlags(fs *flag.FlagSet) map[string]bool {
	out := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) {
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
			out[f.Name] = true
		}
	})
	return out
}

// Reorder moves flags ahead of positional arguments so a flag written after the
// URL is still read as a flag. Go's flag package stops parsing at the first
// non-flag argument, which made `xtree <url> -L 2` fail with "exactly one
// SharePoint URL is required" — a complaint about the one thing the caller had
// got right, since -L and 2 had become the second and third URLs.
//
// Everything after a `--` terminator is passed through untouched, which is how
// a local file whose name begins with a dash is spelled.
func Reorder(args []string, fs *flag.FlagSet) []string {
	isBool := BoolFlags(fs)

	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			// The terminator travels with the operands it protects, so Parse
			// still meets it before them.
			positional = append(positional, args[i:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.ContainsRune(name, '=') {
			continue // --flag=value carries its own value
		}
		if !isBool[name] && i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}
	return append(flags, positional...)
}

// HelpRequested reports whether args asks for help in flag position. Go's flag
// package treats -h as a parse error and prints usage to stderr; catching the
// request first puts it on stdout at exit 0, where a caller that asked for help
// can read it as the answer it is. Walking the arguments rather than scanning
// for the string means `--name "--help"` is read as the glob it is.
func HelpRequested(args []string, fs *flag.FlagSet) bool {
	isBool := BoolFlags(fs)
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return false
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			continue
		}
		name := strings.TrimLeft(a, "-")
		if name == "h" || name == "help" {
			return true
		}
		if strings.ContainsRune(name, '=') {
			continue
		}
		if !isBool[name] && i+1 < len(args) {
			i++ // skip the flag's value so it is never mistaken for a flag
		}
	}
	return false
}
