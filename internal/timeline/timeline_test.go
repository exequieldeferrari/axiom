package timeline_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/event"
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

func (s *stream) inSubagent(id string) *stream {
	s.nested = id
	return s
}

// at overrides the time of the next event, including with a zero time, so that
// a log whose times are missing or out of order can be replayed.
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
	if !s.now.IsZero() {
		s.now = s.now.Add(time.Second)
	}
	s.events = append(s.events, ev)
	return s
}

func (s *stream) start(source string) *stream {
	return s.add(event.Event{Type: event.TypeSessionStart, Session: &event.Session{Source: source}})
}

func (s *stream) end(reason string) *stream {
	return s.add(event.Event{Type: event.TypeSessionEnd, Session: &event.Session{Reason: reason}})
}

func (s *stream) read(path string) *stream {
	return s.add(event.Event{Type: event.TypeToolCall, Tool: &event.ToolCall{
		Name:     "Read",
		Outcome:  event.OutcomeSuccess,
		Metadata: &event.ToolMetadata{File: &event.FileOp{Path: path, Access: event.AccessRead}},
	}})
}

// callWithoutTool is a tool_call record carrying no call, which a future or
// broken writer could produce.
func (s *stream) callWithoutTool() *stream {
	return s.add(event.Event{Type: event.TypeToolCall})
}

// unknownType is a record from a schema version that added an event type this
// one does not know.
func (s *stream) unknownType() *stream {
	return s.add(event.Event{Type: event.Type("checkpoint")})
}

func derive(s *stream) timeline.Report {
	t := timeline.New()
	for _, ev := range s.events {
		t.Add(ev)
	}
	return t.Report()
}

// shape renders a report as one line per session identity, so that a test can
// state the structure it expects instead of walking it field by field.
//
// Each epoch reads "ordinal opening→closing cN tN sN": tool calls, turns with
// work, and subagent calls.
func shape(r timeline.Report) []string {
	out := make([]string, 0, len(r.Sessions))
	for _, s := range r.Sessions {
		parts := make([]string, 0, len(s.Epochs))
		for _, e := range s.Epochs {
			parts = append(parts, fmt.Sprintf("%d %s→%s c%d t%d s%d",
				e.Ordinal, opening(e.Opening), closing(e.Closing), e.ToolCalls, e.Turns, e.SubagentCalls))
		}
		line := s.ID + ":"
		if len(parts) > 0 {
			line += " " + strings.Join(parts, " | ")
		}
		if s.EndsWithoutEpoch > 0 {
			line += fmt.Sprintf(" +%d end(s) with no epoch", s.EndsWithoutEpoch)
		}
		out = append(out, line)
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

func closing(c timeline.Closing) string {
	switch c.Kind {
	case timeline.ClosingEnded:
		return "ended(" + c.Reason + ")"
	case timeline.ClosingReset:
		return "reset"
	default:
		return "open"
	}
}

func assertShape(t *testing.T, r timeline.Report, want ...string) {
	t.Helper()
	got := shape(r)
	if !slices.Equal(got, want) {
		t.Errorf("structure:\n got %v\nwant %v", got, want)
	}
}

// These are the sequences Claude Code 2.1.228 was observed producing. They are
// replayed here so that the structure Axiom derives from real captures is
// pinned, not just the structure it derives from invented ones.
func TestObservedSequences(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		stream *stream
		want   []string
	}{
		// One session, one context, ending on its own.
		"startup": {
			newStream("a").start("startup").inTurn("t1").read("/repo/x.go").end("other"),
			[]string{"a: 1 startup→ended(other) c1 t1 s0"},
		},

		// Compaction keeps the session identity and opens a context inside it,
		// so one identity holds two epochs.
		"compaction mid-session": {
			newStream("a").start("startup").inTurn("t1").read("/repo/x.go").
				inTurn("t2").start("compact").read("/repo/y.go").end("other"),
			[]string{"a: 1 startup→reset c1 t1 s0 | 2 compact→ended(other) c1 t1 s0"},
		},

		// /clear ends the session with a reason and starts a different
		// identity. Axiom reports both and links neither.
		"clear": {
			newStream("a").start("startup").inTurn("t1").read("/repo/x.go").end("clear").
				as("b").inTurn("").start("clear").inTurn("t2").read("/repo/y.go").end("prompt_input_exit"),
			[]string{
				"a: 1 startup→ended(clear) c1 t1 s0",
				"b: 1 clear→ended(prompt_input_exit) c1 t1 s0",
			},
		},

		// Resuming reuses the identity, so the resumed work is a second epoch
		// of the same session rather than a new session.
		"resume": {
			newStream("a").start("startup").inTurn("t1").read("/repo/x.go").end("other").
				inTurn("").start("resume").inTurn("t2").read("/repo/y.go").end("other"),
			[]string{"a: 1 startup→ended(other) c1 t1 s0 | 2 resume→ended(other) c1 t1 s0"},
		},

		// A fork reports a new identity. Nothing recorded says what it forked
		// from.
		"fork": {
			newStream("a").start("startup").inTurn("t1").read("/repo/x.go").end("other").
				as("b").inTurn("").start("fork").inTurn("t2").read("/repo/y.go").end("other"),
			[]string{
				"a: 1 startup→ended(other) c1 t1 s0",
				"b: 1 fork→ended(other) c1 t1 s0",
			},
		},

		// Automatic compaction was observed opening a context in the middle of
		// a turn: the turn was submitted before the boundary and its tool call
		// arrived after it. The resumed epoch that preceded it did no work.
		"automatic compaction inside a turn": {
			newStream("a").start("resume").inTurn("t1").start("compact").read("/repo/x.go").end("other"),
			[]string{"a: 1 resume→reset c0 t0 s0 | 2 compact→ended(other) c1 t1 s0"},
		},

		// A subagent's calls are recorded under the session's identity and
		// turn, and inside the epoch they arrived in.
		"subagent calls": {
			newStream("a").start("startup").inTurn("t1").
				inSubagent("sub-1").read("/repo/x.go").
				inSubagent("").read("/repo/y.go").end("other"),
			[]string{"a: 1 startup→ended(other) c2 t1 s1"},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assertShape(t, derive(tc.stream), tc.want...)
		})
	}
}

