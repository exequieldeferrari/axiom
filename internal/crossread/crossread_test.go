package crossread_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/exequieldeferrari/axiom/internal/crossread"
	"github.com/exequieldeferrari/axiom/internal/delegation"
	"github.com/exequieldeferrari/axiom/internal/event"
)

// read is a successful whole-file read, recorded in one scope. An empty scope
// is the session scope.
func read(session, scope, path string) event.Event {
	return event.Event{
		SchemaVersion: event.SchemaVersion,
		Type:          event.TypeToolCall,
		SessionID:     session,
		SubagentID:    scope,
		Tool: &event.ToolCall{
			Name:    "Read",
			Outcome: event.OutcomeSuccess,
			Metadata: &event.ToolMetadata{
				File: &event.FileOp{Path: path, Access: event.AccessRead},
			},
		},
	}
}

// launch is a call that handed work to a nested agent, carrying the identity
// the agent returned for it. An empty returns is a launch that carried none.
func launch(session, scope, returns string) event.Event {
	ev := event.Event{
		SchemaVersion: event.SchemaVersion,
		Type:          event.TypeToolCall,
		SessionID:     session,
		SubagentID:    scope,
		Tool: &event.ToolCall{
			Name:     "Agent",
			Outcome:  event.OutcomeSuccess,
			Metadata: &event.ToolMetadata{Subagent: &event.SubagentOp{}},
		},
	}
	if returns != "" {
		ev.Tool.Result = &event.ToolResult{Subagent: &event.SubagentResult{AgentID: returns}}
	}
	return ev
}

func with(ev event.Event, change func(*event.Event)) event.Event {
	change(&ev)
	return ev
}

// report drives both sides of the seam over one log: internal/delegation
// establishes which scope handed work to which, and this package groups the
// reading against what it established.
func report(events ...event.Event) crossread.Report {
	d := delegation.New()
	a := crossread.New()
	for _, ev := range events {
		d.Add(ev)
		a.Add(ev)
	}
	return a.Report(d.Report())
}

// summarize renders a report's paths so that a case can state the relation it
// expects in one line: session, path, then each group as its launching scope
// and the members that read the path.
func summarize(r crossread.Report) []string {
	out := make([]string, 0, len(r.Paths))
	for _, p := range r.Paths {
		parts := []string{p.SessionID + " " + p.Path}
		for _, g := range p.Groups {
			members := make([]string, 0, len(g.Scopes))
			for _, s := range g.Scopes {
				members = append(members, fmt.Sprintf("%s×%d", label(s.Ref), s.Reads))
			}
			parts = append(parts, fmt.Sprintf("%s{%s}", label(g.Launcher), strings.Join(members, " ")))
		}
		out = append(out, strings.Join(parts, " "))
	}
	return out
}

func label(ref crossread.ScopeRef) string {
	if ref.Root {
		return "root"
	}
	return fmt.Sprintf("a%d", ref.Ordinal)
}

