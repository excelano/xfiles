package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// runCLI drives run() the way a shell would, capturing what reaches stdout.
// run() reads os.Args and prints to os.Stdout directly, so both are swapped for
// the call rather than threaded through a signature the five binaries share.
func runCLI(t *testing.T, args ...string) (code int, stdout string) {
	t.Helper()
	oldArgs, oldOut := os.Args, os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Args = append([]string{"xfind"}, args...)
	os.Stdout = w
	defer func() { os.Args, os.Stdout = oldArgs, oldOut }()

	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	code = run()
	w.Close()
	return code, <-done
}

// An explicit --help is an answer, not a failure: it belongs on stdout at exit
// 0, where a caller that asked the question can read it.
func TestHelpIsASuccessOnStdout(t *testing.T) {
	for _, arg := range []string{"--help", "-h"} {
		code, out := runCLI(t, arg)
		if code != 0 {
			t.Errorf("%s: exit = %d, want 0", arg, code)
		}
		if !strings.Contains(out, "Usage: xfind") {
			t.Errorf("%s: usage did not reach stdout; got %q", arg, out)
		}
		if !strings.Contains(out, "Exit codes:") {
			t.Errorf("%s: help does not state the exit-code contract", arg)
		}
	}
}

// A version string that does not name its tool is unreadable in a log that
// mixes five of them.
func TestVersionNamesTheTool(t *testing.T) {
	code, out := runCLI(t, "--version")
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if !strings.HasPrefix(out, "xfind ") {
		t.Errorf("version = %q, want it to name the tool", out)
	}
}

func TestUnknownFlagIsABadInvocation(t *testing.T) {
	if code, _ := runCLI(t, "--no-such-flag"); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
}
