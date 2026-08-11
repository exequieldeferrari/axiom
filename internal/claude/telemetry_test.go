package claude_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/claude"
	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/otlp"
)

// realExport decodes a captured Claude Code export. The fixture lives with the
// decoder that owns the wire format; the privacy rules below have to be proven
// against a real payload rather than one written to pass them.
func realExport(t *testing.T) map[string]otlp.Record {
	t.Helper()

	data, err := os.ReadFile("../otlp/testdata/claude_logs.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	records, err := otlp.DecodeLogs(data)
	if err != nil {
		t.Fatalf("DecodeLogs: %v", err)
	}
	byName := make(map[string]otlp.Record, len(records))
	for _, r := range records {
		byName[r.Name] = r
	}
	return byName
}

func usage(t *testing.T, rec otlp.Record) event.Usage {
	t.Helper()

	u, ok := claude.Usage(rec)
	if !ok {
		t.Fatalf("%s was not mapped", rec.Name)
	}
	return u
}

func TestModelRequestMapping(t *testing.T) {
	t.Parallel()

	u := usage(t, realExport(t)["api_request"])

	if u.Kind != event.UsageModelRequest {
		t.Errorf("Kind = %q", u.Kind)
	}
	if u.Agent != claude.AgentName {
		t.Errorf("Agent = %q, want %q", u.Agent, claude.AgentName)
	}
	if u.SchemaVersion != event.SchemaVersion {
		t.Errorf("SchemaVersion = %d", u.SchemaVersion)
	}
	if u.SessionID != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("SessionID = %q", u.SessionID)
	}
	if u.TurnID != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Errorf("TurnID = %q", u.TurnID)
	}
	if u.Model != "claude-sonnet-5" {
		t.Errorf("Model = %q", u.Model)
	}
	if u.Source != "sdk" {
		t.Errorf("Source = %q", u.Source)
	}
	if u.Tokens == nil {
		t.Fatal("Tokens is nil")
	}
	want := event.Tokens{Input: 2, Output: 93, CacheRead: 0, CacheCreation: 35419}
	if *u.Tokens != want {
		t.Errorf("Tokens = %+v, want %+v", *u.Tokens, want)
	}
	if u.CostMicros == nil || *u.CostMicros != 213915 {
		t.Errorf("CostMicros = %v, want 213915", u.CostMicros)
	}
	if u.DurationMS == nil || *u.DurationMS != 3002 {
		t.Errorf("DurationMS = %v, want 3002", u.DurationMS)
	}
	if !u.Timestamp.Equal(time.Unix(0, 1786407295795000000).UTC()) {
		t.Errorf("Timestamp = %s", u.Timestamp)
	}

	// A model request has no tool identity to carry.
	if u.InvocationID != "" || u.ToolName != "" || u.ResultBytes != nil {
		t.Errorf("tool fields set on a model request: %+v", u)
	}
}

func TestToolResultMapping(t *testing.T) {
	t.Parallel()

	u := usage(t, realExport(t)["tool_result"])

	if u.Kind != event.UsageToolResult {
		t.Errorf("Kind = %q", u.Kind)
	}
	// The identifier that a later milestone will join on.
	if u.InvocationID != "toolu_000000000000000000000" {
		t.Errorf("InvocationID = %q", u.InvocationID)
	}
	if u.ToolName != "Read" {
		t.Errorf("ToolName = %q", u.ToolName)
	}
	if u.ResultBytes == nil || *u.ResultBytes != 43 {
		t.Errorf("ResultBytes = %v, want 43", u.ResultBytes)
	}
	// duration_ms arrives as a quoted string on this event.
	if u.DurationMS == nil || *u.DurationMS != 2 {
		t.Errorf("DurationMS = %v, want 2", u.DurationMS)
	}
	if u.Tokens != nil || u.CostMicros != nil || u.Model != "" {
		t.Errorf("model fields set on a tool result: %+v", u)
	}
}

// Everything Claude Code exports that is not a measurement stays out of the
// usage stream, including the prompt event that carries prompt text when
// someone enables content logging.
func TestUnmeasuredEventsAreDropped(t *testing.T) {
	t.Parallel()

	records := realExport(t)
	for _, name := range []string{"user_prompt", "hook_registered"} {
		if u, ok := claude.Usage(records[name]); ok {
			t.Errorf("%s was mapped to %+v", name, u)
		}
	}
	if u, ok := claude.Usage(otlp.Record{Name: "some_future_event", Time: time.Now()}); ok {
		t.Errorf("an unknown event was mapped to %+v", u)
	}
}