// The relation, case by case. Every case states the delegation structure in
// its events and the reading in one line of expectation.
func TestRelatedScopes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		events []event.Event
		want   []string
	}{
		{
			name: "a launching scope and the scope it launched read one path",
			events: []event.Event{
				read("s", "", "/a.go"),
				launch("s", "", "agent-a"),
				read("s", "agent-a", "/a.go"),
			},
			want: []string{"s /a.go root{root×1 a1×1}"},
		},
		{
			name: "they read different paths",
			events: []event.Event{
				read("s", "", "/a.go"),
				launch("s", "", "agent-a"),
				read("s", "agent-a", "/b.go"),
			},
			want: nil,
		},
		{
			name: "one of two paths is read in both",
			events: []event.Event{
				read("s", "", "/a.go"),
				read("s", "", "/b.go"),
				launch("s", "", "agent-a"),
				read("s", "agent-a", "/b.go"),
				read("s", "agent-a", "/c.go"),
			},
			want: []string{"s /b.go root{root×1 a1×1}"},
		},
		{
			name: "two scopes launched by one scope that never read the path",
			events: []event.Event{
				launch("s", "", "agent-a"),
				launch("s", "", "agent-b"),
				read("s", "agent-a", "/a.go"),
				read("s", "agent-b", "/a.go"),
			},
			want: []string{"s /a.go root{a1×1 a2×1}"},
		},
		{
			name: "a launching scope and two it launched",
			events: []event.Event{
				read("s", "", "/a.go"),
				launch("s", "", "agent-a"),
				launch("s", "", "agent-b"),
				read("s", "agent-a", "/a.go"),
				read("s", "agent-b", "/a.go"),
			},
			want: []string{"s /a.go root{root×1 a1×1 a2×1}"},
		},
		{
			name: "a nested agent and the agent it launched",
			events: []event.Event{
				launch("s", "", "agent-a"),
				launch("s", "agent-a", "agent-b"),
				read("s", "agent-a", "/a.go"),
				read("s", "agent-b", "/a.go"),
			},
			want: []string{"s /a.go a1{a1×1 a2×1}"},
		},
		{
			name: "one path in two groups at once",
			events: []event.Event{
				read("s", "", "/a.go"),
				launch("s", "", "agent-a"),
				launch("s", "agent-a", "agent-b"),
				read("s", "agent-a", "/a.go"),
				read("s", "agent-b", "/a.go"),
			},
			want: []string{"s /a.go root{root×1 a1×1} a1{a1×1 a2×1}"},
		},
		{
			name: "two steps apart is not related",
			events: []event.Event{
				read("s", "", "/a.go"),
				launch("s", "", "agent-a"),
				launch("s", "agent-a", "agent-b"),
				read("s", "agent-b", "/a.go"),
			},
			want: nil,
		},
		{
			name: "a scope no launch relates reads what a related scope read",
			events: []event.Event{
				read("s", "", "/a.go"),
				launch("s", "", "agent-a"),
				read("s", "agent-a", "/a.go"),
				read("s", "orphan", "/a.go"),
			},
			want: []string{"s /a.go root{root×1 a1×1}"},
		},
		{
			name: "one identity in two sessions relates neither",
			events: []event.Event{
				launch("s1", "", "agent-a"),
				read("s1", "agent-a", "/a.go"),
				read("s2", "", "/a.go"),
			},
			want: nil,
		},
		{
			name: "reading twice in one scope is one scope",
			events: []event.Event{
				read("s", "", "/a.go"),
				read("s", "", "/a.go"),
				read("s", "", "/a.go"),
				launch("s", "", "agent-a"),
			},
			want: nil,
		},
		{
			name: "a launch with no returned identity relates nothing",
			events: []event.Event{
				read("s", "", "/a.go"),
				launch("s", "", ""),
				read("s", "agent-a", "/a.go"),
			},
			want: nil,
		},
		{
			name: "an identity returned twice is one relation",
			events: []event.Event{
				launch("s", "", "agent-a"),
				launch("s", "", "agent-a"),
				read("s", "", "/a.go"),
				read("s", "agent-a", "/a.go"),
			},
			want: []string{"s /a.go root{root×1 a1×1}"},
		},
		{
			name: "a launch returning the identity that made it relates nothing",
			events: []event.Event{
				launch("s", "agent-a", "agent-a"),
				read("s", "agent-a", "/a.go"),
				read("s", "", "/a.go"),
			},
			want: nil,
		},
		{
			name: "paths are the exact strings recorded",
			events: []event.Event{
				read("s", "", "/tmp/a.go"),
				launch("s", "", "agent-a"),
				read("s", "agent-a", "/private/tmp/a.go"),
			},
			want: nil,
		},
		{
			name: "a call naming no session is dropped",
			events: []event.Event{
				read("", "", "/a.go"),
				launch("", "", "agent-a"),
				read("", "agent-a", "/a.go"),
			},
			want: nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := summarize(report(c.events...))
			if !slices.Equal(got, c.want) {
				t.Errorf("relations = %v, want %v", got, c.want)
			}
		})
	}
}