func TestOpeningAndClosingStates(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		stream *stream
		want   []string
	}{
		// A start with no source is a start Axiom saw and a source the agent
		// did not report, which is not the same as no start at all.
		"source not recorded": {
			newStream("a").start("").inTurn("t1").read("/repo/x.go"),
			[]string{"a: 1 unspecified→open c1 t1 s0"},
		},

		// A log that begins mid-session has work with no start before it.
		"no start recorded": {
			newStream("a").inTurn("t1").read("/repo/x.go").read("/repo/y.go"),
			[]string{"a: 1 absent→open c2 t1 s0"},
		},

		// A source no version of Axiom knows is reported as itself.
		"source Axiom does not know": {
			newStream("a").start("teleport").inTurn("t1").read("/repo/x.go"),
			[]string{"a: 1 teleport→open c1 t1 s0"},
		},

		// A reset with no work after it is still a reset that happened.
		"back to back starts": {
			newStream("a").start("startup").start("compact").start("compact").inTurn("t1").read("/repo/x.go"),
			[]string{"a: 1 startup→reset c0 t0 s0 | 2 compact→reset c0 t0 s0 | 3 compact→open c1 t1 s0"},
		},

		// An end with nothing open closes nothing, and is the only trace of a
		// session whose start was never recorded.
		"end without an open epoch": {
			newStream("a").end("clear"),
			[]string{"a: +1 end(s) with no epoch"},
		},

		// Work after the session was reported ending opens an epoch whose
		// start was not observed rather than continuing the closed one.
		"records after the session ended": {
			newStream("a").start("startup").inTurn("t1").read("/repo/x.go").end("other").read("/repo/y.go"),
			[]string{"a: 1 startup→ended(other) c1 t1 s0 | 2 absent→open c1 t1 s0"},
		},

		// An end whose reason the agent did not report.
		"end without a reason": {
			newStream("a").start("startup").inTurn("t1").read("/repo/x.go").end(""),
			[]string{"a: 1 startup→ended() c1 t1 s0"},
		},

		// Nothing was recorded after this epoch. That is a fact about the log
		// and not a claim that the agent is still running.
		"open at the end of the log": {
			newStream("a").start("startup").inTurn("t1").read("/repo/x.go"),
			[]string{"a: 1 startup→open c1 t1 s0"},
		},

		// Nothing validates the lifecycle detail on the way into or out of the
		// log, so a boundary can arrive without it. Losing the boundary would
		// be the worse outcome by far.
		"start with no lifecycle detail": {
			newStream("a").add(event.Event{Type: event.TypeSessionStart}).inTurn("t1").read("/repo/x.go"),
			[]string{"a: 1 unspecified→open c1 t1 s0"},
		},

		"end with no lifecycle detail": {
			newStream("a").start("startup").inTurn("t1").read("/repo/x.go").
				add(event.Event{Type: event.TypeSessionEnd}),
			[]string{"a: 1 startup→ended() c1 t1 s0"},
		},

		// A call whose turn was never recorded is still work, but it is not a
		// turn: nothing establishes which turn it belonged to.
		"call with no turn": {
			newStream("a").start("startup").read("/repo/x.go").read("/repo/y.go"),
			[]string{"a: 1 startup→open c2 t0 s0"},
		},

		// A record carrying no call describes no work, so it opens no context.
		"tool call with no call": {
			newStream("a").callWithoutTool(),
			[]string{},
		},

		// A type this version does not know is not work it can place.
		"unknown event type": {
			newStream("a").unknownType(),
			[]string{},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assertShape(t, derive(tc.stream), tc.want...)
		})
	}
}

