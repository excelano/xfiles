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
