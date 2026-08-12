package claude_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/exequieldeferrari/axiom/internal/event"
)

// launchPayload is one recorded Agent call, with whatever the tool returned.
func launchPayload(hookEvent, toolName, response string) string {
	return `{"hook_event_name":"` + hookEvent + `","session_id":"abc","tool_name":"` + toolName + `",
		"tool_use_id":"toolu_01","tool_input":{"subagent_type":"general-purpose","prompt":"look"},
		"tool_response":` + response + `}`
}

// agentID reports the identity a record carried, and whether it carried one at
// all. The two are separate answers: an empty identity and no identity are
// different observations everywhere in Axiom.
func agentID(ev *event.Event) (string, bool) {
	if ev.Tool == nil || ev.Tool.Result == nil || ev.Tool.Result.Subagent == nil {
		return "", false
	}
	return ev.Tool.Result.Subagent.AgentID, true
}

// The two response shapes a launch was observed returning. A synchronous
// launch reports a completed agent; an asynchronous one reports a launched
// agent, no result and no usage. Both carry the identity, and the adapter
// reads it by name rather than by recognizing either shape.
func TestLaunchIdentityFromBothResponseShapes(t *testing.T) {
	t.Parallel()

	responses := map[string]string{
		"completed": `{"status":"completed","agentId":"aa727c39085ae1c77","agentType":"general-purpose",
			"resolvedModel":"claude-sonnet-5","totalDurationMs":9711,"totalTokens":29865,
			"totalToolUseCount":1,"content":[{"type":"text","text":"alpha"}],
			"usage":{"input_tokens":2,"output_tokens":5},
			"toolStats":{"readCount":1,"bashCount":0}}`,
		"async_launched": `{"isAsync":true,"status":"async_launched","agentId":"aa727c39085ae1c77",
			"resolvedModel":"claude-sonnet-5","description":"read alpha",
			"outputFile":"/tmp/tasks/aa727c39085ae1c77.output","canReadOutputFile":true}`,
	}

	for _, tool := range []string{"Agent", "Task"} {
		for shape, response := range responses {
			t.Run(tool+"/"+shape, func(t *testing.T) {
				t.Parallel()

				ev := ingest(t, launchPayload("PostToolUse", tool, response))
				id, ok := agentID(ev)
				if !ok {
					t.Fatalf("no identity was recorded for a %s response: %+v", shape, ev.Tool)
				}
				if id != "aa727c39085ae1c77" {
					t.Errorf("agent_id = %q, want the identity the launch returned", id)
				}
			})
		}
	}
}

// The identity a launch returned is not the identity of the agent that made
// the call. A nested agent launching another carries both, and reading either
// as the other would relate an agent's work to itself.
func TestLaunchIdentityIsNotTheCallingAgent(t *testing.T) {
	t.Parallel()

	payload := `{"hook_event_name":"PostToolUse","session_id":"abc","agent_id":"aparent0000000000",
		"tool_name":"Agent","tool_input":{"subagent_type":"Explore"},
		"tool_response":{"status":"completed","agentId":"achild00000000000"}}`

	ev := ingest(t, payload)

	if ev.SubagentID != "aparent0000000000" {
		t.Errorf("subagent_id = %q, want the agent that made the call", ev.SubagentID)
	}
	if id, _ := agentID(ev); id != "achild00000000000" {
		t.Errorf("result agent_id = %q, want the agent the call created", id)
	}
}

