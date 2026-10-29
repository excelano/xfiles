// Author: David M. Anderson
// Built with AI assistance (Claude, Anthropic)

package drive

import (
	"fmt"
	"net/url"
	"testing"
)

func TestItemRef(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "/root"},
		{"/", "/root"},
		{"Docs", "/root:/Docs:"},
		{"/Docs/", "/root:/Docs:"},
		{"Docs/Reports", "/root:/Docs/Reports:"},
		{"Docs/Q1 Plan.xlsx", "/root:/Docs/Q1%20Plan.xlsx:"},
		{"a/b c/d&e", "/root:/a/b%20c/d&e:"},
	}
	for _, c := range cases {
		if got := itemRef(c.in); got != c.want {
			t.Errorf("itemRef(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseSiteURL(t *testing.T) {
	cases := []struct {
		in, host, sitePath, rest string
	}{
		{"https://c.sharepoint.com/sites/Marketing", "c.sharepoint.com", "/sites/Marketing", ""},
		{"https://c.sharepoint.com/sites/Marketing/", "c.sharepoint.com", "/sites/Marketing", ""},
		{"https://c.sharepoint.com/sites/Marketing/Shared%20Documents/Reports", "c.sharepoint.com", "/sites/Marketing", "Shared Documents/Reports"},
		{"https://c.sharepoint.com/teams/Eng/Project%20Files", "c.sharepoint.com", "/teams/Eng", "Project Files"},
		{"https://c-my.sharepoint.com/personal/ann_c_com", "c-my.sharepoint.com", "/personal/ann_c_com", ""},
		{"https://c-my.sharepoint.com/personal/ann_c_com/Documents/Reports", "c-my.sharepoint.com", "/personal/ann_c_com", "Documents/Reports"},
		{"https://c-my.sharepoint.com/personal/ann_c_com/_layouts/15/onedrive.aspx?id=%2Fpersonal%2Fann_c_com%2FDocuments", "c-my.sharepoint.com", "/personal/ann_c_com", "_layouts/15/onedrive.aspx"},
		{"https://c.sharepoint.com", "c.sharepoint.com", "", ""},
		{"https://c.sharepoint.com/Shared%20Documents", "c.sharepoint.com", "", "Shared Documents"},
		// "Copy link" sharing URLs: /:type:/action/ prefix is stripped.
		{"https://c.sharepoint.com/:f:/r/sites/Marketing/Shared%20Documents/General/Phase%202", "c.sharepoint.com", "/sites/Marketing", "Shared Documents/General/Phase 2"},
		{"https://c.sharepoint.com/:w:/r/sites/Marketing", "c.sharepoint.com", "/sites/Marketing", ""},
		{"https://c.sharepoint.com/:x:/g/teams/Eng/Project%20Files", "c.sharepoint.com", "/teams/Eng", "Project Files"},
		// Full "Copy link" with trailing query cruft (?csf=&web=&e=share-token).
		{"https://c.sharepoint.com/:f:/r/sites/Marketing/Shared%20Documents/General/Phase%202?csf=1&web=1&e=cRDuU2", "c.sharepoint.com", "/sites/Marketing", "Shared Documents/General/Phase 2"},
	}
	for _, c := range cases {
		host, sitePath, rest, err := parseSiteURL(c.in)
		if err != nil {
			t.Errorf("parseSiteURL(%q) error: %v", c.in, err)
			continue
		}
		if host != c.host || sitePath != c.sitePath || rest != c.rest {
			t.Errorf("parseSiteURL(%q) = (%q, %q, %q), want (%q, %q, %q)",
				c.in, host, sitePath, rest, c.host, c.sitePath, c.rest)
		}
	}
}

func TestMatchDriveByURL(t *testing.T) {
	drives := []driveMeta{
		{ID: "d1", Name: "Documents", WebURL: "https://c.sharepoint.com/sites/Marketing/Shared%20Documents"},
		{ID: "d2", Name: "Project Files", WebURL: "https://c.sharepoint.com/sites/Marketing/Project%20Files"},
	}
	cases := []struct {
		name, in, wantID, wantStart string
		wantOK                      bool
	}{
		{"library root", "https://c.sharepoint.com/sites/Marketing/Project%20Files", "d2", "", true},
		{"folder in default lib", "https://c.sharepoint.com/sites/Marketing/Shared%20Documents/Reports", "d1", "Reports", true},
		{"nested folder", "https://c.sharepoint.com/sites/Marketing/Shared%20Documents/Reports/Q1", "d1", "Reports/Q1", true},
		{"view suffix stripped", "https://c.sharepoint.com/sites/Marketing/Shared%20Documents/Forms/AllItems.aspx", "d1", "", true},
		{"folder via id param", "https://c.sharepoint.com/sites/Marketing/Shared%20Documents/Forms/AllItems.aspx?id=%2Fsites%2FMarketing%2FShared%20Documents%2FReports%2FQ1&viewid=x", "d1", "Reports/Q1", true},
		{"longest match wins", "https://c.sharepoint.com/sites/Marketing/Project%20Files/Sub", "d2", "Sub", true},
		{"no match below site", "https://c.sharepoint.com/sites/Marketing", "", "", false},
		{"wrong host", "https://other.sharepoint.com/sites/Marketing/Shared%20Documents/Reports", "", "", false},
		{"share link prefix", "https://c.sharepoint.com/:f:/r/sites/Marketing/Shared%20Documents/Reports/Q1", "d1", "Reports/Q1", true},
	}
	for _, c := range cases {
		m, start, ok := matchDriveByURL(c.in, drives)
		if ok != c.wantOK || m.ID != c.wantID || start != c.wantStart {
			t.Errorf("%s: matchDriveByURL(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.name, c.in, m.ID, start, ok, c.wantID, c.wantStart, c.wantOK)
		}
	}
}

func TestShareLinkPrefixLen(t *testing.T) {
	cases := []struct {
		name string
		segs []string
		want int
	}{
		{"plain site", []string{"sites", "Marketing"}, 0},
		{"folder share", []string{":f:", "r", "sites", "Marketing"}, 2},
		{"word doc guest share", []string{":w:", "g", "sites", "Marketing"}, 2},
		{"type token only", []string{":f:", "sites"}, 1},
		{"empty", nil, 0},
		{"colon-led but unterminated", []string{":notashare", "x"}, 0},
	}
	for _, c := range cases {
		if got := shareLinkPrefixLen(c.segs); got != c.want {
			t.Errorf("%s: shareLinkPrefixLen(%v) = %d, want %d", c.name, c.segs, got, c.want)
		}
	}
}

func TestSplitPath(t *testing.T) {
	cases := []struct {
		in, parent, leaf string
	}{
		{"", "", ""},
		{"/", "", ""},
		{"file.txt", "", "file.txt"},
		{"/file.txt", "", "file.txt"},
		{"Docs/file.txt", "Docs", "file.txt"},
		{"Docs/Reports/q1.xlsx", "Docs/Reports", "q1.xlsx"},
		{"/Docs/Sub/", "Docs", "Sub"},
	}
	for _, c := range cases {
		parent, leaf := splitPath(c.in)
		if parent != c.parent || leaf != c.leaf {
			t.Errorf("splitPath(%q) = (%q, %q), want (%q, %q)", c.in, parent, leaf, c.parent, c.leaf)
		}
	}
}

func TestPathUnder(t *testing.T) {
	for _, c := range []struct{ parent, name, want string }{
		{"/drives/b!x/root:", "a.csv", "a.csv"},
		{"/drives/b!x/root:/Mixed Case #1 50%/inner", "a.csv", "Mixed Case #1 50%/inner/a.csv"},
	} {
		if got := pathUnder(c.parent, c.name); got != c.want {
			t.Errorf("pathUnder(%q, %q) = %q, want %q", c.parent, c.name, got, c.want)
		}
	}
}

func TestLibraryPath(t *testing.T) {
	lib, _ := url.Parse("https://c.sharepoint.com/sites/Marketing/Shared%20Documents")
	cases := []struct {
		name, in, want string
		ok             bool
	}{
		{"escaped", "https://c.sharepoint.com/sites/Marketing/Shared%20Documents/Reports/Q1%20Plan.xlsx", "Reports/Q1 Plan.xlsx", true},
		// Microsoft Search returns webUrls with literal spaces.
		{"literal spaces", "https://c.sharepoint.com/sites/Marketing/Shared Documents/Reports/Q1 Plan.xlsx", "Reports/Q1 Plan.xlsx", true},
		{"case differs", "https://C.sharepoint.com/sites/marketing/shared%20documents/a.txt", "a.txt", true},
		{"other library", "https://c.sharepoint.com/sites/Marketing/Project%20Files/a.txt", "", false},
		{"other host", "https://d.sharepoint.com/sites/Marketing/Shared%20Documents/a.txt", "", false},
		{"library itself", "https://c.sharepoint.com/sites/Marketing/Shared%20Documents", "", false},
	}
	for _, c := range cases {
		got, ok := libraryPath(lib, c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: libraryPath = (%q, %v), want (%q, %v)", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestWalkDir(t *testing.T) {
	tree := map[string][]Item{
		"":       {{Name: "b.txt"}, {Name: "A", IsFolder: true}, {Name: "c", IsFolder: true}},
		"A":      {{Name: "deep", IsFolder: true}, {Name: "z.txt"}},
		"A/deep": {{Name: "x.txt"}},
		"c":      {},
	}
	list := func(dir string) ([]Item, error) { return append([]Item(nil), tree[dir]...), nil }
	walk := func(maxDepth int, foldersOnly bool) string {
		var seen []string
		walkDir(list, "", 1, maxDepth, foldersOnly, func(it Item, p string, depth int, isLast bool) bool {
			seen = append(seen, fmt.Sprintf("%d:%s:%v", depth, p, isLast))
			return true
		})
		return fmt.Sprint(seen)
	}
	cases := []struct {
		name        string
		maxDepth    int
		foldersOnly bool
		want        string
	}{
		{"everything", 0, false, "[1:A:false 2:A/deep:false 3:A/deep/x.txt:true 2:A/z.txt:true 1:b.txt:false 1:c:true]"},
		{"depth limit", 1, false, "[1:A:false 1:b.txt:false 1:c:true]"},
		{"folders only, last folder is last", 0, true, "[1:A:false 2:A/deep:true 1:c:true]"},
	}
	for _, c := range cases {
		if got := walk(c.maxDepth, c.foldersOnly); got != c.want {
			t.Errorf("%s: visited %s, want %s", c.name, got, c.want)
		}
	}
}
