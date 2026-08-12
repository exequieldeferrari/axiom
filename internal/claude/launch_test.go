package claude_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/exequieldeferrari/axiom/internal/delegation"
	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/work"
)

// A launch that declared no type still relates to the work its agent did.
//
// This crosses the seam the defect hid in. The adapter decides what a record
// says the call was, and the relation decides which calls belong to it, and a
// launch that reached the log as an uninterpreted call is a launch no relation
// can hold however exactly its identity matches. Both sides are driven here
// through their own public behaviour, from the payload shape a capture
// produced, so neither can be corrected without the other noticing.
//
// The shape is the one observed against Claude Code 2.1.228: `description` and
// `prompt` in the input, no `subagent_type`, an identity in the response, and a
// nested call reporting that identity.
func TestTypelessLaunchRelatesToItsNestedWork(t *testing.T) {
	t.Parallel()

	launch := ingest(t, `{"hook_event_name":"PostToolUse","session_id":"abc","turn_id":"turn-1",
		"tool_name":"Agent","tool_use_id":"toolu_01",
		"tool_input":{"description":"check the config","prompt":"look at how the lifetime is used"},
		"tool_response":{"isAsync":true,"status":"async_launched","agentId":"ac21d414794fa0e11"}}`)

	if got := work.Of(launch.Tool); got != work.SubagentLaunch {
		t.Fatalf("the call was classified as %v, want a subagent launch", got)
	}

	nested := ingest(t, `{"hook_event_name":"PostToolUse","session_id":"abc","turn_id":"turn-1",
		"agent_id":"ac21d414794fa0e11","tool_name":"Read","tool_use_id":"toolu_02",
		"tool_input":{"file_path":"/repo/src/legacy.py"}}`)

	a := delegation.New()
	// Recorded in the order the capture produced: the launch reached the log
	// before the work its agent went on to do.
	a.Add(*launch)
	a.Add(*nested)
	report := a.Report()

	if len(report.Launches) != 1 {
		t.Fatalf("the log holds %d recognized launches, want the one that was recorded",
			len(report.Launches))
	}
	got := report.Launches[0]
	if got.Work == nil {
		t.Fatal("the launch returned an identity and was reported as having none to match on")
	}
	if got.Work.Calls != 1 || got.Work.Composition.WholeReads != 1 {
		t.Errorf("attributable work = %+v, want the one nested read", got.Work)
	}
	if report.Unrelated.Calls != 0 {
		t.Errorf("%d nested calls were left orphaned by a launch that accounts for them",
			report.Unrelated.Calls)
	}
}

// A launch that declared no type is persisted as a launch that declared no
// type. The field is omitted rather than written empty, which is the shape the
// model has always allowed, and no value is invented to fill it from the
// description, the prompt or the response.
func TestTypelessLaunchPersistsNoType(t *testing.T) {
	t.Parallel()

	ev := ingest(t, `{"hook_event_name":"PostToolUse","session_id":"abc","tool_name":"Agent",
		"tool_input":{"description":"check the config","prompt":"look"},
		"tool_response":{"status":"completed","agentId":"ac21d414794fa0e11","agentType":"Explore"}}`)

	encoded, err := json.Marshal(ev.Tool.Metadata)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"subagent":{}}`
	if string(encoded) != want {
		t.Errorf("metadata = %s, want %s", encoded, want)
	}
	if strings.Contains(string(encoded), "Explore") {
		t.Errorf("the type the response reported was read as the type declared:\n%s", encoded)
	}
}

// The correction is forward-only. A launch that reached the log before it as a
// call Axiom could not describe stays one: the tool name is persisted, and
// nothing reads a launch back out of it, because a name is not the evidence
// the metadata is.
func TestHistoricalUninterpretedLaunchIsNotReconstructed(t *testing.T) {
	t.Parallel()

	const historical = `{"schema_version":1,"agent":"claude-code","type":"tool_call",
		"timestamp":"2026-08-12T20:27:18Z","session_id":"abc","turn_id":"turn-1",
		"tool":{"name":"Agent","invocation_id":"toolu_01","outcome":"success",
		"result":{"subagent":{"agent_id":"ac21d414794fa0e11"}}}}`

	var ev event.Event
	if err := json.Unmarshal([]byte(historical), &ev); err != nil {
		t.Fatalf("a historical record no longer decodes: %v", err)
	}
	if got := work.Of(ev.Tool); got != work.Uninterpreted {
		t.Errorf("a historical record was reclassified as %v; it says what it said", got)
	}
}