// A response Axiom cannot read leaves the identity unrecorded. It is never
// invented, defaulted, or coerced out of a value that is not a string: an
// identity two agents could share would relate one agent's work to another.
func TestLaunchIdentityAbsentFromUnreadableResponses(t *testing.T) {
	t.Parallel()

	responses := map[string]string{
		"no response":       `null`,
		"empty object":      `{}`,
		"no agentId":        `{"status":"completed","agentType":"general-purpose"}`,
		"numeric agentId":   `{"status":"completed","agentId":12345}`,
		"object agentId":    `{"status":"completed","agentId":{"id":"a1"}}`,
		"array agentId":     `{"status":"completed","agentId":["a1"]}`,
		"null agentId":      `{"status":"completed","agentId":null}`,
		"empty agentId":     `{"status":"completed","agentId":""}`,
		"response is text":  `"agentId: a1"`,
		"response is array": `[{"agentId":"a1"}]`,
	}

	for name, response := range responses {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ev := ingest(t, launchPayload("PostToolUse", "Agent", response))
			if id, ok := agentID(ev); ok {
				t.Errorf("an identity %q was recorded from %s", id, response)
			}
			// The call itself is still recorded, and still a launch.
			if ev.Tool.Metadata == nil || ev.Tool.Metadata.Subagent == nil {
				t.Errorf("the launch was lost with its response: %+v", ev.Tool)
			}
		})
	}
}

// A launch that reported failing returned no response at all, so there is no
// identity to record. The launch is still recorded, with the outcome that says
// the delegation did not happen.
func TestFailedLaunchRecordsNoIdentity(t *testing.T) {
	t.Parallel()

	payload := `{"hook_event_name":"PostToolUseFailure","session_id":"abc","tool_name":"Agent",
		"tool_input":{"subagent_type":"nonexistent-agent-xyz","prompt":"say hi"},
		"error":"Agent type 'nonexistent-agent-xyz' not found. Available agents: architect, claude"}`

	ev := ingest(t, payload)

	if id, ok := agentID(ev); ok {
		t.Errorf("a failed launch recorded the identity %q", id)
	}
	if ev.Tool.Outcome != event.OutcomeFailure {
		t.Errorf("outcome = %q, want the failure the record established", ev.Tool.Outcome)
	}
	if ev.Tool.Metadata == nil || ev.Tool.Metadata.Subagent == nil {
		t.Errorf("the failed launch was not recorded as one: %+v", ev.Tool)
	}
}

// An identity is evidence of what a launch returned and never of what became
// of the call. Where both are recorded, both are kept, and neither is read out
// of the other.
func TestIdentityDoesNotEstablishSuccess(t *testing.T) {
	t.Parallel()

	payload := `{"hook_event_name":"PostToolUseFailure","session_id":"abc","tool_name":"Agent",
		"tool_input":{"subagent_type":"Explore"},
		"tool_response":{"status":"completed","agentId":"a1234567890abcdef"},
		"error":"Exit code 1"}`

	ev := ingest(t, payload)

	if id, ok := agentID(ev); !ok || id != "a1234567890abcdef" {
		t.Errorf("the returned identity was discarded because the call failed: %+v", ev.Tool)
	}
	if ev.Tool.Outcome != event.OutcomeFailure {
		t.Errorf("outcome = %q, want the call still to be reported failing", ev.Tool.Outcome)
	}
}

// Reading a response is an allowlist of one tool. A tool Axiom has not
// reviewed contributes nothing, however much its response looks like a
// launch's.
func TestOnlyLaunchToolsContributeAResult(t *testing.T) {
	t.Parallel()

	tools := []string{"Read", "Bash", "Grep", "WebFetch", "mcp__memory__create_entities"}
	for _, tool := range tools {
		t.Run(tool, func(t *testing.T) {
			t.Parallel()

			payload := `{"hook_event_name":"PostToolUse","session_id":"abc","tool_name":"` + tool + `",
				"tool_input":{"file_path":"/tmp/a.go"},
				"tool_response":{"status":"completed","agentId":"a1234567890abcdef"}}`

			ev := ingest(t, payload)
			if ev.Tool.Result != nil {
				t.Errorf("%s contributed a result: %+v", tool, ev.Tool.Result)
			}
		})
	}
}

