package profiler_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/profiler"
)

const defaultDurationMS int64 = 5

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
		now:     time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC),
		session: session,
	}
}

// as directs later events at a different session.
func (s *stream) as(session string) *stream {
	s.session = session
	return s
}

// inTurn directs later events at a different turn of the same session.
func (s *stream) inTurn(id string) *stream {
	s.turn = id
	return s
}

// inSubagent directs later events at a nested agent within the same session.
func (s *stream) inSubagent(id string) *stream {
	s.nested = id
	return s
}

func (s *stream) add(ev event.Event) *stream {
	s.now = s.now.Add(time.Second)
	ev.SchemaVersion = event.SchemaVersion
	ev.Agent = "test"
	ev.Timestamp = s.now
	ev.SessionID = s.session
	ev.TurnID = s.turn
	ev.SubagentID = s.nested
	s.events = append(s.events, ev)
	return s
}

type option func(*event.ToolCall)

// failed marks a call as failed with no detail at all, which is what an agent
// that reports only an outcome produces.
func failed(t *event.ToolCall)  { t.Outcome = event.OutcomeFailure }
func untimed(t *event.ToolCall) { t.DurationMS = nil }

// unestablished gives a call an outcome that says nothing about what became of
// it: nothing validates the field on the way into or out of the log, so a record
// can carry no outcome or one a later model added.
func unestablished(state string) option {
	return func(t *event.ToolCall) { t.Outcome = event.Outcome(state) }
}

// failing marks a call as failed with the failure the agent reported for it,
// carrying no classification of that report. It is the shape of every record
// written before adapters classified one, and stands for them in tests.
func failing(digest string) option {
	return func(t *event.ToolCall) {
		t.Outcome = event.OutcomeFailure
		t.Failure = &event.Failure{Kind: event.FailureKindError, Digest: digest}
	}
}

// reported marks a call as failed with a report the adapter classified, which
// is what a current adapter records. The digest stands for the report's exact
// text: two calls given the same one reported the same string.
func reported(digest string, as event.Reporting) option {
	return func(t *event.ToolCall) {
		t.Outcome = event.OutcomeFailure
		t.Failure = &event.Failure{
			Kind:      event.FailureKindError,
			Digest:    digest,
			Reporting: as,
		}
	}
}

// The three classified shapes, named as the captures they came from: a report
// carrying more than the status, a report that was the status alone, and a
// report the adapter could not place.
func withDetail(digest string) option { return reported(digest, event.ReportingDetail) }
func statusOnly(digest string) option { return reported(digest, event.ReportingStatusOnly) }
func unreadable(digest string) option { return reported(digest, event.ReportingUnrecognized) }

// untexted marks a call as failed with no report at all, which leaves nothing
// to digest and nothing to compare against another attempt.
func untexted(t *event.ToolCall) {
	t.Outcome = event.OutcomeFailure
	t.Failure = &event.Failure{Kind: event.FailureKindError, Reporting: event.ReportingNoText}
}

// exiting adds the status a failed call exited with.
func exiting(code int) option {
	return func(t *event.ToolCall) {
		if t.Failure == nil {
			t.Failure = &event.Failure{Kind: event.FailureKindError}
		}
		t.Failure.ExitCode = &code
	}
}

// interrupted marks a call a person stopped part way through.
func interrupted(t *event.ToolCall) {
	t.Outcome = event.OutcomeFailure
	t.Failure = &event.Failure{Kind: event.FailureKindInterrupt}
}

func took(ms int64) option {
	return func(t *event.ToolCall) { t.DurationMS = &ms }
}

// invocation gives a call the identifier the agent used for it.
func invocation(id string) option {
	return func(t *event.ToolCall) { t.InvocationID = id }
}

func (s *stream) tool(name string, md *event.ToolMetadata, opts ...option) *stream {
	d := defaultDurationMS
	call := &event.ToolCall{
		Name:       name,
		Outcome:    event.OutcomeSuccess,
		DurationMS: &d,
		Metadata:   md,
	}
	for _, opt := range opts {
		opt(call)
	}
	return s.add(event.Event{Type: event.TypeToolCall, Tool: call})
}

