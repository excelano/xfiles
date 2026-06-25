package main

import (
	"sort"
	"testing"
	"time"
)

func TestDiffers(t *testing.T) {
	base := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		a, b fileEntry
		want bool
	}{
		{"identical", fileEntry{size: 10, mtime: base}, fileEntry{size: 10, mtime: base}, false},
		{"size differs", fileEntry{size: 10, mtime: base}, fileEntry{size: 11, mtime: base}, true},
		{"within window", fileEntry{size: 10, mtime: base}, fileEntry{size: 10, mtime: base.Add(time.Second)}, false},
		{"window edge", fileEntry{size: 10, mtime: base}, fileEntry{size: 10, mtime: base.Add(2 * time.Second)}, false},
		{"beyond window", fileEntry{size: 10, mtime: base}, fileEntry{size: 10, mtime: base.Add(3 * time.Second)}, true},
		{"beyond window negative", fileEntry{size: 10, mtime: base}, fileEntry{size: 10, mtime: base.Add(-3 * time.Second)}, true},
	}
	for _, c := range cases {
		if got := differs(c.a, c.b); got != c.want {
			t.Errorf("%s: differs = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestRelTo(t *testing.T) {
	cases := []struct {
		root, full, want string
	}{
		{"", "a.txt", "a.txt"},
		{"", "sub/a.txt", "sub/a.txt"},
		{"Docs/Reports", "Docs/Reports/a.txt", "a.txt"},
		{"/Docs/Reports/", "Docs/Reports/sub/b.txt", "sub/b.txt"},
		{"Docs", "Docs", ""},
	}
	for _, c := range cases {
		if got := relTo(c.root, c.full); got != c.want {
			t.Errorf("relTo(%q, %q) = %q, want %q", c.root, c.full, got, c.want)
		}
	}
}

func TestHasAncestorIn(t *testing.T) {
	set := map[string]bool{"a": true, "a/b": true, "x/y/z": true}
	cases := []struct {
		rel  string
		want bool
	}{
		{"a", false},          // top-most, no ancestor in set
		{"a/b", true},         // a is in set
		{"a/b/c", true},       // a (and a/b) in set
		{"x/y/z", false},      // x and x/y not in set
		{"x/y/z/leaf", true},  // x/y/z in set
		{"standalone", false}, // unrelated
	}
	for _, c := range cases {
		if got := hasAncestorIn(c.rel, set); got != c.want {
			t.Errorf("hasAncestorIn(%q) = %v, want %v", c.rel, got, c.want)
		}
	}
}

func TestClassify(t *testing.T) {
	url := "https://contoso.sharepoint.com/sites/Marketing/Shared%20Documents/Reports"
	cases := []struct {
		name      string
		src, dst  string
		wantDir   direction
		wantError bool
	}{
		{"upload", "./reports", url, upload, false},
		{"download", url, "./reports", download, false},
		{"two urls", url, url, 0, true},
		{"two locals", "./a", "./b", 0, true},
	}
	for _, c := range cases {
		got, err := classify(c.src, c.dst)
		if c.wantError {
			if err == nil {
				t.Errorf("%s: expected error, got none", c.name)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error: %v", c.name, err)
			continue
		}
		if got != c.wantDir {
			t.Errorf("%s: direction = %v, want %v", c.name, got, c.wantDir)
		}
	}
}

func relsOf(ops []op) []string {
	out := make([]string, len(ops))
	for i, o := range ops {
		out[i] = o.rel
	}
	return out
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPlan(t *testing.T) {
	base := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	source := map[string]fileEntry{
		"keep.txt":      {rel: "keep.txt", size: 5, mtime: base},
		"changed.txt":   {rel: "changed.txt", size: 9, mtime: base},                // size differs
		"touched.txt":   {rel: "touched.txt", size: 6, mtime: base.Add(time.Hour)}, // same size, mtime moved
		"new.txt":       {rel: "new.txt", size: 3, mtime: base},
		"sub":           {rel: "sub", isDir: true},
		"sub/deep":      {rel: "sub/deep", isDir: true},
		"sub/deep/n.md": {rel: "sub/deep/n.md", size: 1, mtime: base},
		"conflict":      {rel: "conflict", size: 2, mtime: base}, // file here, dir in dest
	}
	dest := map[string]fileEntry{
		"keep.txt":    {rel: "keep.txt", size: 5, mtime: base},
		"changed.txt": {rel: "changed.txt", size: 4, mtime: base},
		"touched.txt": {rel: "touched.txt", size: 6, mtime: base},
		"conflict":    {rel: "conflict", isDir: true},
		"gone.txt":    {rel: "gone.txt", size: 7, mtime: base},
		"oldsub":      {rel: "oldsub", isDir: true},
		"oldsub/a":    {rel: "oldsub/a", size: 1, mtime: base},
		"oldsub/b":    {rel: "oldsub/b", size: 1, mtime: base},
	}

	mkdirs, copies, verify, deletes, conflicts, upToDate := plan(source, dest, true)

	if got := relsOf(mkdirs); !eqStrings(got, []string{"sub", "sub/deep"}) {
		t.Errorf("mkdirs = %v, want [sub sub/deep]", got)
	}
	if got := relsOf(copies); !eqStrings(got, []string{"changed.txt", "new.txt", "sub/deep/n.md"}) {
		t.Errorf("copies = %v, want [changed.txt new.txt sub/deep/n.md]", got)
	}
	// Same size, only the mtime moved: deferred to a content-hash check, not copied.
	if got := relsOf(verify); !eqStrings(got, []string{"touched.txt"}) {
		t.Errorf("verify = %v, want [touched.txt]", got)
	}
	// gone.txt and the oldsub subtree are missing from source. oldsub's children
	// must collapse into the single top-most oldsub delete.
	if got := relsOf(deletes); !eqStrings(got, []string{"gone.txt", "oldsub"}) {
		t.Errorf("deletes = %v, want [gone.txt oldsub]", got)
	}
	if len(conflicts) != 1 {
		t.Errorf("conflicts = %v, want exactly one", conflicts)
	}
	if upToDate != 1 {
		t.Errorf("upToDate = %d, want 1 (keep.txt)", upToDate)
	}

	// Without --delete, nothing is removed.
	_, _, _, dels, _, _ := plan(source, dest, false)
	if len(dels) != 0 {
		t.Errorf("deletes without --delete = %v, want none", relsOf(dels))
	}
}

func TestPlanMkdirOrdering(t *testing.T) {
	// Directory creations must list parents before children regardless of map
	// iteration order.
	source := map[string]fileEntry{
		"a/b/c": {rel: "a/b/c", isDir: true},
		"a":     {rel: "a", isDir: true},
		"a/b":   {rel: "a/b", isDir: true},
	}
	mkdirs, _, _, _, _, _ := plan(source, map[string]fileEntry{}, false)
	got := relsOf(mkdirs)
	if !sort.SliceIsSorted(got, func(i, j int) bool { return depth(got[i]) < depth(got[j]) }) {
		t.Errorf("mkdirs not parent-first: %v", got)
	}
	if !eqStrings(got, []string{"a", "a/b", "a/b/c"}) {
		t.Errorf("mkdirs = %v, want [a a/b a/b/c]", got)
	}
}
