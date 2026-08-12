package reacquire_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/reacquire"
	"github.com/exequieldeferrari/axiom/internal/timeline"
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
		now:     time.Date(2026, 8, 11, 20, 0, 0, 0, time.UTC),
		session: session,
	}
}

// as directs later events at a different session identity, which is what /clear
// and a fork produce.
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

func (s *stream) file(name string, outcome event.Outcome, f *event.FileOp) *stream {
	return s.add(event.Event{Type: event.TypeToolCall, Tool: &event.ToolCall{
		Name:     name,
		Outcome:  outcome,
		Metadata: &event.ToolMetadata{File: f},
	}})
}

func (s *stream) read(path string) *stream {
	return s.file("Read", event.OutcomeSuccess, &event.FileOp{Path: path, Access: event.AccessRead})
}

func (s *stream) failedRead(path string) *stream {
	return s.file("Read", event.OutcomeFailure, &event.FileOp{Path: path, Access: event.AccessRead})
}

// unestablishedRead is a read from a record that does not say what became of
// the call, which a future schema or a broken writer could produce.
func (s *stream) unestablishedRead(path string) *stream {
	return s.file("Read", event.Outcome(""), &event.FileOp{Path: path, Access: event.AccessRead})
}

func (s *stream) rangedRead(path string, offset int) *stream {
	return s.file("Read", event.OutcomeSuccess,
		&event.FileOp{Path: path, Access: event.AccessRead, Offset: &offset})
}

func (s *stream) edit(path string) *stream {
	return s.file("Edit", event.OutcomeSuccess, &event.FileOp{Path: path, Access: event.AccessEdit})
}

func (s *stream) write(path string) *stream {
	return s.file("Write", event.OutcomeSuccess, &event.FileOp{Path: path, Access: event.AccessWrite})
}

func (s *stream) failedEdit(path string) *stream {
	return s.file("Edit", event.OutcomeFailure, &event.FileOp{Path: path, Access: event.AccessEdit})
}

func (s *stream) unestablishedEdit(path string) *stream {
	return s.file("Edit", event.Outcome(""), &event.FileOp{Path: path, Access: event.AccessEdit})
}

func (s *stream) shell(digest string) *stream {
	return s.add(event.Event{Type: event.TypeToolCall, Tool: &event.ToolCall{
		Name:     "Bash",
		Outcome:  event.OutcomeSuccess,
		Metadata: &event.ToolMetadata{Shell: &event.ShellOp{CommandDigest: digest}},
	}})
}

// analyze replays a stream through the same single pass the CLI uses: the
// timeline places each record and the accumulator is handed the placement.
func analyze(s *stream) reacquire.Report {
	tl := timeline.New()
	a := reacquire.New()
	for _, ev := range s.events {
		a.Add(ev, tl.Add(ev))
	}
	return a.Report()
}

// shape renders a report as one line per path, so that a test can state what it
// expects instead of walking the structure field by field.
//
// Each acquisition reads "ordinal(opening) rN". A write or edit recorded after
// the read appends "+w/e", and one whose outcome was never established appends
// "+no-outcome:N", so the two are never read as the same observation.
func shape(r reacquire.Report) []string {
	out := make([]string, 0, len(r.Paths))
	for _, p := range r.Paths {
		parts := make([]string, 0, len(p.Epochs))
		for _, a := range p.Epochs {
			part := fmt.Sprintf("%d(%s) r%d", a.Epoch.Ordinal, opening(a.Opening), a.Reads)
			if a.WriteOrEditAfter {
				part += " +w/e"
			}
			if a.UnestablishedAfter > 0 {
				part += fmt.Sprintf(" +no-outcome:%d", a.UnestablishedAfter)
			}
			parts = append(parts, part)
		}
		out = append(out, fmt.Sprintf("%s %s: %s", p.SessionID, p.Path, strings.Join(parts, " | ")))
	}
	return out
}