func (s *stream) read(path string, opts ...option) *stream {
	return s.tool("Read", &event.ToolMetadata{
		File: &event.FileOp{Path: path, Access: event.AccessRead},
	}, opts...)
}

func (s *stream) readRange(path string, offset int, opts ...option) *stream {
	return s.tool("Read", &event.ToolMetadata{
		File: &event.FileOp{Path: path, Access: event.AccessRead, Offset: &offset},
	}, opts...)
}

func (s *stream) edit(path string, opts ...option) *stream {
	return s.tool("Edit", &event.ToolMetadata{
		File: &event.FileOp{Path: path, Access: event.AccessEdit},
	}, opts...)
}

func (s *stream) write(path string, opts ...option) *stream {
	return s.tool("Write", &event.ToolMetadata{
		File: &event.FileOp{Path: path, Access: event.AccessWrite},
	}, opts...)
}

func (s *stream) shell(digest string, opts ...option) *stream {
	return s.tool("Bash", &event.ToolMetadata{
		Shell: &event.ShellOp{CommandDigest: digest},
	}, opts...)
}

func (s *stream) background(digest string, opts ...option) *stream {
	return s.tool("Bash", &event.ToolMetadata{
		Shell: &event.ShellOp{CommandDigest: digest, Background: true},
	}, opts...)
}

func (s *stream) search(digest string, opts ...option) *stream {
	return s.tool("Grep", &event.ToolMetadata{
		Search: &event.SearchOp{Kind: event.SearchContent, PatternDigest: digest},
	}, opts...)
}

func (s *stream) subagent(kind string, opts ...option) *stream {
	return s.tool("Task", &event.ToolMetadata{
		Subagent: &event.SubagentOp{Type: kind},
	}, opts...)
}

// unrecognised is a tool the metadata allowlist does not cover, such as an MCP
// tool or NotebookEdit, so Axiom cannot know what it touched.
func (s *stream) unrecognised(name string, opts ...option) *stream {
	return s.tool(name, nil, opts...)
}

func (s *stream) sessionStart(source string) *stream {
	return s.add(event.Event{
		Type:    event.TypeSessionStart,
		Session: &event.Session{Source: source},
	})
}

func (s *stream) sessionEnd() *stream {
	return s.add(event.Event{
		Type:    event.TypeSessionEnd,
		Session: &event.Session{Reason: "exit"},
	})
}

// identity renders a finding for comparison. ObservedTotal is a pointer, so
// two equal findings are not ==.
func identity(f profiler.Finding) string {
	total := "unknown"
	if f.ObservedTotal != nil {
		total = f.ObservedTotal.String()
	}
	return fmt.Sprintf("%s|%s|%d|%d|%s|%s|%s|%s%s",
		f.Kind, f.SessionID, f.Occurrences, f.Redundant,
		f.First, f.Last, total, f.Path, f.CommandDigest)
}

func analyze(s *stream) profiler.Report {
	p := profiler.New()
	for _, ev := range s.events {
		p.Add(ev)
	}
	return p.Report()
}

