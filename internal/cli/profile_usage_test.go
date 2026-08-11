package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/profiler"
	"github.com/exequieldeferrari/axiom/internal/store"
)

// The shapes here mirror a real paired capture: a file read three times in one
// turn, where the agent returned the whole file once and a short notice for
// each repeat. The identifiers and paths are invented.
const (
	firstReadBytes  int64 = 7696
	repeatReadBytes int64 = 93
)

// readCall is a read the agent identified, so a measurement can be attached to
// this exact occurrence.
func readCall(session, turn, invocation, path string, when time.Time) event.Event {
	ev := readEvent(session, path, when, 2)
	ev.TurnID = turn
	ev.Tool.InvocationID = invocation
	return ev
}

// seedUsage writes usage records beside an existing event log.
func seedUsage(t *testing.T, dir string, records ...event.Usage) {
	t.Helper()

	s, err := store.OpenUsage(dir)
	if err != nil {
		t.Fatalf("OpenUsage: %v", err)
	}
	for _, u := range records {
		if err := s.Append(u); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
}

func measurement(session, turn, invocation string, size *int64) event.Usage {
	return event.Usage{
		SchemaVersion: event.SchemaVersion,
		Agent:         "claude-code",
		Kind:          event.UsageToolResult,
		Timestamp:     at(0),
		SessionID:     session,
		TurnID:        turn,
		InvocationID:  invocation,
		ToolName:      "Read",
		ResultBytes:   size,
	}
}

func bytesOf(n int64) *int64 { return &n }

// repeatedReadWithTelemetry seeds both streams for one file read three times.
func repeatedReadWithTelemetry(t *testing.T, usage ...event.Usage) string {
	t.Helper()

	dir := seed(t,
		readCall("session-1", "turn-1", "call-1", "/repo/notes.txt", at(0)),
		readCall("session-1", "turn-1", "call-2", "/repo/notes.txt", at(time.Second)),
		readCall("session-1", "turn-1", "call-3", "/repo/notes.txt", at(2*time.Second)),
	)
	seedUsage(t, dir, usage...)
	return dir
}

// The first read did the work, so its bytes are not part of what repeating it
// cost.
func TestProfileMeasuresOnlyTheRepeatedCalls(t *testing.T) {
	t.Parallel()

	dir := repeatedReadWithTelemetry(t,
		measurement("session-1", "turn-1", "call-1", bytesOf(firstReadBytes)),
		measurement("session-1", "turn-1", "call-2", bytesOf(repeatReadBytes)),
		measurement("session-1", "turn-1", "call-3", bytesOf(repeatReadBytes)),
	)

	out := profileOutput(t, dir)

	if !strings.Contains(out, "Redundant tool output             186 B") {
		t.Errorf("the measured redundant output is missing or wrong:\n%s", out)
	}
	if strings.Contains(out, "7.5 KB") {
		t.Errorf("the first read's bytes were attributed to the redundancy:\n%s", out)
	}
	if !strings.Contains(out, "It is a count of bytes, not tokens and not\ncost") {
		t.Errorf("the measurement is not explained:\n%s", out)
	}
}

func TestProfileReportsLargeRedundantOutput(t *testing.T) {
	t.Parallel()

	dir := repeatedReadWithTelemetry(t,
		measurement("session-1", "turn-1", "call-1", bytesOf(firstReadBytes)),
		measurement("session-1", "turn-1", "call-2", bytesOf(firstReadBytes)),
		measurement("session-1", "turn-1", "call-3", bytesOf(firstReadBytes)),
	)

	out := profileOutput(t, dir)

	if !strings.Contains(out, "Redundant tool output             15.0 KB") {
		t.Errorf("the measured redundant output is missing or wrong:\n%s", out)
	}
}

// Telemetry exists only while a receiver is running, so its absence is the
// ordinary case and must change nothing.
func TestProfileOmitsMeasurementWithoutTelemetry(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		readCall("session-1", "turn-1", "call-1", "/repo/notes.txt", at(0)),
		readCall("session-1", "turn-1", "call-2", "/repo/notes.txt", at(time.Second)),
	)

	out := profileOutput(t, dir)

	if !strings.Contains(out, "Potentially redundant reads       1") {
		t.Fatalf("the finding is missing:\n%s", out)
	}
	if strings.Contains(out, "Redundant tool output") {
		t.Errorf("an unmeasured finding reported a measurement:\n%s", out)
	}
	if strings.Contains(out, "count of bytes") {
		t.Errorf("the measurement is explained where none was made:\n%s", out)
	}
}

