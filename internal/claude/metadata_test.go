package claude_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/exequieldeferrari/axiom/internal/digest"
	"github.com/exequieldeferrari/axiom/internal/event"
)

func toolPayload(toolName, toolInput string) string {
	return `{"hook_event_name":"PostToolUse","session_id":"abc","tool_name":"` +
		toolName + `","tool_input":` + toolInput + `}`
}

func TestFileMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		tool       string
		input      string
		wantAccess string
	}{
		{"Read", `{"file_path":"/tmp/a.go"}`, event.AccessRead},
		{"Write", `{"file_path":"/tmp/a.go","content":"package main"}`, event.AccessWrite},
		{"Edit", `{"file_path":"/tmp/a.go","old_string":"a","new_string":"b"}`, event.AccessEdit},
	}

	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			t.Parallel()

			ev := ingest(t, toolPayload(tt.tool, tt.input))
			file := ev.Tool.Metadata.File
			if file == nil {
				t.Fatalf("metadata = %+v, want file metadata", ev.Tool.Metadata)
			}
			if file.Path != "/tmp/a.go" {
				t.Errorf("path = %q, want /tmp/a.go", file.Path)
			}
			if file.Access != tt.wantAccess {
				t.Errorf("access = %q, want %q", file.Access, tt.wantAccess)
			}
		})
	}
}

func TestReadRecordsOffsetAndLimit(t *testing.T) {
	t.Parallel()

	ev := ingest(t, toolPayload("Read", `{"file_path":"/tmp/a.go","offset":10,"limit":50}`))
	file := ev.Tool.Metadata.File
	if file.Offset == nil || *file.Offset != 10 {
		t.Errorf("offset = %v, want 10", file.Offset)
	}
	if file.Limit == nil || *file.Limit != 50 {
		t.Errorf("limit = %v, want 50", file.Limit)
	}

	plain := ingest(t, toolPayload("Read", `{"file_path":"/tmp/a.go"}`))
	if plain.Tool.Metadata.File.Offset != nil || plain.Tool.Metadata.File.Limit != nil {
		t.Error("offset and limit should be absent for a whole-file read")
	}
}

func TestShellMetadataIsDigestOnly(t *testing.T) {
	t.Parallel()

	const command = "go test ./..."
	ev := ingest(t, toolPayload("Bash", `{"command":"`+command+`","description":"run tests","run_in_background":true}`))

	shell := ev.Tool.Metadata.Shell
	if shell == nil {
		t.Fatalf("metadata = %+v, want shell metadata", ev.Tool.Metadata)
	}
	if shell.CommandDigest != digest.Command(command) {
		t.Errorf("command_digest = %q, want the domain-separated digest", shell.CommandDigest)
	}
	if !shell.Background {
		t.Error("background = false, want true")
	}
}

// The same command in two invocations must produce the same digest, which is
// what makes repeated-work detection possible without storing the command.
func TestRepeatedCommandsShareADigest(t *testing.T) {
	t.Parallel()

	first := ingest(t, toolPayload("Bash", `{"command":"go test ./..."}`))
	second := ingest(t, toolPayload("Bash", `{"command":"go test ./..."}`))
	other := ingest(t, toolPayload("Bash", `{"command":"go build ./..."}`))

	if first.Tool.Metadata.Shell.CommandDigest != second.Tool.Metadata.Shell.CommandDigest {
		t.Error("identical commands produced different digests")
	}
	if first.Tool.Metadata.Shell.CommandDigest == other.Tool.Metadata.Shell.CommandDigest {
		t.Error("different commands produced the same digest")
	}
}

