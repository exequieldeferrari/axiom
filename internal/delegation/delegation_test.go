package delegation_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/delegation"
	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/work"
)

var time0 = time.Date(2026, 8, 12, 15, 0, 0, 0, time.UTC)

// call is one recorded tool call. Every fixture is built from this so that a
// test states only what it is about.
type call struct {
	session    string
	turn       string
	invocation string
	// subagent is the identity of the agent that MADE the call, and returns
	// the identity the call CREATED. The two are never the same field, and
	// the fixture keeps them as far apart as the model does.
	subagent string
	returns  string
	metadata *event.ToolMetadata
	outcome  event.Outcome
}

func (c call) event() event.Event {
	outcome := c.outcome
	if outcome == "" {
		outcome = event.OutcomeSuccess
	}
	ev := event.Event{
		SchemaVersion: event.SchemaVersion,
		Agent:         "claude-code",
		Type:          event.TypeToolCall,
		Timestamp:     time0,
		SessionID:     c.session,
		TurnID:        c.turn,
		SubagentID:    c.subagent,
		Tool: &event.ToolCall{
			Name:         "Tool",
			InvocationID: c.invocation,
			Outcome:      outcome,
			Metadata:     c.metadata,
		},
	}
	if c.returns != "" {
		ev.Tool.Result = &event.ToolResult{Subagent: &event.SubagentResult{AgentID: c.returns}}
	}
	return ev
}

// launch is a recorded call that handed work to a nested agent. Whether it
// recorded a returned identity is the caller's to say.
func launch(session, turn, invocation, returns string) call {
	return call{
		session: session, turn: turn, invocation: invocation, returns: returns,
		metadata: &event.ToolMetadata{Subagent: &event.SubagentOp{Type: "general-purpose"}},
	}
}

func read(session, turn, path, by string) call {
	return call{
		session: session, turn: turn, subagent: by,
		metadata: &event.ToolMetadata{File: &event.FileOp{Path: path, Access: event.AccessRead}},
	}
}

func shell(session, turn, by string) call {
	return call{
		session: session, turn: turn, subagent: by,
		metadata: &event.ToolMetadata{Shell: &event.ShellOp{CommandDigest: "d"}},
	}
}

func report(calls ...call) delegation.Report {
	a := delegation.New()
	for _, c := range calls {
		a.Add(c.event())
	}
	return a.Report()
}

// describe renders a report as one line per launch, so a test can state what
// it expects rather than walking the structure.
//
// An identity that was not recorded and an identity nothing reported are
// written differently, because reading either as the other is the mistake this
// package exists to prevent.
func describe(r delegation.Report) []string {
	out := make([]string, 0, len(r.Launches))
	for _, l := range r.Launches {
		switch {
		case l.Work == nil:
			out = append(out, fmt.Sprintf("%s/%s %s unidentified", l.SessionID, l.TurnID, l.InvocationID))
		default:
			out = append(out, strings.TrimSpace(fmt.Sprintf("%s/%s %s calls:%d turns:%d %s",
				l.SessionID, l.TurnID, l.InvocationID, l.Work.Calls, l.Work.TurnIDs,
				strings.Join(shapes(l.Work.Composition), " "))))
		}
	}
	return out
}

func shapes(c work.Composition) []string {
	var parts []string
	for _, p := range []struct {
		label string
		n     int
	}{
		{"whole", c.WholeReads},
		{"ranged", c.RangedReads},
		{"search", c.Searches},
		{"shell", c.Shell},
		{"write", c.Writes.Total()},
		{"edit", c.Edits.Total()},
		{"launch", c.Launches.Total()},
		{"uninterpreted", c.Uninterpreted},
	} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%s:%d", p.label, p.n))
		}
	}
	return parts
}

func assertLaunches(t *testing.T, r delegation.Report, want ...string) {
	t.Helper()

	if got := describe(r); !slices.Equal(got, want) {
		t.Errorf("launches:\n  got  %v\n  want %v", got, want)
	}
}