// Each of these repeats an operation, and each has a reason the repetition
// could be legitimate. Reporting any of them would be a false positive.
func TestNoFindings(t *testing.T) {
	t.Parallel()

	cases := map[string]*stream{
		"same command in a later session": newStream("a").
			shell("go-test").as("b").shell("go-test"),

		"command re-run after an edit": newStream("a").
			shell("go-test").edit("/src/main.go").shell("go-test"),

		"command re-run after a failed edit": newStream("a").
			shell("go-test").edit("/src/main.go", failed).shell("go-test"),

		"command re-run after another command": newStream("a").
			shell("git-status").shell("git-add").shell("git-status"),

		"command retried after failing": newStream("a").
			shell("go-build", failed).shell("go-build"),

		"command re-run after an unrecognised tool": newStream("a").
			shell("go-test").unrecognised("NotebookEdit").shell("go-test"),

		"background command repeated": newStream("a").
			background("serve").background("serve"),

		"same file read in a later session": newStream("a").
			read("/src/main.go").as("b").read("/src/main.go"),

		"file re-read after an edit": newStream("a").
			read("/src/main.go").edit("/src/main.go").read("/src/main.go"),

		"file re-read after a write": newStream("a").
			read("/src/main.go").write("/src/main.go").read("/src/main.go"),

		"file re-read after a failed edit": newStream("a").
			read("/src/main.go").edit("/src/main.go", failed).read("/src/main.go"),

		"file re-read after a shell command": newStream("a").
			read("/src/main.go").shell("gofmt-w").read("/src/main.go"),

		"file re-read after an unrecognised tool": newStream("a").
			read("/src/main.go").unrecognised("mcp__db__query").read("/src/main.go"),

		"file re-read after a subagent ran": newStream("a").
			read("/src/main.go").subagent("explore").read("/src/main.go"),

		"file re-read by a nested agent": newStream("a").
			read("/src/main.go").inSubagent("sub-1").read("/src/main.go"),

		"file re-read after the context was reset": newStream("a").
			read("/src/main.go").sessionStart("compact").read("/src/main.go"),

		"failed reads of the same file": newStream("a").
			read("/src/main.go", failed).read("/src/main.go", failed),

		"read repeated only in part": newStream("a").
			readRange("/src/main.go", 0).readRange("/src/main.go", 0),

		"different files read once each": newStream("a").
			read("/src/a.go").read("/src/b.go").read("/src/c.go"),

		"different commands": newStream("a").
			shell("go-test").shell("go-vet").shell("go-build"),

		// A repeated failed attempt is only a finding when nothing between
		// the attempts could have made the next one worth trying.
		"command attempted again after an edit": newStream("a").inTurn("t1").
			shell("go-test", failing("x")).edit("/src/main.go").shell("go-test", failing("x")),

		"command attempted again after another command": newStream("a").inTurn("t1").
			shell("go-test", failing("x")).shell("go-vet").shell("go-test", failing("x")),

		"command attempted again after another command failed": newStream("a").inTurn("t1").
			shell("go-test", failing("x")).shell("go-vet", failing("y")).shell("go-test", failing("x")),

		"command attempted again after an unrecognised tool": newStream("a").inTurn("t1").
			shell("go-test", failing("x")).unrecognised("mcp__db__query").shell("go-test", failing("x")),

		"command attempted again after a subagent ran": newStream("a").inTurn("t1").
			shell("go-test", failing("x")).subagent("explore").shell("go-test", failing("x")),

		"command attempted again after a background command": newStream("a").inTurn("t1").
			shell("go-test", failing("x")).background("serve").shell("go-test", failing("x")),

		"command attempted again after the context was reset": newStream("a").inTurn("t1").
			shell("go-test", failing("x")).sessionStart("compact").shell("go-test", failing("x")),

		// A person stopped the call, so what the agent does next answers
		// them rather than repeating itself.
		"command attempted again after an interrupt": newStream("a").inTurn("t1").
			shell("go-test", failing("x")).shell("go-test", interrupted).shell("go-test", failing("x")),

		"command interrupted twice": newStream("a").inTurn("t1").
			shell("go-test", interrupted).shell("go-test", interrupted),

		"command attempted again in a later turn": newStream("a").inTurn("t1").
			shell("go-test", failing("x")).inTurn("t2").shell("go-test", failing("x")),

		// Without a turn there is no boundary to respect, so the claim
		// cannot be made at all.
		"command attempted again with no turn identifier": newStream("a").
			shell("go-test", failing("x")).shell("go-test", failing("x")),

		"command attempted again in a later session": newStream("a").inTurn("t1").
			shell("go-test", failing("x")).as("b").shell("go-test", failing("x")),

		"command attempted again by a nested agent": newStream("a").inTurn("t1").
			shell("go-test", failing("x")).inSubagent("sub-1").shell("go-test", failing("x")),

		"command that failed once and then succeeded": newStream("a").inTurn("t1").
			shell("go-test", failing("x")).shell("go-test"),
	}

	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			report := analyze(s)
			if got := report.Findings; len(got) != 0 {
				t.Errorf("got %d findings, want none:\n%+v", len(got), got)
			}

			// Without this, a builder that stopped emitting events would make
			// every case above pass while proving nothing.
			want := toolCalls(s)
			if want < 2 {
				t.Fatalf("the case only builds %d tool calls, too few to repeat anything", want)
			}
			if report.ToolCalls != want {
				t.Fatalf("profiler saw %d tool calls, want the %d the case builds", report.ToolCalls, want)
			}
		})
	}
}

