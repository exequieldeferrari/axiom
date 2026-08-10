package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/store"
)

// seed writes events to a fresh data directory and returns it.
func seed(t *testing.T, events ...event.Event) string {
	t.Helper()

	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, ev := range events {
		if err := s.Append(ev); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	return dir
}

func profileOutput(t *testing.T, dir string) string {
	t.Helper()

	var out bytes.Buffer
	if err := profileLog(dir, &out); err != nil {
		t.Fatalf("profileLog: %v", err)
	}
	return out.String()
}

var base = time.Date(2026, 8, 10, 20, 25, 4, 0, time.UTC)

func at(offset time.Duration) time.Time { return base.Add(offset) }

func ms(v int64) *int64 { return &v }

func readEvent(session, path string, when time.Time, duration int64) event.Event {
	return event.Event{
		SchemaVersion: event.SchemaVersion,
		Agent:         "claude-code",
		Type:          event.TypeToolCall,
		Timestamp:     when,
		SessionID:     session,
		Tool: &event.ToolCall{
			Name:       "Read",
			Outcome:    event.OutcomeSuccess,
			DurationMS: ms(duration),
			Metadata: &event.ToolMetadata{
				File: &event.FileOp{Path: path, Access: event.AccessRead},
			},
		},
	}
}

func shellEvent(session, digest string, when time.Time, duration int64) event.Event {
	return event.Event{
		SchemaVersion: event.SchemaVersion,
		Agent:         "claude-code",
		Type:          event.TypeToolCall,
		Timestamp:     when,
		SessionID:     session,
		Tool: &event.ToolCall{
			Name:       "Bash",
			Outcome:    event.OutcomeSuccess,
			DurationMS: ms(duration),
			Metadata: &event.ToolMetadata{
				Shell: &event.ShellOp{CommandDigest: digest},
			},
		},
	}
}

func TestProfileReportsMissingLog(t *testing.T) {
	t.Parallel()

	out := profileOutput(t, t.TempDir())

	if !strings.Contains(out, "No events recorded yet") {
		t.Errorf("output does not explain the empty state:\n%s", out)
	}
}

// A session with nothing to report is a result, not a failure.
func TestProfileReportsCleanSession(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		readEvent("session-1", "/repo/a.go", at(0), 4),
		readEvent("session-1", "/repo/b.go", at(time.Second), 3),
		readEvent("session-2", "/repo/a.go", at(time.Minute), 5),
	)

	out := profileOutput(t, dir)

	for _, want := range []string{
		"Axiom Profile",
		"Events              3",
		"Sessions analyzed   2",
		"Tool calls          3",
		"No high-confidence redundant work detected.",
		"scoped to a single session",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

func TestProfileReportsRepeatedShell(t *testing.T) {
	t.Parallel()

	const (
		digest  = "3f1c0a9e77b46d2ab5c8e10f2d4a6b8c9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b"
		session = "7b4d3ab1-b1f0-4f0e-9c1a-0a1b2c3d4e5f"
	)
	dir := seed(t,
		shellEvent(session, digest, at(0), 300),
		shellEvent(session, digest, at(4*time.Second), 340),
	)

	out := profileOutput(t, dir)

	for _, want := range []string{
		"HIGH",
		"Repeated shell operation",
		"session 7b4d3ab1",
		"Executed 2 times, with only read-only operations in between",
		"Potentially redundant executions  1",
		"Repeated-call tool time           340ms",
		"Command digest                    3f1c0a9e77b4…",
		"2026-08-10 20:25:04 → 20:25:08 UTC",
		"1 finding.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}

	// The digest is shortened for display and the command itself was never
	// recorded, so neither can be read out of the report.
	if strings.Contains(out, digest) {
		t.Errorf("the full digest was printed:\n%s", out)
	}
}

func TestProfileReportsRepeatedRead(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		readEvent("session-1", "/repo/internal/store/store.go", at(0), 6),
		readEvent("session-1", "/repo/internal/store/store.go", at(time.Minute), 5),
	)

	out := profileOutput(t, dir)

	for _, want := range []string{
		"Repeated file read",
		"Read 2 times, with no agent modification observed in between",
		"Potentially redundant reads       1",
		"File                              /repo/internal/store/store.go",
		"not\ncounting the first",
		"measures nothing about context, tokens, or cost",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

// Two contexts repeating the same file must not print as two identical
// findings, which would read as a duplicate rather than as separate evidence.
func TestProfileAttributesFindingsToTheirContext(t *testing.T) {
	t.Parallel()

	const session = "7b4d3ab1-b1f0-4f0e-9c1a-0a1b2c3d4e5f"
	nested := readEvent(session, "/repo/a.go", at(2*time.Second), 4)
	nested.SubagentID = "sub-1-explore"
	nestedAgain := readEvent(session, "/repo/a.go", at(3*time.Second), 4)
	nestedAgain.SubagentID = "sub-1-explore"

	out := profileOutput(t, seed(t,
		readEvent(session, "/repo/a.go", at(0), 4),
		readEvent(session, "/repo/a.go", at(time.Second), 4),
		nested,
		nestedAgain,
	))

	if got := strings.Count(out, "Repeated file read"); got != 2 {
		t.Fatalf("got %d findings, want one per context:\n%s", got, out)
	}
	// A subagent identifier is printed whole: shortening it like a session UUID
	// could cut two distinct subagents down to the same prefix.
	if !strings.Contains(out, "session 7b4d3ab1 · subagent sub-1-explore") {
		t.Errorf("the subagent's work is not attributed to it:\n%s", out)
	}
	// The session's own finding stays unqualified.
	if !strings.Contains(out, "session 7b4d3ab1\n") {
		t.Errorf("the session's own work gained a subagent:\n%s", out)
	}
}

func TestProfileReportsMissingDuration(t *testing.T) {
	t.Parallel()

	first := readEvent("session-1", "/repo/a.go", at(0), 4)
	second := readEvent("session-1", "/repo/a.go", at(time.Second), 0)
	second.Tool.DurationMS = nil

	out := profileOutput(t, seed(t, first, second))

	if !strings.Contains(out, "Repeated-call tool time           not reported") {
		t.Errorf("an unknown duration was not reported as unknown:\n%s", out)
	}
}

// Corruption has to be visible: a dropped write could be the reason a
// legitimate re-read looks redundant.
func TestProfileSurfacesSkippedRecords(t *testing.T) {
	t.Parallel()

	dir := seed(t, readEvent("session-1", "/repo/a.go", at(0), 4))
	path := filepath.Join(dir, store.FileName)

	log, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	damaged := append(log, []byte("{not json}\n{\"schema_version\":9}\ntruncated")...)
	if err := os.WriteFile(path, damaged, 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}

	out := profileOutput(t, dir)

	for _, want := range []string{
		"Warning: 3 records skipped",
		"1 malformed",
		"1 truncated",
		"1 from a newer schema",
		"findings may be incomplete",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

func TestProfileDoesNotModifyTheLog(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		readEvent("session-1", "/repo/a.go", at(0), 4),
		readEvent("session-1", "/repo/a.go", at(time.Second), 4),
	)
	path := filepath.Join(dir, store.FileName)

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	profileOutput(t, dir)

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Error("profiling changed the event log")
	}
	newInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !newInfo.ModTime().Equal(info.ModTime()) {
		t.Error("profiling touched the event log")
	}
}

func TestRunProfileUsesTheDataDirectory(t *testing.T) {
	dir := seed(t, readEvent("session-1", "/repo/a.go", at(0), 4))
	t.Setenv("AXIOM_DATA_DIR", dir)

	var out bytes.Buffer
	if err := runProfile(nil, &out); err != nil {
		t.Fatalf("runProfile: %v", err)
	}
	if !strings.Contains(out.String(), "Tool calls          1") {
		t.Errorf("runProfile did not read the configured data directory:\n%s", out.String())
	}
}

func TestRunDispatchesProfile(t *testing.T) {
	dir := seed(t,
		readEvent("session-1", "/repo/a.go", at(0), 4),
		readEvent("session-1", "/repo/a.go", at(time.Second), 4),
	)
	t.Setenv("AXIOM_DATA_DIR", dir)

	var stdout, stderr bytes.Buffer
	if err := Run([]string{"axiom", "profile"}, &stdout, &stderr); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
	if !strings.Contains(stdout.String(), "Repeated file read") {
		t.Errorf("profile output did not reach stdout:\n%s", stdout.String())
	}
}

func TestRunProfileRejectsArguments(t *testing.T) {
	t.Parallel()

	err := runProfile([]string{"yesterday"}, &bytes.Buffer{})

	if !IsUsage(err) {
		t.Fatalf("error = %v, want a usage error", err)
	}
}

func TestWindowSpanningDays(t *testing.T) {
	t.Parallel()

	got := window(
		time.Date(2026, 8, 10, 23, 59, 0, 0, time.UTC),
		time.Date(2026, 8, 11, 0, 1, 0, 0, time.UTC),
	)

	want := "2026-08-10 23:59:00 → 2026-08-11 00:01:00 UTC"
	if got != want {
		t.Errorf("window = %q, want %q", got, want)
	}
}
