package cli

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/exequieldeferrari/axiom/internal/claude"
	"github.com/exequieldeferrari/axiom/internal/store"
)

// runHook dispatches the machine-facing hook entrypoint.
//
// args holds the arguments after "hook". A bad agent name is a usage error
// because it can only come from a hand-edited settings file, where surfacing
// the mistake is more useful than hiding it.
func runHook(args []string) error {
	if len(args) == 0 {
		return &UsageError{Msg: "hook requires an agent name (axiom hook claude)"}
	}
	if args[0] != "claude" {
		return &UsageError{Msg: fmt.Sprintf("unknown hook agent %q", args[0])}
	}

	dir, err := store.DefaultDir()
	if err != nil {
		return nil
	}
	return runClaudeHook(os.Stdin, dir, time.Now().UTC())
}

// runClaudeHook ingests one Claude Code hook payload.
//
// It always returns nil. Axiom is a passive observer, so an ingestion problem
// must never reach the agent: Claude Code shows a hook's stderr to the user or
// to the model on a non-zero exit, and losing an event is strictly better than
// interfering with a coding session.
//
// It takes no writer at all, because anything written to stdout during
// SessionStart is injected into Claude's context as instructions.
func runClaudeHook(stdin io.Reader, dataDir string, now time.Time) error {
	ev, err := claude.Ingest(stdin, now)
	if err != nil || ev == nil {
		return nil
	}

	s, err := store.Open(dataDir)
	if err != nil {
		return nil
	}
	_ = s.Append(*ev)
	return nil
}
