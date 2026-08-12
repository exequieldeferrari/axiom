package turns_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/timeline"
	"github.com/exequieldeferrari/axiom/internal/turns"
)

// stream builds an ordered event sequence the way the store would replay it.
type stream struct {
	events  []event.Event
	now     time.Time
	session string
	turn    string
	nested  string
}

func newStream(session string) *stream {
	return &stream{
		now:     time.Date(2026, 8, 12, 15, 0, 0, 0, time.UTC),
		session: session,
	}
}

func (s *stream) as(session string) *stream {
	s.session = session
	return s
}

func (s *stream) inTurn(id string) *stream {
	s.turn = id
	return s
}

// inSubagent directs later events at a nested agent, and is cleared by passing
// an empty identifier.
func (s *stream) inSubagent(id string) *stream {
	s.nested = id
	return s
}

// at sets the time the next records carry, which lets a test record work out
// of chronological order without changing the order it is appended in.
func (s *stream) at(t time.Time) *stream {
	s.now = t
	return s
}

func (s *stream) add(ev event.Event) *stream {
	ev.SchemaVersion = event.SchemaVersion
	ev.Agent = "test"
	ev.Timestamp = s.now
	ev.SessionID = s.session
	ev.TurnID = s.turn
	ev.SubagentID = s.nested
	s.now = s.now.Add(time.Second)
	s.events = append(s.events, ev)
	return s
}

func (s *stream) start(source string) *stream {
	return s.add(event.Event{Type: event.TypeSessionStart, Session: &event.Session{Source: source}})
}

func (s *stream) end(reason string) *stream {
	return s.add(event.Event{Type: event.TypeSessionEnd, Session: &event.Session{Reason: reason}})
}

func (s *stream) tool(name string, outcome event.Outcome, m *event.ToolMetadata) *stream {
	return s.add(event.Event{Type: event.TypeToolCall, Tool: &event.ToolCall{
		Name: name, Outcome: outcome, Metadata: m,
	}})
}

func (s *stream) file(name string, outcome event.Outcome, f *event.FileOp) *stream {
	return s.tool(name, outcome, &event.ToolMetadata{File: f})
}

func (s *stream) read(path string) *stream {
	return s.file("Read", event.OutcomeSuccess, &event.FileOp{Path: path, Access: event.AccessRead})
}

func (s *stream) rangedRead(path string, offset int) *stream {
	return s.file("Read", event.OutcomeSuccess,
		&event.FileOp{Path: path, Access: event.AccessRead, Offset: &offset})
}

func (s *stream) write(path string, outcome event.Outcome) *stream {
	return s.file("Write", outcome, &event.FileOp{Path: path, Access: event.AccessWrite})
}

func (s *stream) edit(path string, outcome event.Outcome) *stream {
	return s.file("Edit", outcome, &event.FileOp{Path: path, Access: event.AccessEdit})
}

func (s *stream) shell(digest string) *stream {
	return s.tool("Bash", event.OutcomeSuccess, &event.ToolMetadata{
		Shell: &event.ShellOp{CommandDigest: digest},
	})
}

func (s *stream) search() *stream {
	return s.tool("Grep", event.OutcomeSuccess, &event.ToolMetadata{
		Search: &event.SearchOp{PatternDigest: "digest"},
	})
}

// spawn is the current agent's subagent call, which arrives with no metadata
// Axiom can interpret.
func (s *stream) spawn() *stream {
	return s.tool("Agent", event.OutcomeSuccess, nil)
}

// analyze replays a stream through the same single pass the CLI uses: the
// timeline places each record and the accumulator is handed the placement.
func analyze(s *stream) turns.Report {
	tl := timeline.New()
	a := turns.New()
	for _, ev := range s.events {
		a.Add(ev, tl.Add(ev))
	}
	return a.Report()
}

// shape renders a report as one line per turn, so that a test can state what
// it expects instead of walking the structure field by field. Only what was
// recorded appears, exactly as in the report itself.
func shape(r turns.Report) []string {
	out := make([]string, 0, len(r.Turns))
	for _, t := range r.Turns {
		parts := []string{
			fmt.Sprintf("%s/%s", t.SessionID, t.TurnID),
			fmt.Sprintf("#%d", t.Ordinal),
			"epoch:" + strings.Join(ordinals(t.Epochs), ","),
			fmt.Sprintf("calls:%d", t.ToolCalls),
		}
		parts = append(parts, composition(t.Composition)...)
		if t.SubagentCalls > 0 {
			parts = append(parts, fmt.Sprintf("sub:%d", t.SubagentCalls))
		}
		out = append(out, strings.Join(parts, " "))
	}
	return out
}

