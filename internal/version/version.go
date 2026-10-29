// Package version reports the running Interlock build.
package version

import "runtime/debug"

// Version is the release version. release-please updates it on release.
var Version = "0.2.0" // x-release-please-version

// Commit returns the VCS revision the binary was built from, with a "-dirty"
// suffix for an uncommitted tree, or "" when the build carried no VCS info.
func Commit() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	var revision string
	var modified bool
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	if revision != "" && modified {
		revision += "-dirty"
	}
	return revision
}