func opening(o timeline.Opening) string {
	switch o.Kind {
	case timeline.OpeningRecorded:
		return o.Source
	case timeline.OpeningUnspecified:
		return "unspecified"
	default:
		return "absent"
	}
}

func assertShape(t *testing.T, r reacquire.Report, want ...string) {
	t.Helper()
	if got := shape(r); !slices.Equal(got, want) {
		t.Errorf("paths:\n got %v\nwant %v", got, want)
	}
}

func assertNoPaths(t *testing.T, r reacquire.Report) {
	t.Helper()
	if len(r.Paths) != 0 {
		t.Errorf("paths = %v, want none", shape(r))
	}
}

// This is the sequence Claude Code 2.1.228 was observed producing in the
// controlled capture for this milestone: one session identity resumed four
// times, reading one path on either side of a boundary with an edit of it in
// between, and another path read and then edited inside two later epochs.
//
// It is replayed here so that the relations Axiom derives from a real capture
// are pinned, not just the ones it derives from invented sequences.
func TestObservedCapture(t *testing.T) {
	t.Parallel()

	const (
		notes = "/private/tmp/axiom-pr11/proj/notes.txt"
		other = "/private/tmp/axiom-pr11/proj/other.txt"
	)
	s := newStream("a01159c5").
		start("startup").inTurn("t1").read(notes).end("other").
		start("resume").inTurn("t2").edit(notes).end("other").
		start("resume").inTurn("t3").read(notes).end("other").
		start("resume").inTurn("t4").read(other).edit(other).end("other").
		start("resume").inTurn("t5").read(other).edit(other).end("other")

	// notes.txt was read in epochs 1 and 3, and the edit of it fell in epoch 2,
	// which is not after either read in the epoch that read it. other.txt was
	// read in epochs 4 and 5 with an edit after the read in each.
	assertShape(t, analyze(s),
		"a01159c5 "+notes+": 1(startup) r1 | 3(resume) r1",
		"a01159c5 "+other+": 4(resume) r1 +w/e | 5(resume) r1 +w/e",
	)
}

// A session with one epoch has no boundary for a path to be read across,
// however many times it was read.
func TestOneEpochIsNeverAReacquisition(t *testing.T) {
	t.Parallel()

	r := analyze(newStream("a").start("startup").inTurn("t1").
		read("/repo/x.go").read("/repo/x.go").read("/repo/x.go"))

	assertNoPaths(t, r)
	if r.MultiEpochSessions != 0 {
		t.Errorf("MultiEpochSessions = %d, want 0", r.MultiEpochSessions)
	}
}

// Repetition inside one context is the profiler's subject. Many reads in one
// epoch collapse to one acquisition, and the count is carried rather than lost.
func TestRepeatedReadsInsideOneEpochAreOneAcquisition(t *testing.T) {
	t.Parallel()

	assertShape(t, analyze(newStream("a").
		start("startup").inTurn("t1").read("/repo/x.go").read("/repo/x.go").
		start("compact").inTurn("t2").read("/repo/x.go")),
		"a /repo/x.go: 1(startup) r2 | 2(compact) r1")
}

// The same path read under two session identities is two unrelated
// observations. Nothing recorded links one identity to another, so linking them
// here would invent the relation the report is about.
func TestSessionIdentitiesAreNeverCompared(t *testing.T) {
	t.Parallel()

	r := analyze(newStream("a").start("startup").inTurn("t1").read("/repo/x.go").end("clear").
		as("b").start("clear").inTurn("t2").read("/repo/x.go"))

	assertNoPaths(t, r)
	if r.MultiEpochSessions != 0 {
		t.Errorf("MultiEpochSessions = %d, want 0: neither identity had two epochs", r.MultiEpochSessions)
	}
}

