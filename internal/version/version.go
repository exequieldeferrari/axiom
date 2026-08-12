// Package version reports which axiom build is running.
package version

import "runtime/debug"

// Version is what a release build stamps in at link time:
//
//	go build -ldflags "-X github.com/exequieldeferrari/axiom/internal/version.Version=v0.1.0"
//
// Nothing writes a version down in source. A release is identified by its tag,
// so there is no constant to remember to bump and no way for the two to
// disagree.
var Version = "dev"

// unstamped is the value Version keeps when no build stamped it.
const unstamped = "dev"

// String reports the version to print.
func String() string {
	info, _ := debug.ReadBuildInfo()
	return resolve(Version, info)
}

// resolve decides which version a build reports.
//
// A stamped version is authoritative, because that is the one a release
// artifact was built with. Failing that, a binary Go installed from a module —
// `go install github.com/exequieldeferrari/axiom/cmd/axiom@v0.1.0` — already
// knows which version it is, and repeating that is better than calling a
// released binary a development build.
func resolve(stamped string, info *debug.BuildInfo) string {
	if stamped != unstamped {
		return stamped
	}
	if v, ok := moduleVersion(info); ok {
		return v
	}
	return unstamped
}

// moduleVersion reports the version Go recorded for the main module, when that
// version identifies a release rather than a working copy.
//
// A build from a checkout is ruled out by the VCS stamp Go puts on it. Go
// describes such a build with a pseudo-version derived from the commit, which
// is a true description of the source and the wrong answer here: a build
// somebody made from their own tree is "dev", and only a module Go resolved at
// a version speaks for itself.
func moduleVersion(info *debug.BuildInfo) (string, bool) {
	if info == nil {
		return "", false
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			return "", false
		}
	}
	switch info.Main.Version {
	case "", "(devel)":
		return "", false
	default:
		return info.Main.Version, true
	}
}