func composition(c turns.Composition) []string {
	var parts []string
	for _, p := range []struct {
		label string
		n     int
	}{
		{"whole", c.WholeReads},
		{"ranged", c.RangedReads},
		{"search", c.Searches},
		{"shell", c.Shell},
	} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%s:%d", p.label, p.n))
		}
	}
	if o := outcomes("write", c.Writes); o != "" {
		parts = append(parts, o)
	}
	if o := outcomes("edit", c.Edits); o != "" {
		parts = append(parts, o)
	}
	if c.Uninterpreted > 0 {
		parts = append(parts, fmt.Sprintf("uninterpreted:%d", c.Uninterpreted))
	}
	return parts
}

// outcomes renders the three states as "label:ok/failed/unestablished", which
// keeps them visibly separate: none of the three may absorb another.
func outcomes(label string, o turns.Outcomes) string {
	if o.Total() == 0 {
		return ""
	}
	return fmt.Sprintf("%s:%d/%d/%d", label, o.Succeeded, o.Failed, o.Unestablished)
}

func ordinals(epochs []int) []string {
	out := make([]string, 0, len(epochs))
	for _, e := range epochs {
		out = append(out, fmt.Sprint(e))
	}
	return out
}

func assertShape(t *testing.T, r turns.Report, want ...string) {
	t.Helper()

	if got := shape(r); !slices.Equal(got, want) {
		t.Errorf("turns:\n got %#v\nwant %#v", got, want)
	}
}

// A turn holds the work that named it, in every shape the evidence model
// distinguishes, and the categories account for every call.
func TestOneTurnOfSeveralOperations(t *testing.T) {
	t.Parallel()

	r := analyze(newStream("session-1").start("startup").inTurn("turn-1").
		read("/repo/a.go").
		read("/repo/b.go").
		rangedRead("/repo/big.log", 400).
		search().
		shell("digest-1").
		write("/repo/c.go", event.OutcomeSuccess).
		edit("/repo/a.go", event.OutcomeSuccess).
		spawn())

	assertShape(t, r, "session-1/turn-1 #1 epoch:1 calls:8 "+
		"whole:2 ranged:1 search:1 shell:1 write:1/0/0 edit:1/0/0 uninterpreted:1")

	// Every call falls in exactly one category, so the two agree. A category
	// added without being counted here would show up as a call that vanished.
	c := r.Turns[0].Composition
	total := c.WholeReads + c.RangedReads + c.Searches + c.Shell +
		c.Writes.Total() + c.Edits.Total() + c.Uninterpreted
	if total != r.Turns[0].ToolCalls {
		t.Errorf("composition totals %d of %d calls", total, r.Turns[0].ToolCalls)
	}
}

// The three outcomes are counted apart. An outcome that was never established
// is not a failure, and a failed write may still have applied in part.
func TestWriteAndEditOutcomesAreKeptApart(t *testing.T) {
	t.Parallel()

	r := analyze(newStream("session-1").start("startup").inTurn("turn-1").
		write("/repo/a.go", event.OutcomeSuccess).
		write("/repo/b.go", event.OutcomeFailure).
		write("/repo/c.go", event.Outcome("")).
		edit("/repo/a.go", event.OutcomeFailure).
		edit("/repo/b.go", event.Outcome("")))

	assertShape(t, r, "session-1/turn-1 #1 epoch:1 calls:5 write:1/1/1 edit:0/1/1")
}

// A read is counted as the operation it was. A ranged read returns part of a
// file, which is not the same acquisition as reading one.
func TestRangedReadsAreCountedApartFromWholeReads(t *testing.T) {
	t.Parallel()

	r := analyze(newStream("session-1").start("startup").inTurn("turn-1").
		read("/repo/a.go").
		rangedRead("/repo/a.go", 0))

	assertShape(t, r, "session-1/turn-1 #1 epoch:1 calls:2 whole:1 ranged:1")
}

// Turns are numbered within a session, in the order their work was recorded.
func TestSeveralTurnsInOneSession(t *testing.T) {
	t.Parallel()

	r := analyze(newStream("session-1").start("startup").
		inTurn("turn-a").read("/repo/a.go").
		inTurn("turn-b").shell("digest-1").
		inTurn("turn-c").search().
		inTurn("turn-a").read("/repo/b.go"))

	assertShape(t,
		r,
		"session-1/turn-a #1 epoch:1 calls:2 whole:2",
		"session-1/turn-b #2 epoch:1 calls:1 shell:1",
		"session-1/turn-c #3 epoch:1 calls:1 search:1",
	)
}