// A read that did not establish that it delivered the file's contents is not an
// acquisition. The record says what became of a call and never what it
// returned, so neither a failure nor an unestablished outcome can stand for one.
func TestOnlySuccessfulWholeFileReadsAcquire(t *testing.T) {
	t.Parallel()

	cases := map[string]func(*stream, string) *stream{
		"failed read":        (*stream).failedRead,
		"unestablished read": (*stream).unestablishedRead,
	}
	for name, read := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := newStream("a").start("startup").inTurn("t1").read("/repo/x.go").start("compact").inTurn("t2")
			assertNoPaths(t, analyze(read(s, "/repo/x.go")))
		})
	}

	t.Run("ranged read", func(t *testing.T) {
		t.Parallel()
		assertNoPaths(t, analyze(newStream("a").
			start("startup").inTurn("t1").read("/repo/x.go").
			start("compact").inTurn("t2").rangedRead("/repo/x.go", 40)))
	})

	// The exclusion runs both ways: a ranged read is not an acquisition a later
	// whole-file read can be paired with either.
	t.Run("ranged read first", func(t *testing.T) {
		t.Parallel()
		assertNoPaths(t, analyze(newStream("a").
			start("startup").inTurn("t1").rangedRead("/repo/x.go", 40).
			start("compact").inTurn("t2").read("/repo/x.go")))
	})
}

// A nested agent reasons in a context of its own, and these epochs are the
// session's. Its reading is set aside, and counted so that it does not vanish.
func TestSubagentReadsAreExcludedAndAccountedFor(t *testing.T) {
	t.Parallel()

	r := analyze(newStream("a").
		start("startup").inTurn("t1").read("/repo/x.go").
		inSubagent("nested").read("/repo/x.go").read("/repo/y.go").inSubagent("").
		start("compact").inTurn("t2").inSubagent("nested").read("/repo/x.go").inSubagent(""))

	assertNoPaths(t, r)
	if r.SubagentReads != 3 {
		t.Errorf("SubagentReads = %d, want 3", r.SubagentReads)
	}
}

// A subagent's reading does not stand in for the session's own on either side
// of a boundary: the relation below exists because the session read the path
// twice, and the nested read neither creates nor completes one.
func TestSubagentReadDoesNotCompleteARelation(t *testing.T) {
	t.Parallel()

	r := analyze(newStream("a").
		start("startup").inTurn("t1").inSubagent("nested").read("/repo/x.go").inSubagent("").
		start("compact").inTurn("t2").read("/repo/x.go").
		start("compact").inTurn("t3").read("/repo/x.go"))

	assertShape(t, r, "a /repo/x.go: 2(compact) r1 | 3(compact) r1")
	if r.SubagentReads != 1 {
		t.Errorf("SubagentReads = %d, want 1", r.SubagentReads)
	}
}

// A write or edit is noted only where it was recorded after the read, in the
// epoch that read the path. Every other arrangement is a different observation.
func TestWriteOrEditAfterAnAcquisition(t *testing.T) {
	t.Parallel()

	const path = "/repo/x.go"
	first := func() *stream {
		return newStream("a").start("startup").inTurn("t1").read(path).start("compact").inTurn("t2")
	}

	cases := map[string]struct {
		later *stream
		want  string
	}{
		"edit after the read": {
			later: first().read(path).edit(path),
			want:  "2(compact) r1 +w/e",
		},
		"write after the read": {
			later: first().read(path).write(path),
			want:  "2(compact) r1 +w/e",
		},
		// The record establishes that the call was made and what became of it,
		// which is all this reports. Reporting nothing here would say no such
		// call followed the read, which the record contradicts.
		"failed edit after the read": {
			later: first().read(path).failedEdit(path),
			want:  "2(compact) r1 +w/e",
		},
		// The edit came first, so nothing followed the read.
		"edit before the read": {
			later: first().edit(path).read(path),
			want:  "2(compact) r1",
		},
		// An edit of a different path says nothing about this one.
		"edit of another path": {
			later: first().read(path).edit("/repo/y.go"),
			want:  "2(compact) r1",
		},
		// The edit falls between two reads, so it is after the acquisition.
		"read, edit, read": {
			later: first().read(path).edit(path).read(path),
			want:  "2(compact) r2 +w/e",
		},
		// The first read is where the acquisition begins, so an edit before the
		// second read is still after it.
		"edit, read, read": {
			later: first().edit(path).read(path).read(path),
			want:  "2(compact) r2",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assertShape(t, analyze(tc.later), "a "+path+": 1(startup) r1 | "+tc.want)
		})
	}
}

