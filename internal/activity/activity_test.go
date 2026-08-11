package activity_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/activity"
	"github.com/exequieldeferrari/axiom/internal/event"
)

const defaultDurationMS int64 = 5

// stream builds an ordered event sequence the way the store would replay it,
// giving every tool call an invocation identifier so that a measurement can be
// attached to one exact occurrence.
type stream struct {
	events  []event.Event
	now     time.Time
	session string
	turn    string
	nested  string
	calls   int
}

func newStream() *stream {
	return &stream{
		now:     time.Date(2026, 8, 11, 18, 0, 0, 0, time.UTC),
		session: "session-1",
		turn:    "turn-1",
	}
}

// in directs later events at a session and turn.
func (s *stream) in(session, turn string) *stream {
	s.session, s.turn = session, turn
	return s
}

// inSubagent directs later events at a nested agent within the same session.
func (s *stream) inSubagent(id string) *stream {
	s.nested = id
	return s
}

type option func(*event.ToolCall)

func failed(t *event.ToolCall)  { t.Outcome = event.OutcomeFailure }
func untimed(t *event.ToolCall) { t.DurationMS = nil }

// outcome sets the state of a call as a record holds it, including states no
// adapter writes today: nothing validates the field on the way in or out of the
// log.
func outcome(state string) option {
	return func(t *event.ToolCall) { t.Outcome = event.Outcome(state) }
}

// anonymous strips the invocation identifier, which is what an agent that
// reports no identity for a call produces.
func anonymous(t *event.ToolCall) { t.InvocationID = "" }

func took(ms int64) option {
	return func(t *event.ToolCall) { t.DurationMS = &ms }
}

func (s *stream) tool(name string, md *event.ToolMetadata, opts ...option) *stream {
	s.now = s.now.Add(time.Second)
	s.calls++

	d := defaultDurationMS
	call := &event.ToolCall{
		Name:         name,
		InvocationID: fmt.Sprintf("call-%d", s.calls),
		Outcome:      event.OutcomeSuccess,
		DurationMS:   &d,
		Metadata:     md,
	}
	for _, opt := range opts {
		opt(call)
	}

	s.events = append(s.events, event.Event{
		SchemaVersion: event.SchemaVersion,
		Agent:         "test",
		Type:          event.TypeToolCall,
		Timestamp:     s.now,
		SessionID:     s.session,
		TurnID:        s.turn,
		SubagentID:    s.nested,
		Tool:          call,
	})
	return s
}

func (s *stream) file(path, access string, opts ...option) *stream {
	name := map[string]string{
		event.AccessRead:  "Read",
		event.AccessWrite: "Write",
		event.AccessEdit:  "Edit",
	}[access]
	return s.tool(name, &event.ToolMetadata{
		File: &event.FileOp{Path: path, Access: access},
	}, opts...)
}

func (s *stream) read(path string, opts ...option) *stream {
	return s.file(path, event.AccessRead, opts...)
}

func (s *stream) write(path string, opts ...option) *stream {
	return s.file(path, event.AccessWrite, opts...)
}

func (s *stream) edit(path string, opts ...option) *stream {
	return s.file(path, event.AccessEdit, opts...)
}

// readRange reads part of a file, which acquires something other than the file.
func (s *stream) readRange(path string, offset int, opts ...option) *stream {
	return s.tool("Read", &event.ToolMetadata{
		File: &event.FileOp{Path: path, Access: event.AccessRead, Offset: &offset},
	}, opts...)
}

func (s *stream) shell(digest string, opts ...option) *stream {
	return s.tool("Bash", &event.ToolMetadata{
		Shell: &event.ShellOp{CommandDigest: digest},
	}, opts...)
}

func (s *stream) search(digest, root string, opts ...option) *stream {
	return s.tool("Grep", &event.ToolMetadata{
		Search: &event.SearchOp{Kind: event.SearchContent, PatternDigest: digest, Root: root},
	}, opts...)
}

