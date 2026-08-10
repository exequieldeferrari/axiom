package claude_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/claude"
	"github.com/exequieldeferrari/axiom/internal/event"
)

var now = time.Date(2026, 8, 10, 19, 41, 2, 0, time.UTC)

func ingest(t *testing.T, payload string) *event.Event {
	t.Helper()

	ev, err := claude.Ingest(strings.NewReader(payload), now)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if ev == nil {
		t.Fatal("Ingest returned no event, want one")
	}
	return ev
}

func TestIngestSessionStart(t *testing.T) {
	t.Parallel()

	ev := ingest(t, `{
		"hook_event_name": "SessionStart",
		"session_id": "abc123",
		"cwd": "/Users/me/project",
		"source": "startup",
		"model": "claude-sonnet-5"
	}`)

	if ev.Type != event.TypeSessionStart {
		t.Errorf("type = %q, want %q", ev.Type, event.TypeSessionStart)
	}
	if ev.Agent != claude.AgentName {
		t.Errorf("agent = %q, want %q", ev.Agent, claude.AgentName)
	}
	if ev.SchemaVersion != event.SchemaVersion {
		t.Errorf("schema_version = %d, want %d", ev.SchemaVersion, event.SchemaVersion)
	}
	if !ev.Timestamp.Equal(now) {
		t.Errorf("timestamp = %v, want %v", ev.Timestamp, now)
	}
	if ev.SessionID != "abc123" || ev.Cwd != "/Users/me/project" {
		t.Errorf("session/cwd = %q/%q", ev.SessionID, ev.Cwd)
	}
	if ev.Session == nil || ev.Session.Source != "startup" || ev.Session.Model != "claude-sonnet-5" {
		t.Fatalf("session = %+v", ev.Session)
	}
}

// model is documented as sometimes absent, for example after /clear.
func TestIngestSessionStartWithoutModel(t *testing.T) {
	t.Parallel()

	ev := ingest(t, `{"hook_event_name":"SessionStart","session_id":"abc","source":"clear"}`)
	if ev.Session.Model != "" {
		t.Errorf("model = %q, want empty", ev.Session.Model)
	}
}

func TestIngestSessionEnd(t *testing.T) {
	t.Parallel()

	ev := ingest(t, `{"hook_event_name":"SessionEnd","session_id":"abc","reason":"logout"}`)
	if ev.Type != event.TypeSessionEnd {
		t.Errorf("type = %q, want %q", ev.Type, event.TypeSessionEnd)
	}
	if ev.Session == nil || ev.Session.Reason != "logout" {
		t.Fatalf("session = %+v", ev.Session)
	}
}

func TestIngestPostToolUse(t *testing.T) {
	t.Parallel()

	ev := ingest(t, `{
		"hook_event_name": "PostToolUse",
		"session_id": "abc",
		"prompt_id": "turn-1",
		"agent_id": "sub-1",
		"tool_name": "Read",
		"tool_input": {"file_path": "/tmp/a.go"},
		"tool_response": {"success": true},
		"tool_use_id": "toolu_1",
		"duration_ms": 12
	}`)

	if ev.Type != event.TypeToolCall {
		t.Fatalf("type = %q, want %q", ev.Type, event.TypeToolCall)
	}
	if ev.TurnID != "turn-1" || ev.SubagentID != "sub-1" {
		t.Errorf("turn/subagent = %q/%q", ev.TurnID, ev.SubagentID)
	}
	if ev.Tool.Outcome != event.OutcomeSuccess {
		t.Errorf("outcome = %q, want success", ev.Tool.Outcome)
	}
	if ev.Tool.InvocationID != "toolu_1" {
		t.Errorf("invocation_id = %q", ev.Tool.InvocationID)
	}
	if ev.Tool.DurationMS == nil || *ev.Tool.DurationMS != 12 {
		t.Errorf("duration = %v, want 12", ev.Tool.DurationMS)
	}
	if ev.Tool.Failure != nil {
		t.Errorf("failure = %+v, want nil on success", ev.Tool.Failure)
	}
}

// duration_ms is documented as optional.
func TestIngestPostToolUseWithoutDuration(t *testing.T) {
	t.Parallel()

	ev := ingest(t, `{"hook_event_name":"PostToolUse","session_id":"abc","tool_name":"Read","tool_input":{"file_path":"/tmp/a.go"}}`)
	if ev.Tool.DurationMS != nil {
		t.Errorf("duration = %v, want nil", *ev.Tool.DurationMS)
	}
}

func TestIngestPostToolUseFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		payload      string
		wantKind     string
		wantExitCode *int
	}{
		{
			name: "exit code prefix",
			payload: `{"hook_event_name":"PostToolUseFailure","session_id":"abc","tool_name":"Bash",
				"tool_input":{"command":"npm test"},
				"error":"Exit code 1\nError: Cannot find module 'express'","duration_ms":4187}`,
			wantKind:     event.FailureKindError,
			wantExitCode: ptr(1),
		},
		{
			name: "no exit code line",
			payload: `{"hook_event_name":"PostToolUseFailure","session_id":"abc","tool_name":"Bash",
				"tool_input":{"command":"npm test"},"error":"could not start shell"}`,
			wantKind:     event.FailureKindError,
			wantExitCode: nil,
		},
		{
			name: "interrupt",
			payload: `{"hook_event_name":"PostToolUseFailure","session_id":"abc","tool_name":"Bash",
				"tool_input":{"command":"sleep 100"},"error":"aborted","is_interrupt":true}`,
			wantKind:     event.FailureKindInterrupt,
			wantExitCode: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ev := ingest(t, tt.payload)
			if ev.Tool.Outcome != event.OutcomeFailure {
				t.Errorf("outcome = %q, want failure", ev.Tool.Outcome)
			}
			f := ev.Tool.Failure
			if f == nil {
				t.Fatal("failure = nil, want details")
			}
			if f.Kind != tt.wantKind {
				t.Errorf("kind = %q, want %q", f.Kind, tt.wantKind)
			}
			switch {
			case tt.wantExitCode == nil && f.ExitCode != nil:
				t.Errorf("exit_code = %d, want none", *f.ExitCode)
			case tt.wantExitCode != nil && f.ExitCode == nil:
				t.Errorf("exit_code = none, want %d", *tt.wantExitCode)
			case tt.wantExitCode != nil && *f.ExitCode != *tt.wantExitCode:
				t.Errorf("exit_code = %d, want %d", *f.ExitCode, *tt.wantExitCode)
			}
			if f.Digest == "" {
				t.Error("digest is empty, want a digest of the error text")
			}
		})
	}
}

func TestIngestIgnoresUnsupportedEvents(t *testing.T) {
	t.Parallel()

	for _, payload := range []string{
		`{"hook_event_name":"PreToolUse","session_id":"abc","tool_name":"Bash"}`,
		`{"hook_event_name":"Notification","session_id":"abc"}`,
		`{"hook_event_name":"","session_id":"abc"}`,
		`{"session_id":"abc"}`,
	} {
		ev, err := claude.Ingest(strings.NewReader(payload), now)
		if err != nil {
			t.Errorf("Ingest(%s) error = %v, want nil", payload, err)
		}
		if ev != nil {
			t.Errorf("Ingest(%s) = %+v, want no event", payload, ev)
		}
	}
}

func TestIngestRejectsMalformedInput(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		payload string
		want    error
	}{
		"not json":           {payload: `{"hook_event_name":`, want: nil},
		"empty":              {payload: ``, want: nil},
		"array":              {payload: `["SessionStart"]`, want: nil},
		"missing session id": {payload: `{"hook_event_name":"SessionStart"}`, want: claude.ErrMissingSessionID},
		"missing tool name":  {payload: `{"hook_event_name":"PostToolUse","session_id":"abc"}`, want: claude.ErrMissingToolName},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ev, err := claude.Ingest(strings.NewReader(tt.payload), now)
			if err == nil {
				t.Fatalf("Ingest = %+v, want an error", ev)
			}
			if ev != nil {
				t.Errorf("Ingest returned %+v alongside an error, want nil", ev)
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Errorf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestIngestRejectsOversizedPayload(t *testing.T) {
	t.Parallel()

	huge := strings.NewReader(`{"hook_event_name":"PostToolUse","session_id":"abc","tool_name":"Write","tool_input":{"file_path":"/tmp/a","content":"` +
		strings.Repeat("x", claude.MaxPayloadBytes) + `"}}`)

	ev, err := claude.Ingest(huge, now)
	if !errors.Is(err, claude.ErrPayloadTooLarge) {
		t.Fatalf("error = %v, want %v", err, claude.ErrPayloadTooLarge)
	}
	if ev != nil {
		t.Errorf("event = %+v, want nil", ev)
	}
}

func ptr[T any](v T) *T { return &v }
