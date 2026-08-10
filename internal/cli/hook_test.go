package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/claude"
	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/store"
)

var hookNow = time.Date(2026, 8, 10, 19, 41, 2, 0, time.UTC)

func logLines(t *testing.T, dir string) []string {
	t.Helper()

	f, err := os.Open(filepath.Join(dir, store.FileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines
}

func TestClaudeHookRecordsEvent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	payload := `{"hook_event_name":"PostToolUse","session_id":"abc","tool_name":"Read","tool_input":{"file_path":"/tmp/a.go"},"tool_use_id":"toolu_1"}`

	if err := runClaudeHook(strings.NewReader(payload), dir, hookNow); err != nil {
		t.Fatalf("runClaudeHook: %v", err)
	}

	lines := logLines(t, dir)
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}

	var ev event.Event
	if err := json.Unmarshal([]byte(lines[0]), &ev); err != nil {
		t.Fatalf("stored line is not valid JSON: %v", err)
	}
	if ev.Tool == nil || ev.Tool.Name != "Read" {
		t.Fatalf("stored event = %+v", ev)
	}
}

func TestClaudeHookRecordsMultipleEvents(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	payloads := []string{
		`{"hook_event_name":"SessionStart","session_id":"abc","source":"startup"}`,
		`{"hook_event_name":"PostToolUse","session_id":"abc","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`,
		`{"hook_event_name":"PostToolUseFailure","session_id":"abc","tool_name":"Bash","tool_input":{"command":"go build"},"error":"Exit code 2\nboom"}`,
		`{"hook_event_name":"SessionEnd","session_id":"abc","reason":"other"}`,
	}

	for _, p := range payloads {
		if err := runClaudeHook(strings.NewReader(p), dir, hookNow); err != nil {
			t.Fatalf("runClaudeHook: %v", err)
		}
	}

	if lines := logLines(t, dir); len(lines) != len(payloads) {
		t.Fatalf("got %d lines, want %d", len(lines), len(payloads))
	}
}

// Axiom is a passive observer: Claude Code must never see a failure, and a
// non-zero exit is the only way it could.
func TestClaudeHookFailsOpen(t *testing.T) {
	t.Parallel()

	oversized := `{"hook_event_name":"PostToolUse","session_id":"abc","tool_name":"Write","tool_input":{"content":"` +
		strings.Repeat("x", claude.MaxPayloadBytes) + `"}}`

	tests := map[string]string{
		"malformed json":     `{"hook_event_name":`,
		"empty payload":      ``,
		"unsupported event":  `{"hook_event_name":"PreToolUse","session_id":"abc","tool_name":"Bash"}`,
		"missing session id": `{"hook_event_name":"SessionStart"}`,
		"missing tool name":  `{"hook_event_name":"PostToolUse","session_id":"abc"}`,
		"oversized payload":  oversized,
	}

	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			if err := runClaudeHook(strings.NewReader(payload), dir, hookNow); err != nil {
				t.Fatalf("runClaudeHook returned %v, want nil so Claude keeps working", err)
			}
			if lines := logLines(t, dir); len(lines) != 0 {
				t.Fatalf("wrote %d lines, want none: %q", len(lines), lines)
			}
		})
	}
}

// An unwritable data directory must not turn into a Claude-visible failure.
func TestClaudeHookFailsOpenOnUnwritableStore(t *testing.T) {
	t.Parallel()

	// A regular file where the data directory should be makes MkdirAll fail.
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed blocker: %v", err)
	}

	payload := `{"hook_event_name":"SessionStart","session_id":"abc","source":"startup"}`
	if err := runClaudeHook(strings.NewReader(payload), blocked, hookNow); err != nil {
		t.Fatalf("runClaudeHook returned %v, want nil", err)
	}
}

// Anything written to stdout during SessionStart is injected into Claude's
// context as instructions, so the hook path must not be able to write there.
func TestClaudeHookWritesNothingToStdout(t *testing.T) {
	original := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = original })

	dir := t.TempDir()
	payload := `{"hook_event_name":"SessionStart","session_id":"abc","source":"startup","model":"claude-sonnet-5"}`
	if err := runClaudeHook(strings.NewReader(payload), dir, hookNow); err != nil {
		t.Fatalf("runClaudeHook: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	captured, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	if len(captured) != 0 {
		t.Fatalf("hook wrote %q to stdout, want nothing", captured)
	}

	if lines := logLines(t, dir); len(lines) != 1 {
		t.Fatalf("got %d recorded events, want 1", len(lines))
	}
}

func TestRunHookRejectsBadInvocation(t *testing.T) {
	t.Parallel()

	for name, args := range map[string][]string{
		"no agent":      {},
		"unknown agent": {"codex"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := runHook(args)
			if err == nil {
				t.Fatal("runHook returned nil, want a usage error")
			}
			if !IsUsage(err) {
				t.Fatalf("error = %v, want a usage error", err)
			}
		})
	}
}
