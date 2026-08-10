package store_test

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/exequieldeferrari/axiom/internal/store"
)

func TestDefaultDirPrefersExplicitOverride(t *testing.T) {
	t.Setenv("AXIOM_DATA_DIR", filepath.Join("custom", "axiom-data"))

	got, err := store.DefaultDir()
	if err != nil {
		t.Fatalf("DefaultDir: %v", err)
	}
	if want := filepath.Join("custom", "axiom-data"); got != want {
		t.Fatalf("DefaultDir = %q, want %q", got, want)
	}
}

func TestDefaultDirFallsBackToPlatformLocation(t *testing.T) {
	t.Setenv("AXIOM_DATA_DIR", "")

	got, err := store.DefaultDir()
	if err != nil {
		t.Fatalf("DefaultDir: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("DefaultDir = %q, want an absolute path", got)
	}
	if filepath.Base(got) != "axiom" {
		t.Fatalf("DefaultDir = %q, want it to end in axiom", got)
	}
}

func TestDefaultDirUsesXDGDataHomeOnUnix(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		t.Skip("XDG data directory does not apply on this platform")
	}

	t.Setenv("AXIOM_DATA_DIR", "")
	base := t.TempDir()
	t.Setenv("XDG_DATA_HOME", base)

	got, err := store.DefaultDir()
	if err != nil {
		t.Fatalf("DefaultDir: %v", err)
	}
	if want := filepath.Join(base, "axiom"); got != want {
		t.Fatalf("DefaultDir = %q, want %q", got, want)
	}
}

// The XDG specification requires a relative XDG_DATA_HOME to be ignored.
func TestDefaultDirIgnoresRelativeXDGDataHome(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		t.Skip("XDG data directory does not apply on this platform")
	}

	t.Setenv("AXIOM_DATA_DIR", "")
	t.Setenv("XDG_DATA_HOME", "relative/path")

	got, err := store.DefaultDir()
	if err != nil {
		t.Fatalf("DefaultDir: %v", err)
	}
	if strings.Contains(got, "relative/path") {
		t.Fatalf("DefaultDir = %q, want the relative XDG_DATA_HOME to be ignored", got)
	}
}