// toolCalls counts the tool calls a stream contains.
func toolCalls(s *stream) int {
	n := 0
	for _, ev := range s.events {
		if ev.Type == event.TypeToolCall {
			n++
		}
	}
	return n
}

func TestRepeatedShellFinding(t *testing.T) {
	t.Parallel()

	// Reads and searches between the runs observe without changing anything.
	report := analyze(newStream("session-1").
		shell("go-test", took(300)).
		read("/src/main.go").
		shell("go-test", took(200)).
		search("todo").
		shell("go-test", took(140)))

	if len(report.Findings) != 1 {
		t.Fatalf("got %d findings, want 1:\n%+v", len(report.Findings), report.Findings)
	}
	f := report.Findings[0]

	if f.Kind != profiler.KindRepeatedShell {
		t.Errorf("Kind = %q", f.Kind)
	}
	if f.SessionID != "session-1" {
		t.Errorf("SessionID = %q", f.SessionID)
	}
	if f.Occurrences != 3 || f.Redundant != 2 {
		t.Errorf("Occurrences = %d, Redundant = %d, want 3 and 2", f.Occurrences, f.Redundant)
	}
	if f.CommandDigest != "go-test" {
		t.Errorf("CommandDigest = %q", f.CommandDigest)
	}
	if f.Path != "" {
		t.Errorf("Path = %q, want it empty for a shell finding", f.Path)
	}
	// Only the repeats are wasted work; the first execution had to happen.
	if f.ObservedTotal == nil || *f.ObservedTotal != 340*time.Millisecond {
		t.Errorf("ObservedTotal = %v, want 340ms", f.ObservedTotal)
	}
	if !f.Last.After(f.First) {
		t.Errorf("window = %s to %s", f.First, f.Last)
	}
}

func TestRepeatedReadFinding(t *testing.T) {
	t.Parallel()

	// Editing a different file says nothing about this one.
	report := analyze(newStream("session-1").
		read("/src/main.go", took(6)).
		edit("/src/other.go").
		read("/src/main.go", took(4)))

	if len(report.Findings) != 1 {
		t.Fatalf("got %d findings, want 1:\n%+v", len(report.Findings), report.Findings)
	}
	f := report.Findings[0]

	if f.Kind != profiler.KindRepeatedRead {
		t.Errorf("Kind = %q", f.Kind)
	}
	if f.Occurrences != 2 || f.Redundant != 1 {
		t.Errorf("Occurrences = %d, Redundant = %d, want 2 and 1", f.Occurrences, f.Redundant)
	}
	if f.Path != "/src/main.go" {
		t.Errorf("Path = %q", f.Path)
	}
	if f.CommandDigest != "" {
		t.Errorf("CommandDigest = %q, want it empty for a read finding", f.CommandDigest)
	}
	if f.ObservedTotal == nil || *f.ObservedTotal != 4*time.Millisecond {
		t.Errorf("ObservedTotal = %v, want 4ms", f.ObservedTotal)
	}
}

// A read is counted only where it was observed returning something. A read that
// failed and a read whose outcome was never established are both unusable as
// evidence of what the agent had, and neither is a change to the file, so the
// reads around them still form one run.
func TestOnlyAnObservedReadCounts(t *testing.T) {
	t.Parallel()

	for name, unusable := range map[string]option{
		"a read that failed":                     failed,
		"a read with no outcome":                 unestablished(""),
		"a read with an outcome Axiom knows not": unestablished("blocked"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			report := analyze(newStream("a").
				read("/src/main.go").
				read("/src/main.go", unusable).
				read("/src/main.go"))

			if len(report.Findings) != 1 {
				t.Fatalf("got %d findings, want 1:\n%+v", len(report.Findings), report.Findings)
			}
			if got := report.Findings[0].Occurrences; got != 2 {
				t.Errorf("Occurrences = %d, want 2: only the reads observed returning something count", got)
			}
		})
	}
}