func (s *stream) subagent(kind string, opts ...option) *stream {
	return s.tool("Task", &event.ToolMetadata{
		Subagent: &event.SubagentOp{Type: kind},
	}, opts...)
}

// opaque is a tool the metadata allowlist does not cover, such as an MCP tool
// or NotebookEdit.
func (s *stream) opaque(name string, opts ...option) *stream {
	return s.tool(name, nil, opts...)
}

func (s *stream) sessionStart() *stream {
	s.now = s.now.Add(time.Second)
	s.events = append(s.events, event.Event{
		SchemaVersion: event.SchemaVersion,
		Type:          event.TypeSessionStart,
		Timestamp:     s.now,
		SessionID:     s.session,
		Session:       &event.Session{Source: "startup"},
	})
	return s
}

// measured is what telemetry recorded, keyed the way an invocation is
// identified: only within the turn and session that produced it.
type measured map[string]int64

func (m measured) lookup(session, turn, invocation string) (int64, bool) {
	v, ok := m[session+"|"+turn+"|"+invocation]
	return v, ok
}

func bytesFor(session, turn, invocation string, size int64) measured {
	return measured{session + "|" + turn + "|" + invocation: size}
}

func (m measured) and(other measured) measured {
	for k, v := range other {
		m[k] = v
	}
	return m
}

func profileOf(s *stream, m measured) activity.Profile {
	var lookup activity.ByteLookup
	if m != nil {
		lookup = m.lookup
	}
	a := activity.New(lookup)
	for _, ev := range s.events {
		a.Add(ev)
	}
	return a.Profile()
}

// pathAt returns the work reported for one path, failing when there is none.
func pathAt(t *testing.T, p activity.Profile, path string) activity.Path {
	t.Helper()

	for _, got := range p.Paths {
		if got.Path == path {
			return got
		}
	}
	t.Fatalf("no work reported at %q, only %v", path, paths(p))
	return activity.Path{}
}

func paths(p activity.Profile) []string {
	out := make([]string, 0, len(p.Paths))
	for _, path := range p.Paths {
		out = append(out, path.Path)
	}
	return out
}

func TestWorkIsAttributedToThePathItHappenedAt(t *testing.T) {
	t.Parallel()

	p := profileOf(newStream().
		read("/repo/a.go").
		read("/repo/a.go").
		edit("/repo/a.go").
		write("/repo/b.go"), nil)

	a := pathAt(t, p, "/repo/a.go")
	if a.Reads != 2 || a.Edits != 1 || a.Writes != 0 {
		t.Errorf("a.go = %d reads, %d edits, %d writes; want 2, 1, 0", a.Reads, a.Edits, a.Writes)
	}
	if a.Operations() != 3 {
		t.Errorf("a.go operations = %d, want 3", a.Operations())
	}
	if b := pathAt(t, p, "/repo/b.go"); b.Writes != 1 || b.Reads != 0 {
		t.Errorf("b.go = %d writes, %d reads; want 1, 0", b.Writes, b.Reads)
	}
}

// Two reads of one path are two operations and nothing more. Whether either
// repeated the other is a question for the profiler, which answers it under
// rules this analysis does not apply.
func TestRepeatedWorkIsCountedWithoutBeingJudged(t *testing.T) {
	t.Parallel()

	p := profileOf(newStream().read("/repo/a.go").read("/repo/a.go").read("/repo/a.go"), nil)

	if len(p.Paths) != 1 {
		t.Fatalf("paths = %v, want one", paths(p))
	}
	if got := pathAt(t, p, "/repo/a.go"); got.Reads != 3 {
		t.Errorf("reads = %d, want 3", got.Reads)
	}
}