// A read that establishes no acquisition is not one. Each case pairs a
// disqualified read in a nested scope with a qualifying read of the same path
// in the scope that launched it, so a case that fails to disqualify reports a
// relation.
func TestOnlyQualifyingReadsAcquire(t *testing.T) {
	t.Parallel()

	offset, limit := 10, 20
	cases := []struct {
		name string
		call event.Event
	}{
		{"a read the agent reported failing", with(read("s", "agent-a", "/a.go"),
			func(ev *event.Event) { ev.Tool.Outcome = event.OutcomeFailure })},
		{"a read whose outcome was never established", with(read("s", "agent-a", "/a.go"),
			func(ev *event.Event) { ev.Tool.Outcome = "" })},
		{"a read from an offset", with(read("s", "agent-a", "/a.go"),
			func(ev *event.Event) { ev.Tool.Metadata.File.Offset = &offset })},
		{"a read of a limited number of lines", with(read("s", "agent-a", "/a.go"),
			func(ev *event.Event) { ev.Tool.Metadata.File.Limit = &limit })},
		{"a read naming no path", with(read("s", "agent-a", ""),
			func(ev *event.Event) { ev.Tool.Metadata.File.Path = "" })},
		{"a write", with(read("s", "agent-a", "/a.go"),
			func(ev *event.Event) { ev.Tool.Metadata.File.Access = event.AccessWrite })},
		{"an edit", with(read("s", "agent-a", "/a.go"),
			func(ev *event.Event) { ev.Tool.Metadata.File.Access = event.AccessEdit })},
		{"a search", with(read("s", "agent-a", "/a.go"), func(ev *event.Event) {
			ev.Tool.Metadata = &event.ToolMetadata{
				Search: &event.SearchOp{Kind: event.SearchContent, Root: "/a.go"},
			}
		})},
		{"a shell command", with(read("s", "agent-a", "/a.go"), func(ev *event.Event) {
			ev.Tool.Metadata = &event.ToolMetadata{Shell: &event.ShellOp{CommandDigest: "d"}}
		})},
		{"a call this version cannot describe", with(read("s", "agent-a", "/a.go"),
			func(ev *event.Event) { ev.Tool.Metadata = nil })},
		{"a record that is not a tool call", with(read("s", "agent-a", "/a.go"),
			func(ev *event.Event) { ev.Type = event.TypeSessionStart })},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := report(read("s", "", "/a.go"), launch("s", "", "agent-a"), c.call)
			if len(r.Paths) != 0 {
				t.Errorf("%s established an acquisition: %v", c.name, summarize(r))
			}
		})
	}
}

// Nested work reaches the log before the launch that names it as often as
// after, and two agents interleave. None of it may change the answer.
func TestOrderDoesNotChangeTheRelation(t *testing.T) {
	t.Parallel()

	launchA := launch("s", "", "agent-a")
	launchB := launch("s", "", "agent-b")
	rootRead := read("s", "", "/a.go")
	readA := read("s", "agent-a", "/a.go")
	readB := read("s", "agent-b", "/a.go")

	want := []string{"s /a.go root{root×1 a1×1 a2×1}"}
	orders := [][]event.Event{
		{rootRead, launchA, launchB, readA, readB},
		{readA, readB, launchA, launchB, rootRead},
		{readA, launchA, readB, rootRead, launchB},
		{launchB, readB, readA, launchA, rootRead},
	}

	for i, order := range orders {
		got := summarize(report(order...))
		if !slices.Equal(got, want) {
			t.Errorf("order %d gave %v, want %v", i, got, want)
		}
	}
}

// The numbering is assigned in the order the log first mentions an identity,
// whether it is mentioned as a scope that made a call or as one a launch
// returned.
func TestScopesAreNumberedByFirstMention(t *testing.T) {
	t.Parallel()

	// agent-b makes a call before agent-a's launch is recorded, so it is
	// numbered first.
	got := summarize(report(
		read("s", "agent-b", "/a.go"),
		launch("s", "", "agent-a"),
		launch("s", "", "agent-b"),
		read("s", "agent-a", "/a.go"),
	))

	want := []string{"s /a.go root{a1×1 a2×1}"}
	if !slices.Equal(got, want) {
		t.Errorf("relations = %v, want %v", got, want)
	}
}

