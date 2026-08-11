package store_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/store"
)

func sampleUsage(session string) event.Usage {
	bytes := int64(43)
	return event.Usage{
		SchemaVersion: event.SchemaVersion,
		Agent:         "claude-code",
		Kind:          event.UsageToolResult,
		Timestamp:     time.Date(2026, 8, 10, 19, 41, 2, 0, time.UTC),
		SessionID:     session,
		InvocationID:  "toolu_000000000000000000000",
		ToolName:      "Read",
		ResultBytes:   &bytes,
	}
}

func TestUsageRoundTrip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	s, err := store.OpenUsage(dir)
	if err != nil {
		t.Fatalf("OpenUsage: %v", err)
	}
	want := sampleUsage("session-a")
	if err := s.Append(want); err != nil {
		t.Fatalf("Append: %v", err)
	}

	scanner, err := store.ScanUsage(dir)
	if err != nil {
		t.Fatalf("ScanUsage: %v", err)
	}
	defer scanner.Close()

	if !scanner.Scan() {
		t.Fatalf("Scan read nothing: %v", scanner.Err())
	}
	got := scanner.Record()
	if got.SessionID != want.SessionID || got.ToolName != want.ToolName || got.Kind != want.Kind {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if got.ResultBytes == nil || *got.ResultBytes != *want.ResultBytes {
		t.Errorf("ResultBytes = %v", got.ResultBytes)
	}
	if !got.Timestamp.Equal(want.Timestamp) {
		t.Errorf("Timestamp = %s", got.Timestamp)
	}
	if scanner.Scan() {
		t.Error("Scan returned a second record")
	}
}

// The two streams have different writers and different lifetimes. Writing one
// must leave the other alone.
func TestTheStreamsAreSeparateFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	usage, err := store.OpenUsage(dir)
	if err != nil {
		t.Fatalf("OpenUsage: %v", err)
	}
	if err := usage.Append(sampleUsage("session-a")); err != nil {
		t.Fatalf("Append: %v", err)
	}

	if filepath.Base(usage.Path()) != store.UsageFile {
		t.Errorf("usage path = %s, want %s", usage.Path(), store.UsageFile)
	}
	if _, err := os.Stat(filepath.Join(dir, store.EventsFile)); !os.IsNotExist(err) {
		t.Errorf("the event log exists after writing usage: %v", err)
	}

	events, err := store.OpenEvents(dir)
	if err != nil {
		t.Fatalf("OpenEvents: %v", err)
	}
	if err := events.Append(sample("session-a")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if lines := readLines(t, usage.Path()); len(lines) != 1 {
		t.Errorf("the usage log has %d lines after an event was written", len(lines))
	}
}

// A truncated or corrupt usage log is reported the same way as a corrupt event
// log, rather than ending the scan early.
func TestUsageScanReportsWhatItCannotUse(t *testing.T) {
	t.Parallel()

	usable := `{"schema_version":1,"agent":"claude-code","kind":"tool_result",` +
		`"timestamp":"2026-08-10T20:25:04Z","session_id":"a"}`
	log := usable + "\n" +
		"{not json}\n" +
		`{"schema_version":99,"agent":"claude-code","kind":"tool_result","session_id":"b"}` + "\n" +
		usable + "\n" +
		`{"schema_version":1,"agent":"claude-code"`

	s := store.NewScanner[event.Usage](strings.NewReader(log))
	count := 0
	for s.Scan() {
		count++
	}
	if err := s.Err(); err != nil {
		t.Fatalf("Err: %v", err)
	}

	if count != 2 {
		t.Errorf("read %d records, want 2", count)
	}
	stats := s.Stats()
	if stats.Malformed != 1 || stats.UnknownVersion != 1 || stats.Truncated != 1 {
		t.Errorf("stats = %+v", stats)
	}
	if stats.Skipped() != 3 {
		t.Errorf("Skipped = %d, want 3", stats.Skipped())
	}
}

func TestScanUsageReportsAMissingLog(t *testing.T) {
	t.Parallel()

	if _, err := store.ScanUsage(t.TempDir()); err == nil {
		t.Fatal("ScanUsage succeeded on a missing log")
	}
}