// Absence of evidence is never rendered as a value, however it arises.
func TestProfileOmitsIncompleteMeasurements(t *testing.T) {
	t.Parallel()

	cases := map[string][]event.Usage{
		"a repeat was never measured": {
			measurement("session-1", "turn-1", "call-1", bytesOf(firstReadBytes)),
			measurement("session-1", "turn-1", "call-2", bytesOf(repeatReadBytes)),
		},
		"a repeat was measured twice": {
			measurement("session-1", "turn-1", "call-2", bytesOf(repeatReadBytes)),
			measurement("session-1", "turn-1", "call-2", bytesOf(repeatReadBytes)),
			measurement("session-1", "turn-1", "call-3", bytesOf(repeatReadBytes)),
		},
		"a measurement reported no size": {
			measurement("session-1", "turn-1", "call-2", nil),
			measurement("session-1", "turn-1", "call-3", bytesOf(repeatReadBytes)),
		},
		"the measurements belong to another session": {
			measurement("session-9", "turn-1", "call-2", bytesOf(repeatReadBytes)),
			measurement("session-9", "turn-1", "call-3", bytesOf(repeatReadBytes)),
		},
	}

	for name, usage := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			out := profileOutput(t, repeatedReadWithTelemetry(t, usage...))

			if !strings.Contains(out, "Potentially redundant reads       2") {
				t.Fatalf("the finding is missing:\n%s", out)
			}
			if strings.Contains(out, "Redundant tool output") {
				t.Errorf("an incomplete measurement was reported:\n%s", out)
			}
		})
	}
}

// Having no telemetry is the ordinary state of a machine where no receiver has
// run. Explaining it every time would make the normal case look like a fault.
func TestProfileIsSilentWhenThereIsNoUsageLog(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		readCall("session-1", "turn-1", "call-1", "/repo/notes.txt", at(0)),
		readCall("session-1", "turn-1", "call-2", "/repo/notes.txt", at(time.Second)),
	)

	out := profileOutput(t, dir)

	if strings.Contains(out, "Warning") {
		t.Errorf("an absent usage log was reported as a problem:\n%s", out)
	}
	if got := loadUsage(dir).unreadable; got != nil {
		t.Errorf("unreadable = %v, want nil when there is no usage log", got)
	}
}

// A usage log that exists and cannot be read is not the same as none at all.
// Staying quiet would make missing measurements look like an absence of
// redundant output rather than an absence of evidence.
func TestProfileWarnsWhenTheUsageLogCannotBeRead(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		readCall("session-1", "turn-1", "call-1", "/repo/notes.txt", at(0)),
		readCall("session-1", "turn-1", "call-2", "/repo/notes.txt", at(time.Second)),
		readCall("session-1", "turn-1", "call-3", "/repo/notes.txt", at(2*time.Second)),
	)
	// A directory in the log's place opens and then fails to read, which
	// needs no permission change: chmod behaves differently under root and
	// cannot be relied on in CI.
	if err := os.Mkdir(filepath.Join(dir, store.UsageFile), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	out := profileOutput(t, dir)

	if !strings.Contains(out, "the usage log could not be read") {
		t.Errorf("an unreadable usage log was not reported:\n%s", out)
	}
	if !strings.Contains(out, "findings are unmeasured") {
		t.Errorf("the consequence was not explained:\n%s", out)
	}
	if !strings.Contains(out, "Potentially redundant reads       2") {
		t.Errorf("profiling did not survive an unreadable usage log:\n%s", out)
	}
	if strings.Contains(out, "Redundant tool output") {
		t.Errorf("a measurement was reported from a log that could not be read:\n%s", out)
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
	if unopenable.unreadable == nil {
		t.Error("unreadable = nil, want an error when the log cannot be opened")
	}
	if got := unopenable.index.Measure([]profiler.Finding{measurableFinding()})[0].RedundantBytes; got != nil {
		t.Errorf("RedundantBytes = %d, want nil when nothing could be read", *got)
	}

	if got := loadUsage(t.TempDir()).unreadable; got != nil {
		t.Errorf("unreadable = %v, want nil when the log is simply absent", got)
	}
}

// measurableFinding is a finding that would be measured if the index held
// anything for it.
func measurableFinding() profiler.Finding {
	return profiler.Finding{
		SessionID: "session-1",
		Calls: []profiler.Call{
			{TurnID: "turn-1", InvocationID: "call-1"},
			{TurnID: "turn-1", InvocationID: "call-2"},
		},
	}
}

// A measurement Axiom cannot read costs a measurement, not a finding.
func TestProfileSurfacesSkippedUsageRecords(t *testing.T) {
	t.Parallel()

	dir := repeatedReadWithTelemetry(t,
		measurement("session-1", "turn-1", "call-2", bytesOf(repeatReadBytes)),
		measurement("session-1", "turn-1", "call-3", bytesOf(repeatReadBytes)),
	)
	path := filepath.Join(dir, store.UsageFile)

	log, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if err := os.WriteFile(path, append(log, []byte("{not json}\n")...), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}

	out := profileOutput(t, dir)

	for _, want := range []string{
		"Warning: 1 usage record skipped",
		"1 malformed",
		"some measurements are missing",
		"Repeated file read",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

func TestProfileDoesNotModifyTheUsageLog(t *testing.T) {
	t.Parallel()

	dir := repeatedReadWithTelemetry(t,
		measurement("session-1", "turn-1", "call-2", bytesOf(repeatReadBytes)),
		measurement("session-1", "turn-1", "call-3", bytesOf(repeatReadBytes)),
	)
	path := filepath.Join(dir, store.UsageFile)

	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	profileOutput(t, dir)

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Error("profiling touched the usage log")
	}
}
