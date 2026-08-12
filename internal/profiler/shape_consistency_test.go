package profiler_test

import (
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/timeline"
	"github.com/exequieldeferrari/axiom/internal/turns"
)

// This file holds the two remaining operation-shape classifiers to one table.
//
// The profiler reduces a call to a shape for its intervals. Everything else
// that asks the same question now shares one answer in internal/work, which
// the turns side of this table drives: the duplicate that used to sit in
// internal/turns was extracted there when a third consumer arrived, and the
// two sides of this table are the shared classifier and the profiler's own.
//
// The profiler keeps its own because its categories are not the same set: an
// interval counts operations rather than turns' compositions, and it is the
// analysis that judges the work rather than a measurement of it. Sharing the
// counting would tie one to the other; sharing the classification does not,
// and that is the part this table pins.
//
// Duplication that is deliberate still drifts, and this one did. The turns
// classifier was written without the subagent case the profiler had, so one
// report counted the same recorded call as a subagent operation in one section
// and as a call Axiom could not describe in another. The test exists so that
// the next divergence is a failure here rather than a contradiction a reader
// has to notice.
//
// Neither classifier is exported, and neither is exported for this. Each is
// driven through the public behaviour that already reaches it: a finding's
// interval on one side, a turn's composition on the other. Nothing in
// production exists for this test.

// shapes is every metadata shape the two classifiers must agree about, with the
// category each is expected to name. The expectations are written out rather
// than derived so that a change to either classifier has to be made here too.
var shapes = []struct {
	name     string
	metadata *event.ToolMetadata
	category string
}{
	{"no metadata", nil, "uninterpreted"},
	{
		"whole-file read",
		&event.ToolMetadata{File: &event.FileOp{Path: "/repo/x.go", Access: event.AccessRead}},
		"whole read",
	},
	{
		"ranged read",
		&event.ToolMetadata{File: &event.FileOp{Path: "/repo/x.go", Access: event.AccessRead, Offset: intp(10)}},
		"ranged read",
	},
	{
		"write",
		&event.ToolMetadata{File: &event.FileOp{Path: "/repo/x.go", Access: event.AccessWrite}},
		"write",
	},
	{
		"edit",
		&event.ToolMetadata{File: &event.FileOp{Path: "/repo/x.go", Access: event.AccessEdit}},
		"edit",
	},
	{
		"file operation naming no path",
		&event.ToolMetadata{File: &event.FileOp{Access: event.AccessRead}},
		"uninterpreted",
	},
	{
		"file access neither classifier knows",
		&event.ToolMetadata{File: &event.FileOp{Path: "/repo/x.go", Access: "chmod"}},
		"uninterpreted",
	},
	{
		"shell",
		&event.ToolMetadata{Shell: &event.ShellOp{CommandDigest: "other"}},
		"shell",
	},
	{
		// A background command is a barrier for repeated work and is still a
		// recorded command, which is a distinction both classifiers draw the
		// same way.
		"background shell",
		&event.ToolMetadata{Shell: &event.ShellOp{CommandDigest: "other", Background: true}},
		"shell",
	},
	{
		"search",
		&event.ToolMetadata{Search: &event.SearchOp{Kind: event.SearchContent, PatternDigest: "p"}},
		"search",
	},
	{
		"subagent launch",
		&event.ToolMetadata{Subagent: &event.SubagentOp{Type: "general-purpose"}},
		"subagent",
	},
}

func intp(n int) *int { return &n }

// time0 is the one time the turns side needs. Nothing here depends on ordering
// by time, so a single value keeps the fixture as small as the question.
var time0 = time.Date(2026, 8, 12, 15, 0, 0, 0, time.UTC)

func TestTurnsAndProfilerClassifyTheSameShapes(t *testing.T) {
	t.Parallel()

	for _, s := range shapes {
		t.Run(s.name, func(t *testing.T) {
			t.Parallel()

			if got := profilerCategory(t, s.metadata); got != s.category {
				t.Errorf("the profiler counts %s as %q, want %q", s.name, got, s.category)
			}
			if got := turnsCategory(t, s.metadata); got != s.category {
				t.Errorf("turns counts %s as %q, want %q", s.name, got, s.category)
			}
		})
	}
}

// profilerCategory reports which interval category one call of the given shape
// lands in.
//
// The call is placed between the last of two failed attempts and the later
// success of the same command, which is the only thing that produces an
// interval. The two attempts and the success are shell calls of their own and
// are outside the interval, so exactly one operation is counted in it.
func profilerCategory(t *testing.T, m *event.ToolMetadata) string {
	t.Helper()

	f := sequence(t, analyze(newStream("session-1").inTurn("turn-1").
		shell("failing-command", failing("x")).
		shell("failing-command", failing("x")).
		tool("Subject", m).
		shell("failing-command")))

	if f.Interval == nil {
		t.Fatalf("no interval was produced for %+v", m)
	}
	iv := *f.Interval
	if iv.Operations != 1 {
		t.Fatalf("the interval holds %d operations, want the one call under test", iv.Operations)
	}

	return soleCategory(t, map[string]int{
		"whole read":    iv.WholeReads,
		"ranged read":   iv.RangedReads,
		"search":        iv.Searches,
		"shell":         iv.Shell,
		"write":         iv.Writes.Total(),
		"edit":          iv.Edits.Total(),
		"subagent":      iv.Subagents,
		"uninterpreted": iv.Uninterpreted,
	})
}

// turnsCategory reports which composition category one call of the given shape
// lands in.
func turnsCategory(t *testing.T, m *event.ToolMetadata) string {
	t.Helper()

	ev := event.Event{
		SchemaVersion: event.SchemaVersion,
		Agent:         "test",
		Type:          event.TypeToolCall,
		Timestamp:     time0,
		SessionID:     "session-1",
		TurnID:        "turn-1",
		Tool:          &event.ToolCall{Name: "Subject", Outcome: event.OutcomeSuccess, Metadata: m},
	}

	tl := timeline.New()
	a := turns.New()
	a.Add(startOf("session-1"), tl.Add(startOf("session-1")))
	a.Add(ev, tl.Add(ev))

	report := a.Report()
	if len(report.Turns) != 1 || report.Turns[0].ToolCalls != 1 {
		t.Fatalf("the turn does not hold the one call under test: %+v", report.Turns)
	}
	c := report.Turns[0].Composition

	return soleCategory(t, map[string]int{
		"whole read":    c.WholeReads,
		"ranged read":   c.RangedReads,
		"search":        c.Searches,
		"shell":         c.Shell,
		"write":         c.Writes.Total(),
		"edit":          c.Edits.Total(),
		"subagent":      c.Launches.Total(),
		"uninterpreted": c.Uninterpreted,
	})
}

// only names the single category that counted the call. Both compositions
// claim their categories are mutually exclusive, so anything else is a defect
// in the classifier rather than in this test.
func soleCategory(t *testing.T, counts map[string]int) string {
	t.Helper()

	found := ""
	for name, n := range counts {
		switch {
		case n == 0:
		case found != "":
			t.Fatalf("the call was counted as both %q and %q", found, name)
		default:
			found = name
		}
	}
	if found == "" {
		t.Fatal("the call was counted in no category at all")
	}
	return found
}

func startOf(session string) event.Event {
	return event.Event{
		SchemaVersion: event.SchemaVersion,
		Agent:         "test",
		Type:          event.TypeSessionStart,
		Timestamp:     time0,
		SessionID:     session,
		Session:       &event.Session{Source: "startup"},
	}
}