// Writing a file then reading it twice is still a repeated read: the write
// only clears what came before it.
func TestWriteThenRepeatedRead(t *testing.T) {
	t.Parallel()

	report := analyze(newStream("a").
		write("/src/main.go").
		read("/src/main.go").
		read("/src/main.go"))

	if len(report.Findings) != 1 {
		t.Fatalf("got %d findings, want 1:\n%+v", len(report.Findings), report.Findings)
	}
	if got := report.Findings[0].Occurrences; got != 2 {
		t.Errorf("Occurrences = %d, want 2", got)
	}
}

// A barrier splits repetition into separate runs rather than merging them.
func TestSeparateRunsOfTheSameFile(t *testing.T) {
	t.Parallel()

	report := analyze(newStream("a").
		read("/src/main.go").
		read("/src/main.go").
		edit("/src/main.go").
		read("/src/main.go").
		read("/src/main.go"))

	if len(report.Findings) != 2 {
		t.Fatalf("got %d findings, want 2:\n%+v", len(report.Findings), report.Findings)
	}
	for i, f := range report.Findings {
		if f.Occurrences != 2 || f.Redundant != 1 {
			t.Errorf("finding %d: Occurrences = %d, Redundant = %d, want 2 and 1", i, f.Occurrences, f.Redundant)
		}
	}
}

// Hooks run as parallel processes, so a later record can carry an earlier
// timestamp. The window has to bound the run either way.
func TestWindowSurvivesOutOfOrderTimestamps(t *testing.T) {
	t.Parallel()

	late := time.Date(2026, 8, 10, 20, 30, 0, 0, time.UTC)
	early := time.Date(2026, 8, 10, 20, 10, 0, 0, time.UTC)
	middle := time.Date(2026, 8, 10, 20, 20, 0, 0, time.UTC)

	s := newStream("a").read("/src/main.go").read("/src/main.go").read("/src/main.go")
	s.events[0].Timestamp = late
	s.events[1].Timestamp = early
	s.events[2].Timestamp = middle

	report := analyze(s)
	if len(report.Findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(report.Findings))
	}
	f := report.Findings[0]

	if !f.First.Equal(early) {
		t.Errorf("First = %s, want the earliest timestamp %s", f.First, early)
	}
	if !f.Last.Equal(late) {
		t.Errorf("Last = %s, want the latest timestamp %s", f.Last, late)
	}
	if f.Last.Before(f.First) {
		t.Error("the window reads backwards")
	}
}

// A subagent reasons in its own context, so its findings must name it rather
// than appear as more work by the session's own agent.
func TestFindingsAreAttributedToTheirContext(t *testing.T) {
	t.Parallel()

	s := newStream("session-1").
		read("/src/main.go").read("/src/main.go").
		inSubagent("sub-1").
		read("/src/main.go").read("/src/main.go")

	report := analyze(s)
	if len(report.Findings) != 2 {
		t.Fatalf("got %d findings, want one per context:\n%+v", len(report.Findings), report.Findings)
	}

	var parent, nested int
	for _, f := range report.Findings {
		if f.SessionID != "session-1" {
			t.Errorf("SessionID = %q, want session-1", f.SessionID)
		}
		switch f.SubagentID {
		case "":
			parent++
		case "sub-1":
			nested++
		default:
			t.Errorf("SubagentID = %q", f.SubagentID)
		}
	}
	if parent != 1 || nested != 1 {
		t.Errorf("got %d findings for the session and %d for the subagent, want 1 each", parent, nested)
	}
}

func TestMissingDurationIsNotReportedAsZero(t *testing.T) {
	t.Parallel()

	report := analyze(newStream("a").
		read("/src/main.go").
		read("/src/main.go", untimed))

	if len(report.Findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(report.Findings))
	}
	if got := report.Findings[0].ObservedTotal; got != nil {
		t.Errorf("ObservedTotal = %v, want nil when a duration is missing", got)
	}
}