// The whole point of the partition: every observed call lands in exactly one
// bucket, and the file bucket is exactly the work attributed to paths.
func TestEveryObservedCallIsCountedOnce(t *testing.T) {
	t.Parallel()

	p := profileOf(newStream().
		sessionStart().
		read("/repo/a.go").
		readRange("/repo/a.go", 40).
		read("/repo/gone.go", failed).
		edit("/repo/a.go").
		edit("/repo/a.go", failed).
		write("/repo/b.go").
		search("digest-1", "/repo").
		shell("digest-2").
		subagent("Explore").
		opaque("mcp__docs__lookup"), nil)

	c := p.Operations
	if c.Total != 10 {
		t.Errorf("Total = %d, want 10 tool calls and no session event", c.Total)
	}
	if sum := c.File + c.Search + c.Shell + c.Subagent + c.Unrecognized; sum != c.Total {
		t.Errorf("buckets sum to %d, want %d", sum, c.Total)
	}

	operations := 0
	for _, path := range p.Paths {
		operations += path.Operations()
	}
	if operations != c.File {
		t.Errorf("attributed operations = %d, want the file bucket's %d", operations, c.File)
	}
}

// Recognizing an operation is not the same as being able to say where it
// happened. Only a file operation names a path.
func TestRecognizedWorkIsNotAlwaysAttributable(t *testing.T) {
	t.Parallel()

	p := profileOf(newStream().
		shell("digest-1").
		search("digest-2", "/repo/internal").
		subagent("Explore"), nil)

	if len(p.Paths) != 0 {
		t.Errorf("paths = %v, want none: none of these operations named a file", paths(p))
	}
	c := p.Operations
	if c.Shell != 1 || c.Search != 1 || c.Subagent != 1 || c.Unrecognized != 0 {
		t.Errorf("composition = %+v, want one recognized call of each shape", c)
	}
}

// A search root says where an agent looked, not what it read. Treating it as a
// location would attribute work to a directory nobody opened.
func TestASearchRootIsNotAPath(t *testing.T) {
	t.Parallel()

	p := profileOf(newStream().search("digest-1", "/repo/internal/auth"), nil)

	if len(p.Paths) != 0 {
		t.Errorf("paths = %v, want none", paths(p))
	}
}

func TestUninterpretableWorkIsCountedAndNotGuessedAbout(t *testing.T) {
	t.Parallel()

	cases := map[string]*stream{
		"a tool outside the allowlist": newStream().opaque("NotebookEdit"),
		"an MCP tool":                  newStream().opaque("mcp__libraries__list_packages"),
		"a file operation naming no path": newStream().tool("Read", &event.ToolMetadata{
			File: &event.FileOp{Access: event.AccessRead},
		}),
		"an access this analysis does not know": newStream().tool("Read", &event.ToolMetadata{
			File: &event.FileOp{Path: "/repo/a.go", Access: "append"},
		}),
		// A shape the model gains later reaches an older profile as metadata
		// with nothing in it, which is exactly as unknown as no metadata.
		"a shape this analysis does not know": newStream().tool("Read", &event.ToolMetadata{}),
		// Nothing validates the outcome field, so a record can arrive without
		// one. It says nothing about what happened, which is not the same as
		// saying the call failed.
		"an outcome that was never established":  newStream().read("/repo/a.go", outcome("")),
		"an outcome this analysis does not know": newStream().read("/repo/a.go", outcome("blocked")),
	}

	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := profileOf(s, nil)

			if p.Operations.Unrecognized != 1 || p.Operations.File != 0 {
				t.Errorf("composition = %+v, want one unrecognized call and no file work", p.Operations)
			}
			if len(p.Paths) != 0 {
				t.Errorf("paths = %v, want none", paths(p))
			}
		})
	}
}

// A ranged read acquires part of a file, so it is counted apart from a read of
// the whole one. A path read only in ranges must not look unread.
func TestRangedReadsAreCountedApartFromWholeOnes(t *testing.T) {
	t.Parallel()

	p := profileOf(newStream().
		readRange("/repo/big.go", 0).
		readRange("/repo/big.go", 200).
		readRange("/repo/big.go", 400), nil)

	got := pathAt(t, p, "/repo/big.go")
	if got.Reads != 0 || got.RangedReads != 3 {
		t.Errorf("big.go = %d reads, %d ranged; want 0, 3", got.Reads, got.RangedReads)
	}
	if got.Operations() != 3 {
		t.Errorf("operations = %d, want 3", got.Operations())
	}
	if p.Reads != 3 {
		t.Errorf("profile reads = %d, want 3: a ranged read is still a read", p.Reads)
	}
}

