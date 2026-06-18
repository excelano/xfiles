package main

import "testing"

func TestIsURL(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"https://contoso.sharepoint.com/sites/Marketing", true},
		{"HTTPS://Contoso.SharePoint.com/x", true},
		{"http://example.com", true},
		{"./report.xlsx", false},
		{"report.xlsx", false},
		{"/abs/path/file", false},
		{"C:\\Users\\file", false},
	}
	for _, c := range cases {
		if got := isURL(c.in); got != c.want {
			t.Errorf("isURL(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestClassify(t *testing.T) {
	url := "https://contoso.sharepoint.com/sites/Marketing/Shared%20Documents/Reports/Q1.xlsx"
	local := "./Q1.xlsx"
	cases := []struct {
		name, src, dst string
		want           direction
		wantErr        bool
	}{
		{"download", url, local, download, false},
		{"upload", local, url, upload, false},
		{"both urls", url, url, 0, true},
		{"neither url", local, "./copy.xlsx", 0, true},
	}
	for _, c := range cases {
		got, err := classify(c.src, c.dst)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: classify err = %v, wantErr %v", c.name, err, c.wantErr)
			continue
		}
		if err == nil && got != c.want {
			t.Errorf("%s: classify = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestUploadRemote(t *testing.T) {
	cases := []struct {
		name         string
		startPath    string
		destExists   bool
		destIsFolder bool
		localBase    string
		want         string
	}{
		{"into folder", "Reports", true, true, "Q2.xlsx", "Reports/Q2.xlsx"},
		{"into library root", "", true, true, "Q2.xlsx", "Q2.xlsx"},
		{"overwrite file", "Reports/Q1.xlsx", true, false, "Q1.xlsx", "Reports/Q1.xlsx"},
		{"new named target", "Reports/Final.xlsx", false, false, "Q2.xlsx", "Reports/Final.xlsx"},
		{"new at root", "", false, false, "Q2.xlsx", "Q2.xlsx"},
	}
	for _, c := range cases {
		if got := uploadRemote(c.startPath, c.destExists, c.destIsFolder, c.localBase); got != c.want {
			t.Errorf("%s: uploadRemote = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestDownloadLocal(t *testing.T) {
	cases := []struct {
		name       string
		dst        string
		dstIsDir   bool
		remoteBase string
		want       string
	}{
		{"into dir", "/home/u/dl", true, "Q1.xlsx", "/home/u/dl/Q1.xlsx"},
		{"to file path", "/home/u/renamed.xlsx", false, "Q1.xlsx", "/home/u/renamed.xlsx"},
		{"to bare name", "out.xlsx", false, "Q1.xlsx", "out.xlsx"},
	}
	for _, c := range cases {
		if got := downloadLocal(c.dst, c.dstIsDir, c.remoteBase); got != c.want {
			t.Errorf("%s: downloadLocal = %q, want %q", c.name, got, c.want)
		}
	}
}
