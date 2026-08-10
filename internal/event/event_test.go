package event_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/event"
)

// The JSON field names are the on-disk schema. This test fails on a rename so
// that a change to stored history has to be deliberate.
func TestEventJSONSchema(t *testing.T) {
	t.Parallel()

	duration := int64(12)
	exitCode := 1
	offset, limit := 10, 50

	ev := event.Event{
		SchemaVersion: event.SchemaVersion,
		Agent:         "claude-code",
		Type:          event.TypeToolCall,
		Timestamp:     time.Date(2026, 8, 10, 19, 41, 2, 418000000, time.UTC),
		SessionID:     "session-1",
		TurnID:        "turn-1",
		SubagentID:    "agent-1",
		Cwd:           "/tmp/project",
		Tool: &event.ToolCall{
			Name:         "Read",
			InvocationID: "toolu_1",
			Outcome:      event.OutcomeSuccess,
			DurationMS:   &duration,
			Failure: &event.Failure{
				Kind:     event.FailureKindError,
				ExitCode: &exitCode,
				Digest:   "abc",
			},
			Metadata: &event.ToolMetadata{File: &event.FileOp{
				Path:   "/tmp/project/main.go",
				Access: event.AccessRead,
				Offset: &offset,
				Limit:  &limit,
			}},
		},
	}

	got, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	const want = `{"schema_version":1,"agent":"claude-code","type":"tool_call",` +
		`"timestamp":"2026-08-10T19:41:02.418Z","session_id":"session-1",` +
		`"turn_id":"turn-1","subagent_id":"agent-1","cwd":"/tmp/project",` +
		`"tool":{"name":"Read","invocation_id":"toolu_1","outcome":"success",` +
		`"duration_ms":12,"failure":{"kind":"error","exit_code":1,"digest":"abc"},` +
		`"metadata":{"file":{"path":"/tmp/project/main.go","access":"read","offset":10,"limit":50}}}}`

	if string(got) != want {
		t.Fatalf("schema drift\n got: %s\nwant: %s", got, want)
	}
}

func TestOptionalFieldsAreOmitted(t *testing.T) {
	t.Parallel()

	ev := event.Event{
		SchemaVersion: event.SchemaVersion,
		Agent:         "claude-code",
		Type:          event.TypeSessionEnd,
		Timestamp:     time.Date(2026, 8, 10, 19, 41, 2, 0, time.UTC),
		SessionID:     "session-1",
		Session:       &event.Session{Reason: "other"},
	}

	got, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	const want = `{"schema_version":1,"agent":"claude-code","type":"session_end",` +
		`"timestamp":"2026-08-10T19:41:02Z","session_id":"session-1",` +
		`"session":{"reason":"other"}}`

	if string(got) != want {
		t.Fatalf("unexpected encoding\n got: %s\nwant: %s", got, want)
	}
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()

	ev := event.Event{
		SchemaVersion: event.SchemaVersion,
		Agent:         "claude-code",
		Type:          event.TypeToolCall,
		Timestamp:     time.Date(2026, 8, 10, 19, 41, 2, 0, time.UTC),
		SessionID:     "session-1",
		Tool: &event.ToolCall{
			Name:     "Bash",
			Outcome:  event.OutcomeSuccess,
			Metadata: &event.ToolMetadata{Shell: &event.ShellOp{CommandDigest: "d", Background: true}},
		},
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var back event.Event
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !back.Timestamp.Equal(ev.Timestamp) {
		t.Errorf("timestamp = %v, want %v", back.Timestamp, ev.Timestamp)
	}
	if back.Tool == nil || back.Tool.Metadata == nil || back.Tool.Metadata.Shell == nil {
		t.Fatalf("metadata lost in round trip: %+v", back.Tool)
	}
	if back.Tool.Metadata.Shell.CommandDigest != "d" || !back.Tool.Metadata.Shell.Background {
		t.Errorf("shell metadata = %+v, want digest d and background true", back.Tool.Metadata.Shell)
	}
}