// A failed read is not established to have delivered anything and a failed edit
// may have applied in part. Neither is a successful read or a modification, and
// neither may vanish.
func TestFailedOperationsAreCountedAsNeitherReadNorModification(t *testing.T) {
	t.Parallel()

	p := profileOf(newStream().
		read("/repo/a.go", failed).
		edit("/repo/a.go", failed).
		write("/repo/a.go", failed).
		readRange("/repo/a.go", 10, failed), nil)

	got := pathAt(t, p, "/repo/a.go")
	if got.Reads != 0 || got.RangedReads != 0 || got.Writes != 0 || got.Edits != 0 {
		t.Errorf("%+v: a failed operation was counted as work that succeeded", got)
	}
	if got.Failed != 4 || got.Operations() != 4 {
		t.Errorf("failed = %d, operations = %d; want 4, 4", got.Failed, got.Operations())
	}
	if p.Reads != 0 {
		t.Errorf("profile reads = %d, want 0", p.Reads)
	}
}

// Failed means the agent reported a failure. An outcome that was never
// established says nothing, and reading it as failure would infer the worst
// from missing evidence.
func TestFailedMeansAnObservedFailureAndNotAnAbsentSuccess(t *testing.T) {
	t.Parallel()

	for _, state := range []string{"", "blocked", "denied", "timeout", "SUCCESS"} {
		t.Run("outcome "+state, func(t *testing.T) {
			t.Parallel()

			p := profileOf(newStream().read("/repo/a.go", outcome(state)), nil)

			if len(p.Paths) != 0 {
				t.Errorf("paths = %v, want none: Axiom cannot say this operation did anything", paths(p))
			}
			if p.Operations.Unrecognized != 1 || p.Operations.File != 0 {
				t.Errorf("composition = %+v, want the call counted as uninterpretable", p.Operations)
			}
		})
	}

	// The one state that does mean failure still counts as one.
	p := profileOf(newStream().read("/repo/a.go", failed), nil)
	if got := pathAt(t, p, "/repo/a.go"); got.Failed != 1 {
		t.Errorf("failed = %d, want 1 for the outcome the agent reported", got.Failed)
	}
}

// Where and how long are properties of the call, not of its outcome.
func TestAFailedOperationStillHappenedSomewhereAndTookTime(t *testing.T) {
	t.Parallel()

	p := profileOf(newStream().read("/repo/a.go", failed, took(120)), nil)

	got := pathAt(t, p, "/repo/a.go")
	if got.ObservedTime == nil || *got.ObservedTime != 120*time.Millisecond {
		t.Errorf("ObservedTime = %v, want 120ms", got.ObservedTime)
	}
	if got.Turns == nil || *got.Turns != 1 {
		t.Errorf("Turns = %v, want 1", got.Turns)
	}
}

// A turn identifier is the agent's own and means nothing outside its session,
// so two sessions using the same one are two turns.
func TestTurnsAreCountedWithTheirSession(t *testing.T) {
	t.Parallel()

	p := profileOf(newStream().
		read("/repo/a.go").
		in("session-1", "turn-2").read("/repo/a.go").
		in("session-2", "turn-1").read("/repo/a.go"), nil)

	got := pathAt(t, p, "/repo/a.go")
	if got.Turns == nil || *got.Turns != 3 {
		t.Errorf("Turns = %v, want 3", got.Turns)
	}
}

func TestTurnsAreUnknownWhenAnOperationNamedNone(t *testing.T) {
	t.Parallel()

	p := profileOf(newStream().
		read("/repo/a.go").
		in("session-1", "").read("/repo/a.go"), nil)

	if got := pathAt(t, p, "/repo/a.go"); got.Turns != nil {
		t.Errorf("Turns = %d, want unknown rather than an undercount", *got.Turns)
	}
}