// A turn identifier is the agent's own and means nothing outside the session
// that issued it. Two sessions naming the same one are two turns.
func TestTurnIdentityIsScopedBySession(t *testing.T) {
	t.Parallel()

	r := analyze(newStream("session-1").start("startup").inTurn("turn-1").read("/repo/a.go").
		as("session-2").start("startup").inTurn("turn-1").shell("digest-1"))

	assertShape(t,
		r,
		"session-1/turn-1 #1 epoch:1 calls:1 whole:1",
		"session-2/turn-1 #1 epoch:1 calls:1 shell:1",
	)
}

// Compaction opens a context in the middle of a turn, so a turn's work can be
// recorded in more than one epoch. Both are named: forcing it into one would
// place work where it was not recorded.
func TestTurnSpanningSeveralEpochs(t *testing.T) {
	t.Parallel()

	r := analyze(newStream("session-1").start("startup").inTurn("turn-1").
		read("/repo/a.go").
		start("compact").
		read("/repo/b.go").
		start("compact").
		shell("digest-1"))

	assertShape(t, r, "session-1/turn-1 #1 epoch:1,2,3 calls:3 whole:2 shell:1")
}

// Several turns can do their work inside one epoch, which is the ordinary
// case, and the epoch belongs to the session rather than to any of them.
func TestSeveralTurnsInOneEpoch(t *testing.T) {
	t.Parallel()

	r := analyze(newStream("session-1").start("startup").
		inTurn("turn-a").read("/repo/a.go").
		inTurn("turn-b").read("/repo/b.go"))

	assertShape(t,
		r,
		"session-1/turn-a #1 epoch:1 calls:1 whole:1",
		"session-1/turn-b #2 epoch:1 calls:1 whole:1",
	)
}

// A turn identifier on a session start or end names no recorded work. A turn
// built from one would be a turn that did nothing, which is exactly the
// overcount this analysis exists to avoid.
func TestLifecycleIdentifiersDoNotRecordATurn(t *testing.T) {
	t.Parallel()

	r := analyze(newStream("session-1").
		inTurn("turn-start").start("startup").
		inTurn("turn-1").read("/repo/a.go").
		inTurn("turn-end").end("clear"))

	assertShape(t, r, "session-1/turn-1 #1 epoch:1 calls:1 whole:1")
}

// A call that named no turn is not assigned to the neighbouring one. It is
// counted, so work the analysis could not place does not disappear.
func TestCallWithoutATurnIsNotAssigned(t *testing.T) {
	t.Parallel()

	r := analyze(newStream("session-1").start("startup").
		inTurn("turn-1").read("/repo/a.go").
		inTurn("").read("/repo/b.go").
		inTurn("turn-1").read("/repo/c.go"))

	assertShape(t, r, "session-1/turn-1 #1 epoch:1 calls:2 whole:2")
	if r.CallsOutsideTurns != 1 {
		t.Errorf("CallsOutsideTurns = %d, want the one call that named no turn", r.CallsOutsideTurns)
	}
}

// A call that named no session has a turn identifier that identifies nothing,
// so it holds no turn either.
func TestCallWithoutASessionRecordsNoTurn(t *testing.T) {
	t.Parallel()

	r := analyze(newStream("").inTurn("turn-1").read("/repo/a.go"))

	assertShape(t, r)
	if r.CallsOutsideTurns != 1 {
		t.Errorf("CallsOutsideTurns = %d, want the unplaceable call", r.CallsOutsideTurns)
	}
}

// Membership and order come from append order. Hooks are separate processes
// and their records can carry times out of order, so a report built from
// times would order turns by which hook process happened to finish first.
func TestAppendOrderDecidesOrderingNotTime(t *testing.T) {
	t.Parallel()

	early := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	late := time.Date(2026, 8, 12, 17, 0, 0, 0, time.UTC)

	r := analyze(newStream("session-1").start("startup").
		at(late).inTurn("turn-late").read("/repo/a.go").
		at(early).inTurn("turn-early").read("/repo/b.go"))

	assertShape(t,
		r,
		"session-1/turn-late #1 epoch:1 calls:1 whole:1",
		"session-1/turn-early #2 epoch:1 calls:1 whole:1",
	)
}

// The window is widened rather than assigned, so a turn whose records arrived
// out of order still reports a window that reads forwards.
func TestWindowCoversEveryRecordedTime(t *testing.T) {
	t.Parallel()

	early := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	late := time.Date(2026, 8, 12, 17, 0, 0, 0, time.UTC)

	r := analyze(newStream("session-1").start("startup").inTurn("turn-1").
		at(late).read("/repo/a.go").
		at(early).read("/repo/b.go"))

	got := r.Turns[0]
	if !got.First.Equal(early) || !got.Last.Equal(late) {
		t.Errorf("window %s → %s, want %s → %s", got.First, got.Last, early, late)
	}
}