func assertUnrelated(t *testing.T, r delegation.Report, calls, agents int) {
	t.Helper()

	if r.Unrelated.Calls != calls || r.Unrelated.Agents != agents {
		t.Errorf("unrelated = %d calls across %d identities, want %d across %d",
			r.Unrelated.Calls, r.Unrelated.Agents, calls, agents)
	}
}

// The relation is an identity match, so the order the records arrive in
// changes nothing. Both orders occur: a synchronous launch is recorded after
// the calls it produced, because a hook sees a call only once it has returned,
// and an asynchronous one is recorded before them.
func TestRelationHoldsInEitherOrder(t *testing.T) {
	t.Parallel()

	cases := map[string][]call{
		"launch after its nested calls": {
			read("s1", "t1", "/repo/a.go", "agent-1"),
			shell("s1", "t1", "agent-1"),
			launch("s1", "t1", "call-1", "agent-1"),
		},
		"launch before its nested calls": {
			launch("s1", "t1", "call-1", "agent-1"),
			read("s1", "t1", "/repo/a.go", "agent-1"),
			shell("s1", "t1", "agent-1"),
		},
		"launch between its nested calls": {
			read("s1", "t1", "/repo/a.go", "agent-1"),
			launch("s1", "t1", "call-1", "agent-1"),
			shell("s1", "t1", "agent-1"),
		},
	}

	for name, calls := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := report(calls...)
			assertLaunches(t, r, "s1/t1 call-1 calls:2 turns:1 whole:1 shell:1")
			assertUnrelated(t, r, 0, 0)
		})
	}
}

// Two agents launched in one turn, with their work interleaved between the
// launches, exactly as a capture recorded it. Each launch holds its own work
// and neither holds the other's.
func TestParallelLaunchesDoNotCrossAttribute(t *testing.T) {
	t.Parallel()

	r := report(
		launch("s1", "t1", "call-1", "agent-1"),
		launch("s1", "t1", "call-2", "agent-2"),
		shell("s1", "t1", "agent-1"),
		shell("s1", "t1", "agent-2"),
		read("s1", "t1", "/repo/a.go", "agent-2"),
		read("s1", "t1", "/repo/b.go", "agent-1"),
		read("s1", "t1", "/repo/c.go", "agent-1"),
	)

	assertLaunches(t, r,
		"s1/t1 call-1 calls:3 turns:1 whole:2 shell:1",
		"s1/t1 call-2 calls:2 turns:1 whole:1 shell:1",
	)
	assertUnrelated(t, r, 0, 0)
}

// The identity is the session's. Another session reusing it is another agent,
// and relating the two would put one session's work under another's launch.
func TestIdentityDoesNotCrossSessions(t *testing.T) {
	t.Parallel()

	r := report(
		launch("s1", "t1", "call-1", "agent-1"),
		read("s1", "t1", "/repo/a.go", "agent-1"),
		read("s2", "t9", "/repo/b.go", "agent-1"),
	)

	assertLaunches(t, r, "s1/t1 call-1 calls:1 turns:1 whole:1")
	assertUnrelated(t, r, 1, 1)
}

// A launch whose identity nothing reported is not a launch with no identity.
// The first says the log holds no call reporting it; the second says Axiom has
// nothing to match on at all.
func TestLaunchWithNoMatchingCallsIsNotAnUnidentifiedLaunch(t *testing.T) {
	t.Parallel()

	r := report(
		launch("s1", "t1", "call-1", "agent-1"),
		launch("s1", "t1", "call-2", ""),
	)

	assertLaunches(t, r,
		"s1/t1 call-1 calls:0 turns:0",
		"s1/t1 call-2 unidentified",
	)
	assertUnrelated(t, r, 0, 0)
}

// A log that begins after a launch, or ends before one was recorded, holds
// nested work with nothing to relate it to. It is counted and never given to
// a launch beside it.
func TestOrphanedNestedWorkIsCountedApart(t *testing.T) {
	t.Parallel()

	r := report(
		read("s1", "t1", "/repo/a.go", "agent-gone"),
		shell("s1", "t1", "agent-gone"),
		read("s1", "t1", "/repo/b.go", "agent-other"),
		launch("s1", "t1", "call-1", "agent-1"),
		read("s1", "t1", "/repo/c.go", "agent-1"),
	)

	assertLaunches(t, r, "s1/t1 call-1 calls:1 turns:1 whole:1")
	assertUnrelated(t, r, 3, 2)
}