// The order of the output cannot depend on how the analysis happened to
// iterate its own state.
func TestOrderingIsDeterministic(t *testing.T) {
	t.Parallel()

	events := []event.Event{
		launch("s2", "", "agent-a"),
		launch("s2", "", "agent-b"),
		read("s2", "", "/z.go"),
		read("s2", "agent-a", "/z.go"),
		read("s2", "", "/y.go"),
		read("s2", "agent-a", "/y.go"),
		read("s2", "agent-b", "/y.go"),
		launch("s1", "", "agent-a"),
		launch("s1", "agent-a", "agent-b"),
		read("s1", "", "/a.go"),
		read("s1", "agent-a", "/a.go"),
		read("s1", "agent-b", "/a.go"),
		read("s1", "", "/b.go"),
		read("s1", "agent-a", "/b.go"),
		read("s1", "", "/c.go"),
		read("s1", "agent-a", "/c.go"),
		read("s1", "agent-b", "/c.go"),
		read("s1", "", "/d.go"),
		read("s1", "agent-a", "/d.go"),
	}

	first := summarize(report(events...))
	want := []string{
		// Most groups first, then most scope memberships, then the session
		// and the path.
		"s1 /a.go root{root×1 a1×1} a1{a1×1 a2×1}",
		"s1 /c.go root{root×1 a1×1} a1{a1×1 a2×1}",
		"s2 /y.go root{root×1 a1×1 a2×1}",
		"s1 /b.go root{root×1 a1×1}",
		"s1 /d.go root{root×1 a1×1}",
		"s2 /z.go root{root×1 a1×1}",
	}
	if !slices.Equal(first, want) {
		t.Fatalf("relations = %v, want %v", first, want)
	}
	for range 20 {
		if got := summarize(report(events...)); !slices.Equal(got, first) {
			t.Fatalf("a second pass over one log gave %v, want %v", got, first)
		}
	}
}

// The counts a report carries have to hold apart what was delegated, what was
// related, and what the analysis had no relation to look at.
func TestReportCounts(t *testing.T) {
	t.Parallel()

	r := report(
		read("s", "", "/a.go"),
		launch("s", "", "agent-a"),
		launch("s", "", ""),
		launch("s", "agent-a", "agent-b"),
		read("s", "agent-a", "/a.go"),
		read("s", "agent-b", "/b.go"),
		read("s", "orphan", "/a.go"),
		read("s", "orphan", "/b.go"),
	)

	if r.Launches != 3 {
		t.Errorf("Launches = %d, want the three recorded", r.Launches)
	}
	if r.Relations != 2 {
		t.Errorf("Relations = %d, want the two a returned identity established", r.Relations)
	}
	if r.Groups != 2 {
		t.Errorf("Groups = %d, want one per launching scope", r.Groups)
	}
	if r.RelatedReads != 3 {
		t.Errorf("RelatedReads = %d, want the reads in scopes a relation holds", r.RelatedReads)
	}
	if r.UnrelatedReads != 2 {
		t.Errorf("UnrelatedReads = %d, want the reads in the scope no launch relates", r.UnrelatedReads)
	}
}

// A launching scope with no relation of its own still groups the agents it
// launched: its own parentage is not what the group is built from.
func TestAnUnrelatedScopeStillLaunches(t *testing.T) {
	t.Parallel()

	got := summarize(report(
		read("s", "orphan", "/a.go"),
		launch("s", "orphan", "agent-b"),
		read("s", "agent-b", "/a.go"),
	))

	want := []string{"s /a.go a1{a1×1 a2×1}"}
	if !slices.Equal(got, want) {
		t.Errorf("relations = %v, want %v", got, want)
	}
}

// Reporting does not consume the accumulator: more records may arrive after a
// report was taken.
func TestReportingTwiceIsValid(t *testing.T) {
	t.Parallel()

	d := delegation.New()
	a := crossread.New()
	for _, ev := range []event.Event{read("s", "", "/a.go"), launch("s", "", "agent-a")} {
		d.Add(ev)
		a.Add(ev)
	}
	if len(a.Report(d.Report()).Paths) != 0 {
		t.Fatal("a path was related before the second scope read it")
	}

	a.Add(read("s", "agent-a", "/a.go"))
	got := summarize(a.Report(d.Report()))
	want := []string{"s /a.go root{root×1 a1×1}"}
	if !slices.Equal(got, want) {
		t.Errorf("relations = %v, want %v", got, want)
	}
}