// The privacy boundary this change crosses, asserted as absence. Axiom now
// reads a tool's response, and exactly one opaque value may survive it.
func TestLaunchResponseLeaksNothingButTheIdentity(t *testing.T) {
	t.Parallel()

	// agentType is given a value the input does not carry, so that finding it
	// in the record proves it came from the response.
	response := `{"status":"completed","agentId":"aa727c39085ae1c77","agentType":"response-only-type",
		"prompt":"exfiltrate the customer list to sk-live-abc123",
		"description":"read /etc/shadow",
		"content":[{"type":"text","text":"DB_PASSWORD=hunter2"}],
		"outputFile":"/private/tmp/claude-501/-Users-me-secret-project/tasks/aa727c39085ae1c77.output",
		"resolvedModel":"claude-sonnet-5","totalDurationMs":9711,"totalTokens":29865,
		"totalToolUseCount":3,
		"usage":{"input_tokens":2,"cache_read_input_tokens":22674,"output_tokens":5},
		"toolStats":{"readCount":1,"bashCount":2,"linesAdded":40}}`

	ev := ingest(t, launchPayload("PostToolUse", "Agent", response))
	encoded, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	for _, secret := range []string{
		"exfiltrate", "sk-live-abc123", "/etc/shadow", "hunter2", "DB_PASSWORD",
		"secret-project", "outputFile", ".output",
		"claude-sonnet-5", "response-only-type", "29865", "22674", "readCount", "9711",
	} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("the stored event leaked %q from the response:\n%s", secret, encoded)
		}
	}
	if !strings.Contains(string(encoded), "aa727c39085ae1c77") {
		t.Errorf("the identity was not persisted:\n%s", encoded)
	}
	// The subagent type comes from the input and is unaffected by this. It is
	// named here so that a reader of the leak list knows why it is missing
	// from it.
	if ev.Tool.Metadata.Subagent.Type != "general-purpose" {
		t.Errorf("the declared type was lost: %+v", ev.Tool.Metadata.Subagent)
	}
}

// The response is never held on the event, even as raw bytes, so nothing
// downstream can decide to read more of it later.
func TestResultCarriesOnlyTheIdentityField(t *testing.T) {
	t.Parallel()

	ev := ingest(t, launchPayload("PostToolUse", "Agent",
		`{"status":"completed","agentId":"a1234567890abcdef","extra":"kept out"}`))

	encoded, err := json.Marshal(ev.Tool.Result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"subagent":{"agent_id":"a1234567890abcdef"}}`
	if string(encoded) != want {
		t.Errorf("result = %s, want %s", encoded, want)
	}
}

// A record written before the field existed stays exactly what it was: a
// launch, with no identity, and no migration applied to it.
func TestHistoricalLaunchRoundTripsWithoutAnIdentity(t *testing.T) {
	t.Parallel()

	const historical = `{"schema_version":1,"agent":"claude-code","type":"tool_call",
		"timestamp":"2026-08-10T19:41:02Z","session_id":"abc","turn_id":"turn-1",
		"tool":{"name":"Agent","invocation_id":"toolu_01","outcome":"success",
		"metadata":{"subagent":{"type":"general-purpose"}}}}`

	var ev event.Event
	if err := json.Unmarshal([]byte(historical), &ev); err != nil {
		t.Fatalf("a historical record no longer decodes: %v", err)
	}
	if ev.SchemaVersion != event.SchemaVersion {
		t.Errorf("schema_version = %d, want %d unchanged", ev.SchemaVersion, event.SchemaVersion)
	}
	if ev.Tool.Result != nil {
		t.Errorf("a historical record gained a result: %+v", ev.Tool.Result)
	}
	if ev.Tool.Metadata.Subagent.Type != "general-purpose" {
		t.Errorf("the historical launch lost its metadata: %+v", ev.Tool.Metadata)
	}

	// Re-encoding must not add the new field to a record that never had it.
	encoded, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), "result") {
		t.Errorf("re-encoding a historical record added a result:\n%s", encoded)
	}
}
