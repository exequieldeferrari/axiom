//go:build unix

package claude_test

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/event"
)

// A pipe left under an eligible name is not configuration, and opening one the
// ordinary way waits for a writer that never comes. Part of the assertion is
// that this test finishes at all: an observation that blocked here would hold
// the session start behind it until Claude Code timed the hook out, so a
// repository could stall every session by committing one entry.
//
// Only Unix can create one, which is also the only place a release is built
// for. The behavior it pins — that what is measured is the descriptor that was
// opened, not the name — is not platform-specific.
func TestAPipeUnderAnEligibleNameIsNotRead(t *testing.T) {
	root := project(t, nil)
	if err := syscall.Mkfifo(filepath.Join(root, "CLAUDE.md"), 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}

	done := make(chan *event.Harness, 1)
	go func() { done <- observeIn(root) }()

	select {
	case h := <-done:
		if s := componentStatus(t, h, "CLAUDE.md"); s != event.HarnessUnreadable {
			t.Errorf("status = %q, want %q", s, event.HarnessUnreadable)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("observing a pipe under an eligible name did not finish")
	}
}
