package store

import (
	"os"
	"path/filepath"
	"runtime"
)

const appDir = "axiom"

// DefaultDir reports where Axiom keeps its local data.
//
// AXIOM_DATA_DIR overrides everything. Otherwise the location follows platform
// convention: the XDG data directory on Unix, and os.UserConfigDir elsewhere.
// The Go standard library has no UserDataDir, and the cache directory is the
// wrong home for data that must not be purged.
func DefaultDir() (string, error) {
	if dir := os.Getenv("AXIOM_DATA_DIR"); dir != "" {
		return dir, nil
	}

	switch runtime.GOOS {
	case "darwin", "windows", "ios", "plan9":
		base, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(base, appDir), nil
	default:
		// The XDG spec requires relative paths to be ignored.
		if base := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(base) {
			return filepath.Join(base, appDir), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".local", "share", appDir), nil
	}
}