// Two agents running at once append to one log, and their records interleave.
// Each identity keeps its own epochs, and neither one's boundary touches the
// other.
func TestInterleavedSessions(t *testing.T) {
	t.Parallel()

	s := newStream("a").start("startup").
		as("b").start("startup").
		as("a").inTurn("a1").read("/repo/x.go").
		as("b").inTurn("b1").read("/repo/y.go").
		as("a").inTurn("").start("compact").inTurn("a2").read("/repo/x.go").
		as("b").inTurn("b1").read("/repo/z.go").
		as("a").end("other")

	assertShape(t, derive(s),
		"a: 1 startup→reset c1 t1 s0 | 2 compact→ended(other) c1 t1 s0",
		"b: 1 startup→open c2 t1 s0",
	)
}

// A session identity is the only grouping the log establishes. Sessions appear
// in the order they were first recorded, and nothing links one to the next.
func TestSessionsAppearInRecordedOrder(t *testing.T) {
	t.Parallel()

	s := newStream("c").start("startup").as("a").start("startup").as("b").start("startup")

	var ids []string
	for _, session := range derive(s).Sessions {
		ids = append(ids, session.ID)
	}
	if want := []string{"c", "a", "b"}; !slices.Equal(ids, want) {
		t.Errorf("session order = %v, want %v", ids, want)
	}
}

// A turn can span a context reset, so per-epoch turn counts overlap and must
// never be added up.
func TestTurnSpanningEpochs(t *testing.T) {
	t.Parallel()

	s := newStream("a").inTurn("t1").read("/repo/x.go").start("compact").read("/repo/y.go")

	r := derive(s)
	assertShape(t, r, "a: 1 absent→reset c1 t1 s0 | 2 compact→open c1 t1 s0")

	session := r.Sessions[0]
	var summed int
	for _, e := range session.Epochs {
		summed += e.Turns
	}
	if summed != 2 {
		t.Fatalf("summed turns = %d, want 2", summed)
	}
	// One turn did the work in both epochs, so the sum overstates it. The
	// report must present these per epoch and never as a session total.
	if distinct := 1; summed <= distinct {
		t.Errorf("summed turns %d does not exceed the %d distinct turn, so this "+
			"test no longer covers a turn spanning a reset", summed, distinct)
	}
}