func TestTimeIsWithheldWhenAnOperationReportedNone(t *testing.T) {
	t.Parallel()

	p := profileOf(newStream().
		read("/repo/a.go", took(90)).
		read("/repo/a.go", untimed), nil)

	if got := pathAt(t, p, "/repo/a.go"); got.ObservedTime != nil {
		t.Errorf("ObservedTime = %v, want nil rather than a partial sum", got.ObservedTime)
	}
}

func TestReadBytesTotalTheReadsOfOnePath(t *testing.T) {
	t.Parallel()

	p := profileOf(newStream().
		read("/repo/a.go").
		readRange("/repo/a.go", 100),
		bytesFor("session-1", "turn-1", "call-1", 7696).
			and(bytesFor("session-1", "turn-1", "call-2", 304)))

	got := pathAt(t, p, "/repo/a.go")
	if got.ReadBytes == nil || *got.ReadBytes != 8000 {
		t.Errorf("ReadBytes = %v, want 8000 over both reads", got.ReadBytes)
	}
	if p.Reads != 2 || p.ReadsMeasured != 2 {
		t.Errorf("reads measured = %d of %d, want 2 of 2", p.ReadsMeasured, p.Reads)
	}
}

// A partial sum understates the total while looking exactly like a complete
// one, so anything missing withholds the whole value.
func TestReadBytesAreAllOrNothing(t *testing.T) {
	t.Parallel()

	cases := map[string]measured{
		"nothing was measured":        {},
		"only one read was measured":  bytesFor("session-1", "turn-1", "call-1", 7696),
		"the other read was measured": bytesFor("session-1", "turn-1", "call-2", 93),
		"the measurement names a turn the reads did not happen in": bytesFor("session-1", "turn-9", "call-1", 7696).
			and(bytesFor("session-1", "turn-9", "call-2", 93)),
		"the measurements belong to another session": bytesFor("session-9", "turn-1", "call-1", 7696).
			and(bytesFor("session-9", "turn-1", "call-2", 93)),
	}

	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := profileOf(newStream().read("/repo/a.go").read("/repo/a.go"), m)

			if got := pathAt(t, p, "/repo/a.go"); got.ReadBytes != nil {
				t.Errorf("ReadBytes = %d, want nil: an incomplete total is worse than none", *got.ReadBytes)
			}
			if p.Reads != 2 {
				t.Errorf("reads = %d, want 2", p.Reads)
			}
		})
	}
}

func TestAReadWithNoIdentityCannotBeMeasured(t *testing.T) {
	t.Parallel()

	p := profileOf(newStream().read("/repo/a.go", anonymous),
		bytesFor("session-1", "turn-1", "", 7696))

	got := pathAt(t, p, "/repo/a.go")
	if got.ReadBytes != nil {
		t.Errorf("ReadBytes = %d, want nil: nothing identified that call", *got.ReadBytes)
	}
	if p.ReadsMeasured != 0 {
		t.Errorf("ReadsMeasured = %d, want 0", p.ReadsMeasured)
	}
}

// What an edit returns is the agent confirming its own change, not repository
// content, so it is not part of what reads returned.
func TestOnlyReadsAreMeasured(t *testing.T) {
	t.Parallel()

	p := profileOf(newStream().edit("/repo/a.go").write("/repo/b.go"),
		bytesFor("session-1", "turn-1", "call-1", 400).
			and(bytesFor("session-1", "turn-1", "call-2", 900)))

	if got := pathAt(t, p, "/repo/a.go"); got.ReadBytes != nil {
		t.Errorf("ReadBytes = %d, want nil for a path that was never read", *got.ReadBytes)
	}
	if p.Reads != 0 || p.ReadsMeasured != 0 {
		t.Errorf("reads measured = %d of %d, want 0 of 0", p.ReadsMeasured, p.Reads)
	}
}

