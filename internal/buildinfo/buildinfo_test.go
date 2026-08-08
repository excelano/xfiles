package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestPick(t *testing.T) {
	release := &debug.BuildInfo{} // goreleaser stamps the version; build info is irrelevant
	install := &debug.BuildInfo{Main: debug.Module{Version: "v1.6.0"}}
	devBuild := &debug.BuildInfo{
		Main: debug.Module{Version: "1.6.1-0.20260808004201-d7b9d3ff5354+dirty"},
		Settings: []debug.BuildSetting{
			{Key: "vcs", Value: "git"},
			{Key: "vcs.revision", Value: "d7b9d3ff5354"},
			{Key: "vcs.modified", Value: "true"},
		},
	}

	for _, tc := range []struct {
		name    string
		stamped string
		info    *debug.BuildInfo
		want    string
	}{
		{"release stamp wins", "1.6.0", release, "1.6.0"},
		{"release stamp beats build info", "1.6.0", install, "1.6.0"},
		{"go install falls back to the module version", Placeholder, install, "1.6.0"},
		{"local build keeps the placeholder", Placeholder, devBuild, Placeholder},
		{"no build info keeps the placeholder", Placeholder, nil, Placeholder},
		{"module reporting devel keeps the placeholder", Placeholder, &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, Placeholder},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pick(tc.stamped, tc.info); got != tc.want {
				t.Errorf("pick(%q) = %q, want %q", tc.stamped, got, tc.want)
			}
		})
	}
}