// Identity has to be recorded as the run is observed. Which occurrence was
// which cannot be recovered afterwards.
func TestFindingIdentifiesEveryOccurrenceInOrder(t *testing.T) {
	t.Parallel()

	report := analyze(newStream("a").inTurn("turn-1").
		read("/src/main.go", invocation("call-1")).
		read("/src/main.go", invocation("call-2")).
		inTurn("turn-2").
		read("/src/main.go", invocation("call-3")))

	if len(report.Findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(report.Findings))
	}
	f := report.Findings[0]

	want := []profiler.Call{
		{TurnID: "turn-1", InvocationID: "call-1"},
		{TurnID: "turn-1", InvocationID: "call-2"},
		{TurnID: "turn-2", InvocationID: "call-3"},
	}
	if len(f.Calls) != len(want) {
		t.Fatalf("got %d calls, want %d: %+v", len(f.Calls), len(want), f.Calls)
	}
	for i := range want {
		if f.Calls[i] != want[i] {
			t.Errorf("Calls[%d] = %+v, want %+v", i, f.Calls[i], want[i])
		}
	}
}

// The counts describe the same occurrences the identities do, so a reader
// cannot be told about two repeats and handed three identities.
func TestOccurrenceCountsMatchTheIdentities(t *testing.T) {
	t.Parallel()

	report := analyze(newStream("a").
		read("/src/main.go", invocation("call-1")).
		read("/src/main.go", invocation("call-2")).
		read("/src/main.go", invocation("call-3")))

	f := report.Findings[0]
	if f.Occurrences != len(f.Calls) {
		t.Errorf("Occurrences = %d, want %d", f.Occurrences, len(f.Calls))
	}
	if f.Redundant != len(f.Calls)-1 {
		t.Errorf("Redundant = %d, want %d", f.Redundant, len(f.Calls)-1)
	}
}

// An agent that reports no invocation identifiers still produces findings.
// Only the join to a measurement is lost.
func TestOccurrenceIdentityIsOptional(t *testing.T) {
	t.Parallel()

	report := analyze(newStream("a").
		read("/src/main.go").
		read("/src/main.go"))

	f := report.Findings[0]
	if f.Occurrences != 2 {
		t.Errorf("Occurrences = %d, want 2", f.Occurrences)
	}
	for i, c := range f.Calls {
		if c.InvocationID != "" {
			t.Errorf("Calls[%d].InvocationID = %q, want empty", i, c.InvocationID)
		}
	}
}

// A run reported while it is still open keeps growing. The finding already
// handed out must not grow with it.
func TestReportedCallsAreNotAffectedByLaterOccurrences(t *testing.T) {
	t.Parallel()

	p := profiler.New()
	s := newStream("a").
		read("/src/main.go", invocation("call-1")).
		read("/src/main.go", invocation("call-2"))
	for _, ev := range s.events {
		p.Add(ev)
	}

	before := p.Report().Findings[0]
	for _, ev := range newStream("a").read("/src/main.go", invocation("call-3")).events {
		p.Add(ev)
	}

	if len(before.Calls) != 2 {
		t.Errorf("earlier finding now reports %d calls, want 2: %+v", len(before.Calls), before.Calls)
	}
	if got := len(p.Report().Findings[0].Calls); got != 3 {
		t.Errorf("later finding reports %d calls, want 3", got)
	}
}

// Sessions are interleaved in the log because hooks from concurrent agents
// append to the same file.
func TestInterleavedSessionsAreAnalyzedSeparately(t *testing.T) {
	t.Parallel()

	p := profiler.New()
	one := newStream("session-1")
	two := newStream("session-2")

	one.read("/src/a.go")
	two.read("/src/b.go")
	one.read("/src/a.go")
	two.read("/src/b.go")

	for _, ev := range append(one.events, two.events...) {
		p.Add(ev)
	}
	report := p.Report()

	if len(report.Findings) != 2 {
		t.Fatalf("got %d findings, want one per session:\n%+v", len(report.Findings), report.Findings)
	}
	sessions := map[string]string{}
	for _, f := range report.Findings {
		sessions[f.SessionID] = f.Path
	}
	if sessions["session-1"] != "/src/a.go" || sessions["session-2"] != "/src/b.go" {
		t.Errorf("findings did not stay within their sessions: %v", sessions)
	}
}