// This is the privacy boundary, checked against a real payload: Claude Code
// attaches the developer's email address and account identifiers to every
// record it sends.
func TestIdentityNeverReachesAUsageRecord(t *testing.T) {
	t.Parallel()

	records := realExport(t)
	// The fixture has to still contain what we claim to be dropping.
	for _, key := range []string{"user.email", "user.id", "user.account_uuid", "user.account_id", "organization.id", "terminal.type"} {
		if records["api_request"].Attrs.String(key) == "" {
			t.Fatalf("the fixture no longer carries %q, so this test proves nothing", key)
		}
	}

	for _, name := range []string{"api_request", "tool_result"} {
		encoded, err := json.Marshal(usage(t, records[name]))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		for _, secret := range []string{
			"developer@example.com",
			"user_000000000000000000000",
			"12345678-1234-4234-8234-123456789abc",
			"00000000-0000-4000-8000-000000000000",
			strings.Repeat("0", 64),
			"xterm-256color",
			"req_000000000000000000000",
		} {
			if strings.Contains(string(encoded), secret) {
				t.Errorf("%s record contains %q:\n%s", name, secret, encoded)
			}
		}
	}
}

// Content attributes only appear when someone sets an OTEL_LOG_* flag, which
// Axiom never does. If one arrives anyway, it must not be stored.
func TestContentAttributesAreNeverRead(t *testing.T) {
	t.Parallel()

	rec := realExport(t)["tool_result"]
	rec.Attrs["tool_input"] = `{"file_path":"/home/dev/secrets.env"}`
	rec.Attrs["tool_parameters"] = `{"bash_command":"aws sts get-caller-identity"}`
	rec.Attrs["error"] = "cannot open /home/dev/.ssh/id_rsa"

	encoded, err := json.Marshal(usage(t, rec))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, leaked := range []string{"secrets.env", "aws sts", "id_rsa"} {
		if strings.Contains(string(encoded), leaked) {
			t.Errorf("record contains %q:\n%s", leaked, encoded)
		}
	}
}

func TestRecordsWithoutIdentityAreDropped(t *testing.T) {
	t.Parallel()

	records := realExport(t)

	// A record Axiom cannot attribute to a session is worse than no record.
	noSession := records["api_request"]
	delete(noSession.Attrs, "session.id")
	if _, ok := claude.Usage(noSession); ok {
		t.Error("a record with no session was mapped")
	}

	// Without a timestamp there is nothing to order or scope it by.
	noTime := records["tool_result"]
	noTime.Time = time.Time{}
	if _, ok := claude.Usage(noTime); ok {
		t.Error("a record with no timestamp was mapped")
	}
}

// A count the agent never sent must stay absent, so that a later milestone
// cannot mistake silence for a measurement of zero.
func TestUnreportedMeasurementsStayUnknown(t *testing.T) {
	t.Parallel()

	rec := realExport(t)["api_request"]
	for _, key := range []string{
		"input_tokens", "output_tokens", "cache_read_tokens", "cache_creation_tokens",
		"cost_usd_micros", "duration_ms",
	} {
		delete(rec.Attrs, key)
	}

	u := usage(t, rec)
	if u.Tokens != nil {
		t.Errorf("Tokens = %+v, want nil", u.Tokens)
	}
	if u.CostMicros != nil || u.DurationMS != nil {
		t.Errorf("cost or duration invented: %+v", u)
	}

	encoded, _ := json.Marshal(u)
	if strings.Contains(string(encoded), "tokens") || strings.Contains(string(encoded), "cost") {
		t.Errorf("unknown measurements were written as zero:\n%s", encoded)
	}
}

// A partial token report is kept as the agent sent it.
func TestPartialTokenReportIsKept(t *testing.T) {
	t.Parallel()

	rec := realExport(t)["api_request"]
	delete(rec.Attrs, "cache_read_tokens")
	delete(rec.Attrs, "cache_creation_tokens")

	u := usage(t, rec)
	if u.Tokens == nil {
		t.Fatal("Tokens is nil, want the counts that were reported")
	}
	if u.Tokens.Input != 2 || u.Tokens.CacheRead != 0 {
		t.Errorf("Tokens = %+v", *u.Tokens)
	}
}
