// Package project resolves the project a working directory belongs to.
//
// Claude Code reads a project's configuration relative to a root it derives
// from where it was started. Axiom has to derive the same root in two places —
// when it installs hooks, and when it observes what configuration was there —
// and one rule serves both. Two subtly different rules would install into a
// file Claude Code never reads, or record a file it never loaded.
package project

import (
	"os"
	"path/filepath"
)

// Root finds the git repository root containing dir.
//
// Claude Code reads .claude/settings.local.json at the repository root, so
// installing into a subdirectory would write a file it never reads. The
// starting directory is kept outside a repository and when the repository root
// is the home directory, matching Claude Code's own exceptions. A linked
// worktree resolves to the worktree rather than to the main checkout, where
// Claude Code would look.
//
// A directory in no repository is its own root, which is also what Claude Code
// does with one: it still reads a CLAUDE.md sitting beside it.
func Root(dir string) string {
	home, _ := os.UserHomeDir()
	for d := dir; d != home; {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	return dir
}