// A write or edit whose outcome was never established is not known to have run
// at all, so it is neither a recorded operation after the read nor evidence
// that none followed. It is held apart from both.
func TestUnestablishedWriteOrEditIsNeitherOutcome(t *testing.T) {
	t.Parallel()

	const path = "/repo/x.go"
	base := newStream("a").start("startup").inTurn("t1").read(path).start("compact").inTurn("t2").read(path)

	assertShape(t, analyze(base.unestablishedEdit(path)),
		"a "+path+": 1(startup) r1 | 2(compact) r1 +no-outcome:1")

	// An established outcome alongside one that is not keeps both facts: the
	// first call is observed, and the second is still not evidence of anything.
	assertShape(t, analyze(newStream("a").
		start("startup").inTurn("t1").read(path).
		start("compact").inTurn("t2").read(path).unestablishedEdit(path).edit(path)),
		"a "+path+": 1(startup) r1 | 2(compact) r1 +w/e +no-outcome:1")
}

// Identity is the exact string the agent named. Two names for one file stay
// apart, which can only lose a relation and never invent one.
func TestPathIdentityIsExact(t *testing.T) {
	t.Parallel()

	assertNoPaths(t, analyze(newStream("a").
		start("startup").inTurn("t1").read("/private/tmp/x.go").
		start("compact").inTurn("t2").read("/tmp/x.go")))
}

// An epoch that recorded no work is still an epoch, and a path read on either
// side of it was read across a boundary.
func TestEpochWithNoWorkStillSeparates(t *testing.T) {
	t.Parallel()

	r := analyze(newStream("a").
		start("startup").inTurn("t1").read("/repo/x.go").
		start("compact").
		start("compact").inTurn("t2").read("/repo/x.go"))

	assertShape(t, r, "a /repo/x.go: 1(startup) r1 | 3(compact) r1")
	if r.MultiEpochSessions != 1 {
		t.Errorf("MultiEpochSessions = %d, want 1", r.MultiEpochSessions)
	}
}

// A record the timeline could not place has no side of a boundary to be on.
func TestRecordsWithNoEpochAreNotAnalyzed(t *testing.T) {
	t.Parallel()

	// A read carrying no session identity is placed nowhere, so the path is
	// read in one epoch only.
	r := analyze(newStream("a").start("startup").inTurn("t1").read("/repo/x.go").
		as("").read("/repo/x.go").
		as("a").read("/repo/x.go"))

	assertNoPaths(t, r)
	if r.MultiEpochSessions != 0 {
		t.Errorf("MultiEpochSessions = %d, want 0", r.MultiEpochSessions)
	}
}

// Work recorded for a session Axiom saw no start for opens an epoch marked as
// having none, and that is carried through rather than named as a source.
func TestOpeningWithNoStartRecordedIsCarried(t *testing.T) {
	t.Parallel()

	assertShape(t, analyze(newStream("a").
		inTurn("t1").read("/repo/x.go").
		start("compact").inTurn("t2").read("/repo/x.go")),
		"a /repo/x.go: 1(absent) r1 | 2(compact) r1")
}

// A source no version of Axiom has seen is reported as itself. Nothing here
// branches on the value, so a new one cannot change what the analysis does.
func TestUnknownOpeningSourceIsPreserved(t *testing.T) {
	t.Parallel()

	assertShape(t, analyze(newStream("a").
		start("teleport").inTurn("t1").read("/repo/x.go").
		start("").inTurn("t2").read("/repo/x.go")),
		"a /repo/x.go: 1(teleport) r1 | 2(unspecified) r1")
}

