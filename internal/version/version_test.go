package version

import (
	"runtime/debug"
	"testing"
)

// installed describes what Go records for a binary it installed from a module
// at a version: the module's version, and no VCS stamp, because the module
// cache is not a checkout.
func installed(v string) *debug.BuildInfo {
	return &debug.BuildInfo{Main: debug.Module{Version: v}}
}

// built describes what Go records for a build of a working copy: a VCS stamp,
// and a pseudo-version derived from the commit.
func built(v string) *debug.BuildInfo {
	return &debug.BuildInfo{
		Main:     debug.Module{Version: v},
		Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "41352fbd9c37"}},
	}
}

func TestResolve(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		stamped string
		info    *debug.BuildInfo
		want    string
	}{
		"a stamped release wins over everything": {
			stamped: "v0.1.0", info: installed("v9.9.9"), want: "v0.1.0",
		},
		"a stamped release wins over a checkout": {
			stamped: "v0.1.0", info: built("v0.0.0-20260811231854-41352fbd9c37"), want: "v0.1.0",
		},
		"go install reports the module version": {
			stamped: "dev", info: installed("v0.1.0"), want: "v0.1.0",
		},
		"a build of a checkout is dev, not its pseudo-version": {
			stamped: "dev", info: built("v0.0.0-20260811231854-41352fbd9c37"), want: "dev",
		},
		"a build of a checkout at a tag is still dev": {
			stamped: "dev", info: built("v0.1.0"), want: "dev",
		},
		"a build outside a module is dev": {
			stamped: "dev", info: installed("(devel)"), want: "dev",
		},
		"an unversioned build is dev": {
			stamped: "dev", info: installed(""), want: "dev",
		},
		"a build with no recorded information is dev": {
			stamped: "dev", info: nil, want: "dev",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := resolve(tc.stamped, tc.info); got != tc.want {
				t.Errorf("resolve(%q) = %q, want %q", tc.stamped, got, tc.want)
			}
		})
	}
}

// The default is what a contributor sees, and the test binary is exactly that
// case: nothing stamped it, and it was built from this checkout.
func TestAnUnstampedBuildReportsDev(t *testing.T) {
	t.Parallel()

	if Version != unstamped {
		t.Fatalf("Version = %q, want %q: a release stamps this at link time and source never does", Version, unstamped)
	}
	if got := String(); got != unstamped {
		t.Errorf("String() = %q, want %q", got, unstamped)
	}
}