// A record with no time contributes none: a zero time is the absence of one,
// not the earliest moment there is.
func TestUnrecordedTimesDoNotWidenTheWindow(t *testing.T) {
	t.Parallel()

	when := time.Date(2026, 8, 12, 15, 0, 0, 0, time.UTC)

	r := analyze(newStream("session-1").start("startup").inTurn("turn-1").
		at(when).read("/repo/a.go").
		at(time.Time{}).read("/repo/b.go"))

	if got := r.Turns[0].First; !got.Equal(when) {
		t.Errorf("First = %s, want the one recorded time %s", got, when)
	}
}

// A nested agent's calls were observed carrying the turn that launched them.
// They are counted with the turn's work and counted again as nested, because a
// subagent reasons in a context of its own.
func TestNestedCallsAreCountedAsTheTurnsAndAsNested(t *testing.T) {
	t.Parallel()

	r := analyze(newStream("session-1").start("startup").inTurn("turn-1").
		spawn().
		inSubagent("agent-1").read("/repo/a.go").shell("digest-1").
		inSubagent("").read("/repo/b.go"))

	assertShape(t, r, "session-1/turn-1 #1 epoch:1 calls:4 whole:2 shell:1 uninterpreted:1 sub:2")
}

// A tool this version cannot describe is counted as such rather than dropped.
// The count is Axiom's limit, not a call that did nothing.
func TestUninterpretedCalls(t *testing.T) {
	t.Parallel()

	r := analyze(newStream("session-1").start("startup").inTurn("turn-1").
		tool("mcp__db__query", event.OutcomeSuccess, nil).
		file("Read", event.OutcomeSuccess, &event.FileOp{Access: event.AccessRead}).
		file("Chmod", event.OutcomeSuccess, &event.FileOp{Path: "/repo/a.go", Access: "chmod"}).
		// A spawn an adapter did classify still has no category of its own
		// here, which is the whole of what this version claims about one.
		tool("Agent", event.OutcomeSuccess, &event.ToolMetadata{
			Subagent: &event.SubagentOp{Type: "general-purpose"},
		}))

	assertShape(t, r, "session-1/turn-1 #1 epoch:1 calls:4 uninterpreted:4")
}

// Two runs over one log must not disagree, whatever order the maps inside
// happen to be walked in.
func TestReportIsDeterministic(t *testing.T) {
	t.Parallel()

	s := newStream("session-1").start("startup").
		inTurn("turn-a").read("/repo/a.go").
		inTurn("turn-b").read("/repo/b.go").
		as("session-2").start("startup").
		inTurn("turn-c").read("/repo/c.go").
		as("session-1").inTurn("turn-d").read("/repo/d.go")

	want := shape(analyze(s))
	for range 20 {
		if got := shape(analyze(s)); !slices.Equal(got, want) {
			t.Fatalf("two runs disagree:\n got %#v\nwant %#v", got, want)
		}
	}

	// Sessions are grouped in the order they first appeared, and turns
	// ordered within them, so a log that interleaves two sessions still reads
	// as two sessions.
	assertShape(t,
		analyze(s),
		"session-1/turn-a #1 epoch:1 calls:1 whole:1",
		"session-1/turn-b #2 epoch:1 calls:1 whole:1",
		"session-1/turn-d #3 epoch:1 calls:1 whole:1",
		"session-2/turn-c #1 epoch:1 calls:1 whole:1",
	)
}

// Reporting does not consume the accumulator, and a report is not changed by
// what is added after it.
func TestReportingTwiceContinuesTheAnalysis(t *testing.T) {
	t.Parallel()

	tl := timeline.New()
	a := turns.New()
	s := newStream("session-1").start("startup").inTurn("turn-1").read("/repo/a.go")
	for _, ev := range s.events {
		a.Add(ev, tl.Add(ev))
	}

	first := a.Report()
	more := newStream("session-1").at(s.now).inTurn("turn-1").start("compact").read("/repo/b.go")
	for _, ev := range more.events {
		a.Add(ev, tl.Add(ev))
	}

	if got := first.Turns[0]; got.ToolCalls != 1 || len(got.Epochs) != 1 {
		t.Errorf("the earlier report changed: %d calls in %d epochs", got.ToolCalls, len(got.Epochs))
	}
	assertShape(t, a.Report(), "session-1/turn-1 #1 epoch:1,2 calls:2 whole:2")
}