// Operations that name no path contribute nothing, and cannot end or interrupt
// a relation: this analysis has no barriers.
func TestOperationsWithoutAPathAreIgnored(t *testing.T) {
	t.Parallel()

	assertShape(t, analyze(newStream("a").
		start("startup").inTurn("t1").read("/repo/x.go").shell("d1").
		start("compact").inTurn("t2").shell("d2").read("/repo/x.go")),
		"a /repo/x.go: 1(startup) r1 | 2(compact) r1")
}

// Ordering is by how many epochs the reading spanned, then on recorded strings,
// so two runs over one log cannot disagree however the maps iterate.
func TestOrderingIsDeterministic(t *testing.T) {
	t.Parallel()

	s := newStream("b").
		start("startup").inTurn("t1").read("/repo/x.go").read("/repo/y.go").read("/repo/a.go").
		start("compact").inTurn("t2").read("/repo/x.go").read("/repo/y.go").read("/repo/a.go").
		start("compact").inTurn("t3").read("/repo/y.go").
		as("a").start("startup").inTurn("t4").read("/repo/x.go").
		start("compact").inTurn("t5").read("/repo/x.go")

	want := []string{
		// Three epochs first, then the two-epoch paths by session and path.
		"b /repo/y.go: 1(startup) r1 | 2(compact) r1 | 3(compact) r1",
		"a /repo/x.go: 1(startup) r1 | 2(compact) r1",
		"b /repo/a.go: 1(startup) r1 | 2(compact) r1",
		"b /repo/x.go: 1(startup) r1 | 2(compact) r1",
	}
	for range 8 {
		assertShape(t, analyze(s), want...)
	}
}

// A session with more than one epoch and nothing read across it is a different
// fact from a log with no boundary in it at all, so the two are counted apart.
func TestMultiEpochSessionsIsTheDenominator(t *testing.T) {
	t.Parallel()

	r := analyze(newStream("a").
		start("startup").inTurn("t1").read("/repo/x.go").
		start("compact").inTurn("t2").read("/repo/y.go").
		as("b").start("startup").inTurn("t3").read("/repo/x.go"))

	assertNoPaths(t, r)
	if r.MultiEpochSessions != 1 {
		t.Errorf("MultiEpochSessions = %d, want 1", r.MultiEpochSessions)
	}
}

// Reporting is not consuming: a report can be taken while a log is still being
// read, and work arriving afterwards extends the relations already found.
func TestReportDoesNotConsumeTheAccumulator(t *testing.T) {
	t.Parallel()

	tl := timeline.New()
	a := reacquire.New()
	feed := func(s *stream) {
		for _, ev := range s.events {
			a.Add(ev, tl.Add(ev))
		}
	}

	feed(newStream("a").start("startup").inTurn("t1").read("/repo/x.go").
		start("compact").inTurn("t2").read("/repo/x.go"))
	assertShape(t, a.Report(), "a /repo/x.go: 1(startup) r1 | 2(compact) r1")
	assertShape(t, a.Report(), "a /repo/x.go: 1(startup) r1 | 2(compact) r1")

	feed(newStream("a").inTurn("t2").edit("/repo/x.go"))
	assertShape(t, a.Report(), "a /repo/x.go: 1(startup) r1 | 2(compact) r1 +w/e")
}

// The empty case has to be empty, and has to distinguish nothing observed from
// nothing to observe.
func TestEmptyReport(t *testing.T) {
	t.Parallel()

	r := reacquire.New().Report()
	if len(r.Paths) != 0 || r.MultiEpochSessions != 0 || r.SubagentReads != 0 {
		t.Errorf("empty report = %+v", r)
	}
}