// Every launch recorded before the identity was persisted stays a launch, and
// nothing about it is reconstructed from what was recorded near it.
func TestHistoricalLaunchesEstablishNoRelation(t *testing.T) {
	t.Parallel()

	r := report(
		launch("s1", "t1", "call-1", ""),
		read("s1", "t1", "/repo/a.go", "agent-1"),
		shell("s1", "t1", "agent-1"),
		launch("s1", "t1", "call-2", ""),
	)

	assertLaunches(t, r,
		"s1/t1 call-1 unidentified",
		"s1/t1 call-2 unidentified",
	)
	assertUnrelated(t, r, 2, 1)
}

// The relation is the session and the identity. Nested calls that named
// another turn, or none, still belong to the launch that returned their
// identity, and the count of identifiers they named is reported beside them.
func TestRelationDoesNotDependOnTheTurn(t *testing.T) {
	t.Parallel()

	r := report(
		launch("s1", "t1", "call-1", "agent-1"),
		read("s1", "t1", "/repo/a.go", "agent-1"),
		read("s1", "t2", "/repo/b.go", "agent-1"),
		read("s1", "", "/repo/c.go", "agent-1"),
	)

	assertLaunches(t, r, "s1/t1 call-1 calls:3 turns:2 whole:3")
	assertUnrelated(t, r, 0, 0)
}

// Every shape a turn's composition distinguishes is distinguished here too,
// and every attributable call lands in exactly one category.
func TestDelegatedWorkIsComposedAndReconciles(t *testing.T) {
	t.Parallel()

	ranged := call{session: "s1", turn: "t1", subagent: "agent-1", metadata: &event.ToolMetadata{
		File: &event.FileOp{Path: "/repo/a.go", Access: event.AccessRead, Offset: intp(10)}}}
	search := call{session: "s1", turn: "t1", subagent: "agent-1", metadata: &event.ToolMetadata{
		Search: &event.SearchOp{Kind: event.SearchContent, PatternDigest: "p"}}}
	write := call{session: "s1", turn: "t1", subagent: "agent-1", outcome: event.OutcomeFailure,
		metadata: &event.ToolMetadata{File: &event.FileOp{Path: "/repo/a.go", Access: event.AccessWrite}}}
	edit := call{session: "s1", turn: "t1", subagent: "agent-1", metadata: &event.ToolMetadata{
		File: &event.FileOp{Path: "/repo/a.go", Access: event.AccessEdit}}}
	unknown := call{session: "s1", turn: "t1", subagent: "agent-1"}

	r := report(
		launch("s1", "t1", "call-1", "agent-1"),
		read("s1", "t1", "/repo/a.go", "agent-1"),
		ranged, search, shell("s1", "t1", "agent-1"), write, edit, unknown,
	)

	assertLaunches(t, r,
		"s1/t1 call-1 calls:7 turns:1 whole:1 ranged:1 search:1 shell:1 write:1 edit:1 uninterpreted:1")

	w := r.Launches[0].Work
	if got := w.Composition.Total(); got != w.Calls {
		t.Errorf("the composition accounts for %d of %d attributable calls", got, w.Calls)
	}
	// The outcome discipline the category already carries is preserved: a
	// nested agent's failed write is not a write that persisted.
	if w.Composition.Writes.Failed != 1 || w.Composition.Writes.Succeeded != 0 {
		t.Errorf("write outcomes = %+v, want the failure the record established", w.Composition.Writes)
	}
}

// A nested agent that launches another agent is both things at once: it made
// the call, and it created the agent the call returned. The generic relation
// covers it with no case of its own.
func TestNestedLaunchNeedsNoSpecialCase(t *testing.T) {
	t.Parallel()

	inner := launch("s1", "t1", "call-2", "agent-2")
	inner.subagent = "agent-1"

	r := report(
		launch("s1", "t1", "call-1", "agent-1"),
		inner,
		read("s1", "t1", "/repo/a.go", "agent-2"),
	)

	assertLaunches(t, r,
		// The outer launch's attributable work is the nested launch call
		// itself, counted as the launch it is.
		"s1/t1 call-1 calls:1 turns:1 launch:1",
		"s1/t1 call-2 calls:1 turns:1 whole:1",
	)
	assertUnrelated(t, r, 0, 0)
}