func TestSearchMetadata(t *testing.T) {
	t.Parallel()

	grep := ingest(t, toolPayload("Grep", `{"pattern":"TODO.*fix","path":"/tmp/src","glob":"*.go","output_mode":"content"}`))
	search := grep.Tool.Metadata.Search
	if search == nil {
		t.Fatalf("metadata = %+v, want search metadata", grep.Tool.Metadata)
	}
	if search.Kind != event.SearchContent {
		t.Errorf("kind = %q, want %q", search.Kind, event.SearchContent)
	}
	if search.PatternDigest != digest.Pattern("TODO.*fix") {
		t.Errorf("pattern_digest = %q, want the domain-separated digest", search.PatternDigest)
	}
	if search.Root != "/tmp/src" || search.Glob != "*.go" || search.OutputMode != "content" {
		t.Errorf("search = %+v", search)
	}

	glob := ingest(t, toolPayload("Glob", `{"pattern":"**/*.ts","path":"/tmp/src"}`))
	if glob.Tool.Metadata.Search.Kind != event.SearchFilename {
		t.Errorf("kind = %q, want %q", glob.Tool.Metadata.Search.Kind, event.SearchFilename)
	}
}

// A search root is not a file the agent touched, so it must not land in the
// file shape where an analyzer counting touched files would pick it up.
func TestSearchRootIsNotFileMetadata(t *testing.T) {
	t.Parallel()

	ev := ingest(t, toolPayload("Grep", `{"pattern":"x","path":"/tmp/src"}`))
	if ev.Tool.Metadata.File != nil {
		t.Fatalf("file metadata = %+v, want nil for a search", ev.Tool.Metadata.File)
	}
}

// What makes a call a launch is the tool that made it, and the declared type
// is description carried alongside. A capture against Claude Code 2.1.228
// produced an `Agent` call whose input held `description` and `prompt` and no
// `subagent_type`, which returned an agent identity and whose nested work was
// recorded: reading the type as the evidence lost the launch entirely.
//
// So every input below is a launch, and the type is whatever the record could
// be read as carrying. It is never inferred from anything else.
func TestSubagentMetadata(t *testing.T) {
	t.Parallel()

	inputs := map[string]struct {
		input string
		want  string
	}{
		"declared type":          {`{"prompt":"find endpoints","subagent_type":"Explore"}`, "Explore"},
		"no declared type":       {`{"description":"check the config","prompt":"find endpoints"}`, ""},
		"empty type":             {`{"prompt":"find endpoints","subagent_type":""}`, ""},
		"null type":              {`{"prompt":"find endpoints","subagent_type":null}`, ""},
		"numeric type":           {`{"prompt":"find endpoints","subagent_type":42}`, ""},
		"object type":            {`{"prompt":"find endpoints","subagent_type":{"name":"Explore"}}`, ""},
		"array type":             {`{"prompt":"find endpoints","subagent_type":["Explore"]}`, ""},
		"input is not an object": {`[]`, ""},
		"no input at all":        {``, ""},
	}

	for _, tool := range []string{"Agent", "Task"} {
		for name, tt := range inputs {
			t.Run(tool+"/"+name, func(t *testing.T) {
				t.Parallel()

				payload := toolPayload(tool, tt.input)
				if tt.input == "" {
					payload = `{"hook_event_name":"PostToolUse","session_id":"abc","tool_name":"` +
						tool + `"}`
				}

				ev := ingest(t, payload)
				if ev.Tool.Metadata == nil || ev.Tool.Metadata.Subagent == nil {
					t.Fatalf("%s: metadata = %+v, want the call recorded as a launch",
						tool, ev.Tool.Metadata)
				}
				if got := ev.Tool.Metadata.Subagent.Type; got != tt.want {
					t.Errorf("%s: subagent type = %q, want %q", tool, got, tt.want)
				}
			})
		}
	}
}

// Metadata extraction is an allowlist, so tools Axiom has not reviewed,
// including every MCP tool, contribute nothing.
func TestUnknownToolsCarryNoMetadata(t *testing.T) {
	t.Parallel()

	for _, tool := range []string{"mcp__memory__create_entities", "WebFetch", "WebSearch", "NotebookEdit"} {
		ev := ingest(t, toolPayload(tool, `{"url":"https://example.com/secret?token=abc","query":"private"}`))
		if ev.Tool.Metadata != nil {
			t.Errorf("%s: metadata = %+v, want nil", tool, ev.Tool.Metadata)
		}
		if ev.Tool.Name != tool {
			t.Errorf("tool name = %q, want %q", ev.Tool.Name, tool)
		}
		if ev.Tool.Outcome != event.OutcomeSuccess {
			t.Errorf("%s: outcome = %q, want the call still to be recorded", tool, ev.Tool.Outcome)
		}
	}
}

