package event_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/event"
)

// A measurement the agent never reported has to be absent from the record, so
// that a reader cannot mistake it for a measurement of zero.
func TestUnreportedMeasurementsAreAbsent(t *testing.T) {
	t.Parallel()

	u := event.Usage{
		SchemaVersion: event.SchemaVersion,
		Agent:         "claude-code",
		Kind:          event.UsageToolResult,
		Timestamp:     time.Date(2026, 8, 10, 19, 41, 2, 0, time.UTC),
		SessionID:     "session-a",
	}

	encoded, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, field := range []string{"tokens", "cost_micros", "duration_ms", "result_bytes", "model", "tool_name"} {
		if strings.Contains(string(encoded), field) {
			t.Errorf("an unreported %s was written:\n%s", field, encoded)
		}
	}
	for _, field := range []string{"schema_version", "agent", "kind", "timestamp", "session_id"} {
		if !strings.Contains(string(encoded), field) {
			t.Errorf("%s is missing:\n%s", field, encoded)
		}
	}
}

// A count of zero that the agent did report is kept, because zero cache reads
// is a real measurement.
func TestReportedZeroIsKept(t *testing.T) {
	t.Parallel()

	zero := int64(0)
	u := event.Usage{
		SchemaVersion: event.SchemaVersion,
		Kind:          event.UsageModelRequest,
		Tokens:        &event.Tokens{Input: 2, Output: 93},
		CostMicros:    &zero,
	}

	encoded, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(encoded), `"cost_micros":0`) {
		t.Errorf("a reported zero was dropped:\n%s", encoded)
	}
	if !strings.Contains(string(encoded), `"cache_read":0`) {
		t.Errorf("a token category was dropped:\n%s", encoded)
	}

	var got event.Usage
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.CostMicros == nil || *got.CostMicros != 0 {
		t.Errorf("CostMicros = %v, want a reported zero", got.CostMicros)
	}
	if got.Tokens == nil || *got.Tokens != *u.Tokens {
		t.Errorf("Tokens = %v", got.Tokens)
	}
}

// Both canonical records carry the schema they were written under, which is
// what lets one scanner refuse a record it cannot interpret.
func TestBothRecordsReportTheirVersion(t *testing.T) {
	t.Parallel()

	if got := (event.Usage{SchemaVersion: 7}).Version(); got != 7 {
		t.Errorf("Usage.Version = %d", got)
	}
	if got := (event.Event{SchemaVersion: 7}).Version(); got != 7 {
		t.Errorf("Event.Version = %d", got)
	}
}