// A launch is recognized from the metadata the adapter derived, exactly as a
// turn's composition recognizes one, so no section can hold a launch another
// does not.
func TestOnlyRecognizedLaunchesAreRelated(t *testing.T) {
	t.Parallel()

	// A record with a returned identity and no launch metadata: the evidence
	// that it was a launch is what the metadata carries.
	unrecognized := call{session: "s1", turn: "t1", invocation: "call-1", returns: "agent-1"}

	r := report(
		unrecognized,
		read("s1", "t1", "/repo/a.go", "agent-1"),
	)

	assertLaunches(t, r)
	assertUnrelated(t, r, 1, 1)
}

// Records that carry no session cannot be related to anything: the identity on
// them is scoped to a session the record does not name.
func TestRecordsWithoutASessionAreNotRelated(t *testing.T) {
	t.Parallel()

	r := report(
		launch("", "t1", "call-1", "agent-1"),
		read("", "t1", "/repo/a.go", "agent-1"),
	)

	assertLaunches(t, r)
	assertUnrelated(t, r, 0, 0)
}

// Nothing but a tool call takes part.
func TestNonToolRecordsAreIgnored(t *testing.T) {
	t.Parallel()

	a := delegation.New()
	a.Add(event.Event{
		SchemaVersion: event.SchemaVersion, Type: event.TypeSessionStart,
		SessionID: "s1", SubagentID: "agent-1", Session: &event.Session{Source: "startup"},
	})
	a.Add(event.Event{
		SchemaVersion: event.SchemaVersion, Type: event.TypeToolCall, SessionID: "s1",
	})
	a.Add(launch("s1", "t1", "call-1", "agent-1").event())

	r := a.Report()
	assertLaunches(t, r, "s1/t1 call-1 calls:0 turns:0")
	assertUnrelated(t, r, 0, 0)
}

// Two passes over one log produce one answer, and taking a report does not
// consume the accumulator.
func TestReportIsDeterministicAndRepeatable(t *testing.T) {
	t.Parallel()

	calls := []call{
		launch("s1", "t1", "call-1", "agent-1"),
		launch("s1", "t1", "call-2", "agent-2"),
		read("s1", "t1", "/repo/a.go", "agent-1"),
		read("s1", "t1", "/repo/b.go", "agent-3"),
	}

	a := delegation.New()
	for _, c := range calls {
		a.Add(c.event())
	}

	first, second := a.Report(), a.Report()
	if !slices.Equal(describe(first), describe(second)) {
		t.Errorf("two reports of one accumulator differ:\n  %v\n  %v",
			describe(first), describe(second))
	}
	if first.Unrelated != second.Unrelated {
		t.Errorf("unrelated accounting differs: %+v and %+v", first.Unrelated, second.Unrelated)
	}
	if !slices.Equal(describe(first), describe(report(calls...))) {
		t.Errorf("a second pass over one log disagrees with the first")
	}

	// Adding more events after a report is valid, and the earlier report is
	// not changed by it.
	a.Add(read("s1", "t1", "/repo/c.go", "agent-2").event())
	if !slices.Equal(describe(first), describe(second)) {
		t.Errorf("an earlier report changed when more events arrived")
	}
	assertLaunches(t, a.Report(),
		"s1/t1 call-1 calls:1 turns:1 whole:1",
		"s1/t1 call-2 calls:1 turns:1 whole:1",
	)
}

// A log with no delegation in it produces an empty relation rather than an
// absent one.
func TestEmptyLog(t *testing.T) {
	t.Parallel()

	r := report(read("s1", "t1", "/repo/a.go", ""))

	assertLaunches(t, r)
	assertUnrelated(t, r, 0, 0)
	if len(r.Launches) != 0 {
		t.Errorf("launches = %+v, want none", r.Launches)
	}
}

func intp(n int) *int { return &n }