// Every tool call belongs to exactly one epoch, so epoch counts add up to the
// session's own total.
func TestToolCallsSumToTheSession(t *testing.T) {
	t.Parallel()

	s := newStream("a").start("startup").inTurn("t1").read("/repo/x.go").read("/repo/y.go").
		start("compact").inTurn("t2").read("/repo/x.go").
		end("clear").read("/repo/z.go").
		as("b").start("startup").inTurn("b1").read("/repo/x.go")

	var added int
	for _, ev := range s.events {
		if ev.Type == event.TypeToolCall && ev.Tool != nil {
			added++
		}
	}

	r := derive(s)
	var total int
	for _, session := range r.Sessions {
		total += session.ToolCalls()
	}
	if total != added {
		t.Errorf("tool calls across epochs = %d, want the %d recorded", total, added)
	}
	if r.Epochs() != 4 {
		t.Errorf("epochs = %d, want 4", r.Epochs())
	}
}

// Records with no session identity have no context to belong to. They are
// counted so that a report can account for them, and placed nowhere.
func TestRecordsWithoutASessionIdentity(t *testing.T) {
	t.Parallel()

	s := newStream("").start("startup").read("/repo/x.go").
		as("a").start("startup").inTurn("t1").read("/repo/y.go")

	r := derive(s)
	if r.Unidentified != 2 {
		t.Errorf("unidentified = %d, want 2", r.Unidentified)
	}
	assertShape(t, r, "a: 1 startup→open c1 t1 s0")
}

// Membership follows append order. Times are recorded by whichever hook process
// got there first, so they can arrive out of order or not at all, and they are
// never allowed to decide what belongs to an epoch.
func TestTimesDoNotDecideMembership(t *testing.T) {
	t.Parallel()

	early := time.Date(2026, 8, 11, 20, 0, 0, 0, time.UTC)
	late := early.Add(time.Hour)

	s := newStream("a").at(late).start("startup").
		inTurn("t1").at(early).read("/repo/x.go").
		at(time.Time{}).read("/repo/y.go").
		at(late.Add(time.Minute)).start("compact").inTurn("t2").read("/repo/z.go")

	r := derive(s)
	assertShape(t, r, "a: 1 startup→reset c2 t1 s0 | 2 compact→open c1 t1 s0")

	first := r.Sessions[0].Epochs[0]
	// The window reports the times that were recorded, out of order as they
	// were, and a record carrying no time cannot widen it.
	if !first.First.Equal(late) || !first.Last.Equal(early) {
		t.Errorf("window = %s → %s, want %s → %s", first.First, first.Last, late, early)
	}
}

// Reporting is not consuming: a report can be taken while a session is still
// being read, and work arriving afterwards continues the epoch it belongs to
// instead of starting another.
func TestReportDoesNotConsumeTheTimeline(t *testing.T) {
	t.Parallel()

	s := newStream("a").start("startup").inTurn("t1").read("/repo/x.go")
	tl := timeline.New()
	for _, ev := range s.events {
		tl.Add(ev)
	}

	first := tl.Report()
	if got := shape(first); !slices.Equal(got, []string{"a: 1 startup→open c1 t1 s0"}) {
		t.Fatalf("first report = %v", got)
	}
	if got := shape(tl.Report()); !slices.Equal(got, shape(first)) {
		t.Errorf("second report = %v, want %v", got, shape(first))
	}

	more := newStream("a").inTurn("t1").read("/repo/y.go")
	for _, ev := range more.events {
		tl.Add(ev)
	}
	assertShape(t, tl.Report(), "a: 1 startup→open c2 t1 s0")
}

// The empty case has to be empty: an epoch nobody recorded is not an epoch.
func TestEmptyTimeline(t *testing.T) {
	t.Parallel()

	r := timeline.New().Report()
	if len(r.Sessions) != 0 || r.Epochs() != 0 || r.Unidentified != 0 {
		t.Errorf("empty report = %+v", r)
	}
}
