package drive

import "testing"

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
