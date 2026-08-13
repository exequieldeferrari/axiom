package project_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/exequieldeferrari/axiom/internal/project"
)

// The home directory being a repository is one of Claude Code's own exceptions
// to root resolution, alongside directories outside any repository.
func TestRootKeepsTheStartingDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := os.Mkdir(filepath.Join(home, ".git"), 0o755); err != nil {
		t.Fatalf("create .git: %v", err)
	}
	sub := filepath.Join(home, "scratch")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("create subdirectory: %v", err)
	}

	if got := project.Root(sub); got != sub {
		t.Errorf("Root = %s, want the starting directory %s", got, sub)
	}
}

func TestRootFindsTheRepositoryAbove(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("create .git: %v", err)
	}
	sub := filepath.Join(root, "internal", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("create subdirectory: %v", err)
	}

	if got := project.Root(sub); got != root {
		t.Errorf("Root = %s, want the repository root %s", got, root)
	}
}

// A directory in no repository is its own root. Claude Code still reads a
// CLAUDE.md sitting beside it, so resolving to a parent would name a project
// the agent was never started in.
func TestRootOutsideARepositoryIsTheDirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dir := t.TempDir()
	if got := project.Root(dir); got != dir {
		t.Errorf("Root = %s, want %s", got, dir)
	}
}