// The seam, asserted from this side: reading establishes no relation on its
// own. Two scopes reading one path, with no delegation reported, is not a
// group however the reads are arranged — this package cannot manufacture the
// relation it is given.
func TestReadingAloneEstablishesNoRelation(t *testing.T) {
	t.Parallel()

	a := crossread.New()
	for _, ev := range []event.Event{
		read("s", "", "/a.go"),
		read("s", "agent-a", "/a.go"),
		read("s", "agent-b", "/a.go"),
		// A launch record is in the log, and the relation it establishes is
		// not this package's to read out of it.
		launch("s", "", "agent-a"),
	} {
		a.Add(ev)
	}

	got := a.Report(delegation.Report{})

	if len(got.Paths) != 0 {
		t.Errorf("a relation was made from reading alone: %v", summarize(got))
	}
	if got.Launches != 0 || got.Relations != 0 || got.Groups != 0 {
		t.Errorf("delegation was counted from the event stream: %+v", got)
	}
	if got.UnrelatedReads != 3 {
		t.Errorf("UnrelatedReads = %d, want every read set aside", got.UnrelatedReads)
	}
}

// The other side of the seam: a relation this package is given is one it
// groups, whatever the log it came from looked like. Ownership of what a
// relation is sits in internal/delegation, and nothing here second-guesses it.
func TestRelationsAreUsedAsGiven(t *testing.T) {
	t.Parallel()

	a := crossread.New()
	a.Add(read("s", "agent-a", "/a.go"))
	a.Add(read("s", "agent-b", "/a.go"))

	got := a.Report(delegation.Report{
		Launches: []delegation.Launch{{SessionID: "s"}},
		Relations: []delegation.Relation{
			{SessionID: "s", Launcher: "agent-a", AgentID: "agent-b"},
		},
	})

	want := []string{"s /a.go a1{a1×1 a2×1}"}
	if !slices.Equal(summarize(got), want) {
		t.Errorf("relations = %v, want %v", summarize(got), want)
	}
	if got.Launches != 1 || got.Relations != 1 || got.Groups != 1 {
		t.Errorf("counts = %+v, want the launch and relation it was given", got)
	}
}

// A relation naming a scope the log recorded no call by is still a relation.
// It cannot reach the page without reading behind it, and it must not leave a
// scope unnumbered where it does.
func TestRelationsNamingUnobservedScopes(t *testing.T) {
	t.Parallel()

	a := crossread.New()
	a.Add(read("s", "", "/a.go"))

	got := a.Report(delegation.Report{
		Launches: []delegation.Launch{{SessionID: "s"}},
		Relations: []delegation.Relation{
			{SessionID: "s", Launcher: "", AgentID: "agent-never-seen"},
		},
	})

	if len(got.Paths) != 0 {
		t.Errorf("a scope that read nothing was reported as one that read: %v", summarize(got))
	}
	if got.Groups != 1 || got.RelatedReads != 1 {
		t.Errorf("counts = %+v, want the group and the session scope's read", got)
	}
}

// A relation in a session the log recorded no reading for is counted and
// groups nothing.
func TestRelationsInASessionWithNoReading(t *testing.T) {
	t.Parallel()

	a := crossread.New()
	a.Add(read("s1", "", "/a.go"))

	got := a.Report(delegation.Report{
		Launches: []delegation.Launch{{SessionID: "s2"}},
		Relations: []delegation.Relation{
			{SessionID: "s2", Launcher: "", AgentID: "agent-a"},
		},
	})

	if len(got.Paths) != 0 {
		t.Errorf("a session with no reading produced a relation: %v", summarize(got))
	}
	if got.Groups != 1 {
		t.Errorf("Groups = %d, want the group the relation established", got.Groups)
	}
	if got.UnrelatedReads != 1 {
		t.Errorf("UnrelatedReads = %d, want the read in the session that delegated nothing",
			got.UnrelatedReads)
	}
}