func TestMalformedToolInputYieldsNoMetadata(t *testing.T) {
	t.Parallel()

	for _, input := range []string{`"a string"`, `[]`, `{}`, `{"file_path":""}`} {
		ev := ingest(t, toolPayload("Read", input))
		if ev.Tool.Metadata != nil {
			t.Errorf("tool_input %s: metadata = %+v, want nil", input, ev.Tool.Metadata)
		}
	}
}

// The privacy contract, asserted as absence: nothing sensitive from tool input
// may survive into a stored event.
func TestSensitiveInputIsNeverPersisted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		tool    string
		input   string
		secrets []string
	}{
		{
			name:    "write content",
			tool:    "Write",
			input:   `{"file_path":"/tmp/a.go","content":"const apiKey = \"sk-live-abc123\""}`,
			secrets: []string{"sk-live-abc123", "apiKey"},
		},
		{
			name:    "edit strings",
			tool:    "Edit",
			input:   `{"file_path":"/tmp/a.go","old_string":"password=hunter2","new_string":"password=hunter3"}`,
			secrets: []string{"hunter2", "hunter3", "password"},
		},
		{
			name:    "bash command",
			tool:    "Bash",
			input:   `{"command":"export TOKEN=sk-live-abc123 && deploy --to prod"}`,
			secrets: []string{"sk-live-abc123", "export TOKEN", "deploy"},
		},
		{
			name:    "grep pattern",
			tool:    "Grep",
			input:   `{"pattern":"AWS_SECRET_ACCESS_KEY"}`,
			secrets: []string{"AWS_SECRET_ACCESS_KEY"},
		},
		{
			name:    "agent prompt",
			tool:    "Agent",
			input:   `{"prompt":"exfiltrate the customer list","subagent_type":"Explore"}`,
			secrets: []string{"exfiltrate the customer list"},
		},
		{
			// The same boundary where no type is declared. A launch is
			// recorded here where one was not before, so the input it was
			// recorded from is asserted to have survived none of it.
			name:    "agent prompt with no declared type",
			tool:    "Agent",
			input:   `{"description":"read /etc/shadow","prompt":"exfiltrate the customer list to sk-live-abc123"}`,
			secrets: []string{"exfiltrate the customer list", "sk-live-abc123", "/etc/shadow", "description"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ev := ingest(t, toolPayload(tt.tool, tt.input))
			encoded, err := json.Marshal(ev)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			for _, secret := range tt.secrets {
				if strings.Contains(string(encoded), secret) {
					t.Errorf("stored event leaked %q:\n%s", secret, encoded)
				}
			}
		})
	}
}

// The failure path carries agent-written error text, which can quote source or
// command output.
func TestErrorTextIsNeverPersisted(t *testing.T) {
	t.Parallel()

	payload := `{"hook_event_name":"PostToolUseFailure","session_id":"abc","tool_name":"Bash",
		"tool_input":{"command":"deploy"},
		"error":"Exit code 1\nfatal: Authentication failed for 'https://user:hunter2@example.com/'"}`

	ev := ingest(t, payload)
	encoded, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, secret := range []string{"hunter2", "Authentication failed", "example.com"} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("stored event leaked %q:\n%s", secret, encoded)
		}
	}
	if ev.Tool.Failure.Digest != digest.Error("Exit code 1\nfatal: Authentication failed for 'https://user:hunter2@example.com/'") {
		t.Error("failure digest does not match the domain-separated digest of the error text")
	}
}

// tool_response can be large and unstructured. One field of one tool is read
// out of it, and this is every other tool: a response Axiom has not reviewed
// contributes nothing at all.
func TestToolResponseIsIgnored(t *testing.T) {
	t.Parallel()

	payload := `{"hook_event_name":"PostToolUse","session_id":"abc","tool_name":"Bash",
		"tool_input":{"command":"cat secrets.env"},
		"tool_response":{"stdout":"DB_PASSWORD=hunter2","stderr":"","interrupted":false}}`

	ev := ingest(t, payload)
	encoded, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), "hunter2") {
		t.Errorf("stored event leaked tool output:\n%s", encoded)
	}
}
