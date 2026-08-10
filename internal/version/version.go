// Package version holds the axiom CLI version string.
// Override at link time with:
//
//	go build -ldflags "-X github.com/exequieldeferrari/axiom/internal/version.Version=v1.2.3"
package version

var Version = "dev"
