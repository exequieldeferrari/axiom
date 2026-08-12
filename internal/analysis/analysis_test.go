package analysis

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/profiler"
	"github.com/exequieldeferrari/axiom/internal/store"
)

var base = time.Date(2026, 8, 12, 18, 0, 0, 0, time.UTC)

func at(offset time.Duration) time.Time { return base.Add(offset) }

func seed(t *testing.T, events ...event.Event) string {
	t.Helper()

	dir := t.TempDir()
	s, err := store.OpenEvents(dir)
	if err != nil {
		t.Fatalf("OpenEvents: %v", err)
	}
	for _, ev := range events {
		if err := s.Append(ev); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	return dir
}

func call(session, turn, subagent string, when time.Time, tool event.ToolCall) event.Event {
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

func read(path string) event.ToolCall {
	return event.ToolCall{
		Name: "Read", Outcome: event.OutcomeSuccess,
		Metadata: &event.ToolMetadata{
			File: &event.FileOp{Path: path, Access: event.AccessRead},
		},
	}
}

func analyze(t *testing.T, dir string, opts Options) Log {
	t.Helper()

	log, err := Analyze(dir, opts)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	return log
}

// The composition is a partition of the analyzed calls, so it has to total
// exactly what the profiler counted. Two counts of one thing that can disagree
// is the defect internal/work exists to prevent, and the seam must not
// reintroduce it.
func TestCompositionTotalsTheRecordedToolCalls(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		call("s1", "t1", "", at(0), read("/repo/a.go")),
		call("s1", "t1", "", at(time.Second), event.ToolCall{
			Name: "Bash", Outcome: event.OutcomeFailure,
			Metadata: &event.ToolMetadata{Shell: &event.ShellOp{CommandDigest: "d"}},
		}),
		// A tool this version cannot describe, and a ranged read.
		call("s1", "t1", "", at(2*time.Second), event.ToolCall{Name: "ScheduleWakeup", Outcome: event.OutcomeSuccess}),
		call("s1", "", "", at(3*time.Second), event.ToolCall{
			Name: "Read", Outcome: event.OutcomeSuccess,
			Metadata: &event.ToolMetadata{
				File: &event.FileOp{Path: "/repo/b.go", Access: event.AccessRead, Limit: limit(20)},
			},
		}),
		// A record carrying no call at all, which is not a tool call.
		event.Event{
			SchemaVersion: event.SchemaVersion, Agent: "claude-code",
			Type: event.TypeToolCall, Timestamp: at(4 * time.Second), SessionID: "s1",
		},
	)

	log := analyze(t, dir, Options{})

	if got, want := log.Composition.Total(), log.Findings.ToolCalls; got != want {
		t.Errorf("composition totals %d calls, profiler counted %d", got, want)
	}
	if log.Composition.Uninterpreted != 1 {
		t.Errorf("Uninterpreted = %d, want the one call with no metadata", log.Composition.Uninterpreted)
	}
	if log.Composition.RangedReads != 1 {
		t.Errorf("RangedReads = %d, want 1", log.Composition.RangedReads)
	}
	if log.Composition.Shell != 1 {
		t.Errorf("Shell = %d, want 1", log.Composition.Shell)
	}
}

func limit(n int) *int { return &n }

// Selection is exact. A prefix would analyze a different session that happened
// to start the same way, and report it under the identifier that was asked for.
func TestSelectionMatchesOneSessionExactly(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		call("session-abc", "t1", "", at(0), read("/repo/a.go")),
		call("session-abcdef", "t1", "", at(time.Second), read("/repo/b.go")),
	)

	log := analyze(t, dir, Options{Session: "session-abc"})

	if log.Records != 1 {
		t.Errorf("Records = %d, want only the exactly matching session", log.Records)
	}
	if log.Findings.Sessions != 1 {
		t.Errorf("Sessions = %d, want 1", log.Findings.Sessions)
	}
}

