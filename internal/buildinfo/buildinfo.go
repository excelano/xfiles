// Package buildinfo resolves the version string an xfiles binary reports.
//
// Releases stamp the version in through -ldflags at build time. A binary
// produced by `go install github.com/excelano/xfiles/cmd/<tool>@latest` never
// runs goreleaser, so that stamp is absent and the placeholder would be
// reported instead of the release number. Go records the resolved module
// version in the embedded build info for those installs, so fall back to it.
package buildinfo

import (
	"runtime/debug"
	"strings"
)

// Placeholder is what a tool's version variable holds when -ldflags did not
// set it.
const Placeholder = "(devel)"

// Resolve returns the version to report, given the -ldflags stamp.
func Resolve(stamped string) string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return stamped
	}
	return pick(stamped, info)
}

// pick decides what to report from the stamp and the embedded build info. It is
// separate from Resolve so the three cases can be tested without producing a
// real release, install, and development binary.
func pick(stamped string, info *debug.BuildInfo) string {
	if stamped != "" && stamped != Placeholder {
		return stamped // a release: the stamp always wins
	}
	if info == nil {
		return stamped
	}
	// A build from a local checkout records vcs.* settings, and Go synthesizes
	// a pseudo-version from them (1.6.1-0.20260808004201-d7b9d3ff5354+dirty).
	// That is a development build; reporting it would read like a release that
	// does not exist, so keep the placeholder. Only a module fetched by version
	// — what `go install ...@latest` does — has a real version and no vcs
	// settings.
	for _, s := range info.Settings {
		if strings.HasPrefix(s.Key, "vcs.") {
			return stamped
		}
	}
	v := info.Main.Version
	if v == "" || v == "(devel)" {
		return stamped
	}
	return strings.TrimPrefix(v, "v") // module versions carry a leading v
}