func TestReportCounts(t *testing.T) {
	t.Parallel()

	report := analyze(newStream("a").
		sessionStart("startup").
		read("/src/main.go").
		shell("go-test").
		sessionEnd().
		as("b").
		sessionStart("startup").
		read("/src/main.go").
		sessionEnd())

	if report.Events != 7 {
		t.Errorf("Events = %d, want 7", report.Events)
	}
	if report.Sessions != 2 {
		t.Errorf("Sessions = %d, want 2", report.Sessions)
	}
	if report.ToolCalls != 3 {
		t.Errorf("ToolCalls = %d, want 3", report.ToolCalls)
	}
}

func TestFindingOrderIsDeterministic(t *testing.T) {
	t.Parallel()

	s := newStream("a").
		read("/src/b.go").read("/src/b.go").
		shell("go-test").shell("go-test").
		as("c").
		read("/src/a.go").read("/src/a.go")

	first := analyze(s)
	for range 5 {
		got := analyze(s)
		if len(got.Findings) != len(first.Findings) {
			t.Fatalf("finding count varies between runs")
		}
		for i := range got.Findings {
			if identity(got.Findings[i]) != identity(first.Findings[i]) {
				t.Fatalf("finding %d differs between runs:\n%+v\n%+v", i, got.Findings[i], first.Findings[i])
			}
		}
	}

	if len(first.Findings) != 3 {
		t.Fatalf("got %d findings, want 3", len(first.Findings))
	}
	for i := 1; i < len(first.Findings); i++ {
		if first.Findings[i].First.Before(first.Findings[i-1].First) {
			t.Errorf("findings are not ordered by time of first occurrence")
		}
	}
}

// Reporting is a read of accumulated state, so it can be repeated and can be
// followed by more events.
func TestReportDoesNotConsumeState(t *testing.T) {
	t.Parallel()

	p := profiler.New()
	s := newStream("a").read("/src/main.go").read("/src/main.go")
	for _, ev := range s.events {
		p.Add(ev)
	}

	if got := len(p.Report().Findings); got != 1 {
		t.Fatalf("first report has %d findings, want 1", got)
	}
	if got := len(p.Report().Findings); got != 1 {
		t.Fatalf("second report has %d findings, want 1", got)
	}

	more := newStream("a")
	more.read("/src/main.go")
	for _, ev := range more.events {
		p.Add(ev)
	}
	if got := p.Report().Findings[0].Occurrences; got != 3 {
		t.Errorf("Occurrences = %d after a later read, want 3", got)
	}
}

func TestEmptyStreamProducesNothing(t *testing.T) {
	t.Parallel()

	report := profiler.New().Report()

	if len(report.Findings) != 0 || report.Events != 0 || report.Sessions != 0 {
		t.Errorf("report = %+v, want it empty", report)
	}
}

// A realistic exploration session: many files read once, a few commands, and
// a second session that legitimately revisits the same files.
func TestRealisticExplorationProducesNoFindings(t *testing.T) {
	t.Parallel()

	files := []string{
		"/repo/README.md", "/repo/internal/event/event.go", "/repo/internal/store/store.go",
		"/repo/internal/claude/adapter.go", "/repo/internal/cli/root.go",
	}

	s := newStream("session-1").sessionStart("startup").shell("git-status")
	for _, f := range files {
		s.read(f)
	}
	s.search("todo").shell("go-test").sessionEnd()

	s.as("session-2").sessionStart("startup").shell("git-log")
	for _, f := range files {
		s.read(f)
	}
	s.sessionEnd()

	report := analyze(s)
	if len(report.Findings) != 0 {
		t.Errorf("got %d findings, want none:\n%+v", len(report.Findings), report.Findings)
	}
	if report.Sessions != 2 {
		t.Errorf("Sessions = %d, want 2", report.Sessions)
	}
}
