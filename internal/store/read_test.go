package store_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/store"
)

// record renders a valid event line for session.
func record(session string) string {
	return `{"schema_version":1,"agent":"claude-code","type":"tool_call",` +
		`"timestamp":"2026-08-10T20:25:04Z","session_id":"` + session + `",` +
		`"tool":{"name":"Bash","outcome":"success"}}` + "\n"
}

// scanAll collects everything a scanner yields.
func scanAll(t *testing.T, log string) ([]event.Event, store.ScanStats) {
	t.Helper()

	s := store.NewScanner(strings.NewReader(log))
	var got []event.Event
	for s.Scan() {
		got = append(got, s.Event())
	}
	if err := s.Err(); err != nil {
		t.Fatalf("Err: %v", err)
	}
	return got, s.Stats()
}

func TestScannerReadsEveryRecord(t *testing.T) {
	t.Parallel()

	got, stats := scanAll(t, record("a")+record("b")+record("c"))

	if len(got) != 3 {
		t.Fatalf("read %d events, want 3", len(got))
	}
	for i, want := range []string{"a", "b", "c"} {
		if got[i].SessionID != want {
			t.Errorf("event %d session = %q, want %q", i, got[i].SessionID, want)
		}
	}
	if stats.Skipped() != 0 {
		t.Errorf("stats = %+v, want nothing skipped", stats)
	}
}

func TestScannerOnEmptyLog(t *testing.T) {
	t.Parallel()

	got, stats := scanAll(t, "")

	if len(got) != 0 {
		t.Errorf("read %d events, want 0", len(got))
	}
	if stats.Skipped() != 0 {
		t.Errorf("stats = %+v, want nothing skipped", stats)
	}
}

// A corrupt record must not hide the records after it, and must be counted so
// the profiler can say its conclusions may be incomplete.
func TestScannerSkipsAndCountsMalformedRecord(t *testing.T) {
	t.Parallel()

	got, stats := scanAll(t, record("a")+"{not json}\n"+record("b"))

	if len(got) != 2 {
		t.Fatalf("read %d events, want the 2 valid ones", len(got))
	}
	if got[1].SessionID != "b" {
		t.Errorf("second event session = %q, want b", got[1].SessionID)
	}
	if stats.Malformed != 1 || stats.Truncated != 0 {
		t.Errorf("stats = %+v, want 1 malformed", stats)
	}
}

// A record with no terminating newline is what a crash or a full disk leaves,
// and is reported separately from corruption in the middle of the log.
func TestScannerReportsTruncatedFinalRecord(t *testing.T) {
	t.Parallel()

	partial := strings.TrimSuffix(record("b"), "\n")
	got, stats := scanAll(t, record("a")+partial)

	if len(got) != 1 || got[0].SessionID != "a" {
		t.Fatalf("read %d events, want only the complete one", len(got))
	}
	if stats.Truncated != 1 || stats.Malformed != 0 {
		t.Errorf("stats = %+v, want 1 truncated", stats)
	}
}

// A torn write in the middle leaves a partial record glued to the next one.
// That is corruption, not a truncated tail.
func TestScannerTreatsTornMiddleRecordAsMalformed(t *testing.T) {
	t.Parallel()

	torn := strings.TrimSuffix(record("a"), "\n")[:40]
	got, stats := scanAll(t, torn+record("b")+record("c"))

	if len(got) != 1 || got[0].SessionID != "c" {
		t.Fatalf("read %+v, want only the record after the damage", got)
	}
	if stats.Malformed != 1 || stats.Truncated != 0 {
		t.Errorf("stats = %+v, want 1 malformed", stats)
	}
}

func TestScannerSkipsUnknownSchemaVersion(t *testing.T) {
	t.Parallel()

	future := strings.Replace(record("b"), `"schema_version":1`, `"schema_version":2`, 1)
	got, stats := scanAll(t, record("a")+future+record("c"))

	if len(got) != 2 {
		t.Fatalf("read %d events, want the 2 readable ones", len(got))
	}
	if stats.UnknownVersion != 1 {
		t.Errorf("stats = %+v, want 1 unknown version", stats)
	}
	if stats.Malformed != 0 {
		t.Errorf("stats = %+v, a newer schema is not corruption", stats)
	}
}

// A line longer than any the writer can produce must not be buffered without
// limit, and must not swallow the records after it.
func TestScannerRecoversFromOverlongLine(t *testing.T) {
	t.Parallel()

	oversized := strings.Repeat("x", store.MaxRecordBytes*2) + "\n"
	got, stats := scanAll(t, oversized+record("a"))

	if len(got) != 1 || got[0].SessionID != "a" {
		t.Fatalf("read %+v, want the record after the oversized line", got)
	}
	if stats.Malformed != 1 {
		t.Errorf("stats = %+v, want 1 malformed", stats)
	}
}

// The reader must accept the largest record the writer is allowed to produce.
func TestScannerReadsMaximumSizedRecord(t *testing.T) {
	t.Parallel()

	const prefix = `{"schema_version":1,"agent":"claude-code","type":"tool_call",` +
		`"timestamp":"2026-08-10T20:25:04Z","session_id":"`
	const suffix = `"}` + "\n"

	session := strings.Repeat("s", store.MaxRecordBytes-len(prefix)-len(suffix))
	line := prefix + session + suffix
	if len(line) != store.MaxRecordBytes {
		t.Fatalf("test built a %d byte record, want %d", len(line), store.MaxRecordBytes)
	}

	got, stats := scanAll(t, line)
	if len(got) != 1 {
		t.Fatalf("read %d events, want 1; stats %+v", len(got), stats)
	}
	if got[0].SessionID != session {
		t.Error("the largest allowed record did not survive the round trip")
	}
}

func TestScanReadsBackAppendedEvents(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, id := range []string{"a", "b"} {
		if err := s.Append(event.Event{SchemaVersion: event.SchemaVersion, SessionID: id}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	scanner, err := store.Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	defer scanner.Close()

	var got []string
	for scanner.Scan() {
		got = append(got, scanner.Event().SessionID)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("Err: %v", err)
	}
	if strings.Join(got, ",") != "a,b" {
		t.Errorf("read %v, want the appended events in order", got)
	}
}

func TestScanReportsMissingLog(t *testing.T) {
	t.Parallel()

	_, err := store.Scan(t.TempDir())
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("error = %v, want it to wrap fs.ErrNotExist", err)
	}
}

// Reading must never be mistaken for a reason to create the data directory.
func TestScanDoesNotCreateAnything(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "missing")
	if _, err := store.Scan(dir); err == nil {
		t.Fatal("Scan succeeded on a missing directory")
	}
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Error("Scan created the data directory")
	}
}