func TestAPathWithNoReadsReportsNoBytes(t *testing.T) {
	t.Parallel()

	p := profileOf(newStream().write("/repo/new.go"), measured{})

	if got := pathAt(t, p, "/repo/new.go"); got.ReadBytes != nil {
		t.Errorf("ReadBytes = %d, want nil rather than a measured zero", *got.ReadBytes)
	}
}

func TestWithoutTelemetryEveryTotalIsUnknown(t *testing.T) {
	t.Parallel()

	p := profileOf(newStream().read("/repo/a.go"), nil)

	if got := pathAt(t, p, "/repo/a.go"); got.ReadBytes != nil {
		t.Errorf("ReadBytes = %d, want nil when nothing was measured", *got.ReadBytes)
	}
}

// Identity is the string the agent named. Resolving one form into another would
// mean trusting a working directory Axiom did not observe at that moment.
func TestPathIdentityIsExactlyWhatTheAgentNamed(t *testing.T) {
	t.Parallel()

	p := profileOf(newStream().
		read("/repo/a.go").
		read("a.go").
		read("/repo/./a.go"), nil)

	if len(p.Paths) != 3 {
		t.Errorf("paths = %v, want three: no path was normalized into another", paths(p))
	}
}

// A nested agent's work happened at the path like anyone else's. Summing it
// there says where the work was, never that any of it repeated.
func TestNestedAgentWorkIsCountedAtThePath(t *testing.T) {
	t.Parallel()

	p := profileOf(newStream().
		read("/repo/a.go").
		inSubagent("agent-1").read("/repo/a.go").
		inSubagent("agent-2").read("/repo/a.go"), nil)

	if len(p.Paths) != 1 {
		t.Fatalf("paths = %v, want one", paths(p))
	}
	if got := pathAt(t, p, "/repo/a.go"); got.Reads != 3 {
		t.Errorf("reads = %d, want 3", got.Reads)
	}
}

// The busiest path first, and ties settled so that two runs over one log cannot
// disagree about the order.
func TestPathsAreOrderedByWorkThenByPath(t *testing.T) {
	t.Parallel()

	p := profileOf(newStream().
		read("/repo/b.go").
		read("/repo/a.go").
		read("/repo/a.go").
		read("/repo/c.go").
		read("/repo/quiet.go", failed), nil)

	want := []string{"/repo/a.go", "/repo/b.go", "/repo/c.go", "/repo/quiet.go"}
	got := paths(p)
	if len(got) != len(want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("paths = %v, want %v", got, want)
		}
	}
}

func TestEventsThatAreNotToolCallsDescribeNoWork(t *testing.T) {
	t.Parallel()

	// A tool call event carrying no call at all describes nothing either. It
	// should not be possible, and counting it would report work that has no
	// shape.
	empty := newStream().sessionStart().sessionStart()
	empty.events = append(empty.events, event.Event{
		SchemaVersion: event.SchemaVersion,
		Type:          event.TypeToolCall,
		SessionID:     "session-1",
	})

	p := profileOf(empty, nil)

	if p.Operations.Total != 0 || len(p.Paths) != 0 {
		t.Errorf("composition = %+v with paths %v, want nothing", p.Operations, paths(p))
	}
}

// Profiling twice must describe the same log the same way, and must not consume
// what was added.
func TestProfilingTwiceReportsTheSameWork(t *testing.T) {
	t.Parallel()

	s := newStream().read("/repo/a.go").edit("/repo/a.go")
	a := activity.New(nil)
	for _, ev := range s.events {
		a.Add(ev)
	}

	first, second := a.Profile(), a.Profile()
	if first.Operations != second.Operations {
		t.Errorf("composition changed between reports: %+v then %+v", first.Operations, second.Operations)
	}
	if len(first.Paths) != len(second.Paths) || first.Paths[0].Operations() != second.Paths[0].Operations() {
		t.Errorf("paths changed between reports: %v then %v", paths(first), paths(second))
	}
}
