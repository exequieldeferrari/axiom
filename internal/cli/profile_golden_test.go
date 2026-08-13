package cli

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/event"
)

// update rewrites the golden files instead of comparing against them.
//
// The golden exists to hold one output still across a refactor, so rewriting it
// has to be a deliberate act with a reason: a run with -update that nobody read
// proves the refactor changed nothing only in the sense that it agreed with
// itself.
var update = flag.Bool("update", false, "rewrite the profile golden files")

// goldenLog is a log holding one of everything the report has a section for.
//
// It is built from the event schema rather than copied from a capture: the
// sections are what is being pinned, and a recorded log would carry paths and
// identities from the machine that produced it.
func goldenLog() []event.Event {
	const session = "5d1a2b3c-4e5f-4a6b-8c9d-0e1f2a3b4c5d"

	// What one observation of a project looks like: an instruction file and
	// the local settings Axiom installed itself into, no shared settings
	// file, and one subagent definition. Both starts carry the same one,
	// which is what compaction produces: the process did not restart and
	// nothing on disk changed in between.
	observed := func() *event.Harness {
		return &event.Harness{Components: []event.HarnessComponent{
			{Kind: event.HarnessProjectInstructions, Path: "CLAUDE.md",
				Status: event.HarnessObserved,
				Digest: "3f786850e387550fdab836ed7e6dc881de23001b5a1f4f2e30e2e0d0a1f1e2c3"},
			{Kind: event.HarnessProjectSettings, Path: ".claude/settings.json",
				Status: event.HarnessAbsent},
			{Kind: event.HarnessLocalProjectSettings, Path: ".claude/settings.local.json",
				Status: event.HarnessObserved,
				Digest: "89e6c98d92887913cadf06b2adb97f26cde4849b1f1a0e1ad5b0d1c0b9a8f7e6"},
			{Kind: event.HarnessSubagentDirectory, Path: ".claude/agents",
				Status: event.HarnessObserved},
			{Kind: event.HarnessSubagentDefinition, Path: ".claude/agents/explore.md",
				Status: event.HarnessObserved,
				Digest: "b3a8e0e1f9ab1bfe3a36f231f676f78bb30a519d2b21e6c530c0eee8ebb4a5d0"},
		}}
	}
	start := func(source string, when time.Time) event.Event {
		return event.Event{
			SchemaVersion: event.SchemaVersion,
			Agent:         "claude-code",
			Type:          event.TypeSessionStart,
			Timestamp:     when,
			SessionID:     session,
			Session: &event.Session{
				Source: source, Model: "claude-opus-4", Harness: observed(),
			},
		}
	}
	call := func(turn, subagent string, when time.Time, tool event.ToolCall) event.Event {
		return event.Event{
			SchemaVersion: event.SchemaVersion,
			Agent:         "claude-code",
			Type:          event.TypeToolCall,
			Timestamp:     when,
			SessionID:     session,
			TurnID:        turn,
			SubagentID:    subagent,
			Tool:          &tool,
		}
	}
	read := func(path string) event.ToolCall {
		return event.ToolCall{
			Name: "Read", Outcome: event.OutcomeSuccess, DurationMS: ms(7),
			Metadata: &event.ToolMetadata{
				File: &event.FileOp{Path: path, Access: event.AccessRead},
			},
		}
	}
	launch := func(invocation, agent string) event.ToolCall {
		return event.ToolCall{
			Name: "Task", InvocationID: invocation, Outcome: event.OutcomeSuccess, DurationMS: ms(900),
			Metadata: &event.ToolMetadata{Subagent: &event.SubagentOp{Type: "explore"}},
			Result:   &event.ToolResult{Subagent: &event.SubagentResult{AgentID: agent}},
		}
	}

	return []event.Event{
		start("startup", at(0)),
		// Two agents launched from the session scope, each reading a file the
		// session scope also read: one group, two paths across scopes.
		call("turn-1", "", at(time.Second), launch("inv-1", "agent-a")),
		call("turn-1", "", at(2*time.Second), launch("inv-2", "agent-b")),
		call("turn-1", "agent-a", at(3*time.Second), read("/repo/calc/calc.go")),
		call("turn-1", "agent-b", at(4*time.Second), read("/repo/strutil/strutil.go")),
		call("turn-1", "", at(5*time.Second), read("/repo/calc/calc.go")),
		call("turn-1", "", at(6*time.Second), read("/repo/strutil/strutil.go")),
		// A repeated read inside one context, which the profiler reports.
		call("turn-1", "", at(7*time.Second), read("/repo/calc/calc.go")),
		call("turn-1", "", at(8*time.Second), event.ToolCall{
			Name: "Bash", Outcome: event.OutcomeSuccess, DurationMS: ms(1200),
			Metadata: &event.ToolMetadata{
				Shell: &event.ShellOp{CommandDigest: "9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a0b9c8d7e6f5a4b3c2d1e0f9a8b"},
			},
		}),
		// A call outside any turn, and one this version cannot interpret.
		call("", "", at(9*time.Second), event.ToolCall{Name: "ScheduleWakeup", Outcome: event.OutcomeSuccess}),
		// A recorded reset, then the same path read again on the other side of
		// it, which is the cross-epoch observation.
		start("compact", at(10*time.Second)),
		call("turn-2", "", at(11*time.Second), read("/repo/calc/calc.go")),
		call("turn-2", "", at(12*time.Second), event.ToolCall{
			Name: "Edit", Outcome: event.OutcomeSuccess, DurationMS: ms(30),
			Metadata: &event.ToolMetadata{
				File: &event.FileOp{Path: "/repo/calc/calc.go", Access: event.AccessEdit},
			},
		}),
		{
			SchemaVersion: event.SchemaVersion,
			Agent:         "claude-code",
			Type:          event.TypeSessionEnd,
			Timestamp:     at(13 * time.Second),
			SessionID:     session,
			TurnID:        "turn-2",
			Session:       &event.Session{Reason: "other"},
		},
	}
}

// TestProfileOutputIsUnchanged pins the whole report, byte for byte.
//
// It exists for one purpose: a change to how the report is assembled must not
// change what it says. Every other profile test asserts on a phrase, so a
// refactor could move a blank line, drop a caveat or reorder two sections
// without any of them noticing.
func TestProfileOutputIsUnchanged(t *testing.T) {
	cases := []struct {
		name   string
		golden string
		opts   profileOptions
	}{
		{name: "whole log", golden: "profile.txt"},
		{name: "one session", golden: "profile_session.txt",
			opts: profileOptions{session: "5d1a2b3c-4e5f-4a6b-8c9d-0e1f2a3b4c5d"}},
		{name: "no such session", golden: "profile_unknown_session.txt",
			opts: profileOptions{session: "no-such-session"}},
	}

	dir := seed(t, goldenLog()...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scopedProfileOutput(t, dir, tc.opts)
			compareGolden(t, tc.golden, got)
		})
	}
}

func compareGolden(t *testing.T, name, got string) {
	t.Helper()

	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("create testdata: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v (run go test -run TestProfileOutputIsUnchanged -update)", err)
	}
	if got != string(want) {
		t.Errorf("profile output changed.\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
}