// A selection that matched nothing is reported as an empty analysis and never
// as an error: what to say about it is the caller's decision.
func TestSelectionThatMatchesNothingAnalyzesNothing(t *testing.T) {
	t.Parallel()

	dir := seed(t, call("s1", "t1", "", at(0), read("/repo/a.go")))

	log := analyze(t, dir, Options{Session: "no-such-session"})

	if log.Records != 0 {
		t.Errorf("Records = %d, want 0", log.Records)
	}
}

// Skipped records describe the whole log. A record Axiom could not decode
// cannot be attributed to a session, so narrowing them to a selection would
// hide exactly the evidence that says what was lost.
func TestSkippedRecordsSurviveSelection(t *testing.T) {
	t.Parallel()

	dir := seed(t, call("s1", "t1", "", at(0), read("/repo/a.go")))
	path := filepath.Join(dir, store.EventsFile)
	log, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if err := os.WriteFile(path, append(log, []byte("{not json}\n")...), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}

	scoped := analyze(t, dir, Options{Session: "s1"})

	if got := scoped.Stats.Skipped(); got != 1 {
		t.Errorf("Skipped = %d, want the malformed record to survive selection", got)
	}
}

// An absent log is the store's error, unchanged, so that a command can decide
// how to report it.
func TestAnalyzeReportsAnAbsentLog(t *testing.T) {
	t.Parallel()

	_, err := Analyze(t.TempDir(), Options{})

	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v, want fs.ErrNotExist", err)
	}
}

// Having no telemetry is the ordinary state of a machine where no receiver has
// run, and is not an error. Presence is a fact about the file: absent
// measurement is unrecorded consumption, never consumption of zero.
func TestUsageAbsenceIsNotAFailure(t *testing.T) {
	t.Parallel()

	log := analyze(t, seed(t, call("s1", "t1", "", at(0), read("/repo/a.go"))), Options{})

	if log.Usage.Present {
		t.Error("Present = true, want false when no usage log exists")
	}
	if log.Usage.Unreadable != nil {
		t.Errorf("Unreadable = %v, want nil when the log is simply absent", log.Usage.Unreadable)
	}
	if log.Usage.Index == nil {
		t.Error("Index = nil, want an empty index")
	}
}

// Failing to open the log is reported for the same reason as failing to read
// it, and is distinct from the log being absent.
func TestLoadUsageSeparatesAnUnopenableLogFromAnAbsentOne(t *testing.T) {
	t.Parallel()

	// A data directory that is not a directory: the log is not absent, it
	// cannot be opened.
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	unopenable := loadUsage(blocked)
	if unopenable.Unreadable == nil {
		t.Error("Unreadable = nil, want an error when the log cannot be opened")
	}
	measured := unopenable.Index.Measure([]profiler.Finding{{
		SessionID: "session-1",
		Calls: []profiler.Call{
			{TurnID: "turn-1", InvocationID: "call-1"},
			{TurnID: "turn-1", InvocationID: "call-2"},
		},
	}})
	if got := measured[0].RedundantBytes; got != nil {
		t.Errorf("RedundantBytes = %d, want nil when nothing could be read", *got)
	}

	if got := loadUsage(t.TempDir()).Unreadable; got != nil {
		t.Errorf("Unreadable = %v, want nil when the log is simply absent", got)
	}
}

// A usage log that exists is present even when it holds nothing: the receiver
// ran, and that is what presence says.
func TestUsagePresenceIsAboutTheFile(t *testing.T) {
	t.Parallel()

	dir := seed(t, call("s1", "t1", "", at(0), read("/repo/a.go")))
	if err := os.WriteFile(filepath.Join(dir, store.UsageFile), nil, 0o600); err != nil {
		t.Fatalf("write usage log: %v", err)
	}

	log := analyze(t, dir, Options{})

	if !log.Usage.Present {
		t.Error("Present = false, want true when a usage log exists")
	}
}
