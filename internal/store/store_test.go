package store_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/store"
)

func sample(session string) event.Event {
	return event.Event{
		SchemaVersion: event.SchemaVersion,
		Agent:         "claude-code",
		Type:          event.TypeSessionStart,
		Timestamp:     time.Date(2026, 8, 10, 19, 41, 2, 0, time.UTC),
		SessionID:     session,
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read log: %v", err)
	}
	return lines
}

func TestAppendCreatesOneLinePerEvent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	for _, id := range []string{"a", "b", "c"} {
		if err := s.Append(sample(id)); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
	}

	lines := readLines(t, s.Path())
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	for i, line := range lines {
		var ev event.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("line %d is not valid JSON: %v", i, err)
		}
		if ev.SchemaVersion != event.SchemaVersion {
			t.Errorf("line %d schema_version = %d, want %d", i, ev.SchemaVersion, event.SchemaVersion)
		}
	}
}

// A separate process appends on every hook invocation, so reopening must not
// truncate what earlier invocations wrote.
func TestAppendAcrossReopen(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, id := range []string{"first", "second"} {
		s, err := store.Open(dir)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		if err := s.Append(sample(id)); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	lines := readLines(t, filepath.Join(dir, store.FileName))
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	if !strings.Contains(lines[0], "first") || !strings.Contains(lines[1], "second") {
		t.Fatalf("unexpected order: %q", lines)
	}
}

func TestRejectedEventLeavesPriorRecordsIntact(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := s.Append(sample("good")); err != nil {
		t.Fatalf("append: %v", err)
	}
	before, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatalf("read log: %v", err)
	}

	bad := sample("bad")
	bad.Session = &event.Session{Source: strings.Repeat("x", store.MaxRecordBytes)}

	if err := s.Append(bad); err == nil {
		t.Fatal("append of an oversized event returned nil, want an error")
	}

	after, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("log changed after a rejected append\nbefore: %q\nafter:  %q", before, after)
	}
}

func TestEventLogIsPrivate(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := s.Append(sample("a")); err != nil {
		t.Fatalf("append: %v", err)
	}

	info, err := os.Stat(s.Path())
	if err != nil {
		t.Fatalf("stat log: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("log mode = %o, want 600", perm)
	}
}

// Parallel tool calls mean several hook processes append at once.
func TestConcurrentAppends(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func() {
			defer wg.Done()
			_ = s.Append(sample(string(rune('a' + i%26))))
		}()
	}
	wg.Wait()

	lines := readLines(t, s.Path())
	if len(lines) != n {
		t.Fatalf("got %d lines, want %d", len(lines), n)
	}
	for i, line := range lines {
		var ev event.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("line %d interleaved or malformed: %v", i, err)
		}
	}
}
