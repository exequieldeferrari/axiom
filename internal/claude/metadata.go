package claude

import (
	"encoding/json"

	"github.com/exequieldeferrari/axiom/internal/digest"
	"github.com/exequieldeferrari/axiom/internal/event"
)

// The subagent tool's names. Claude Code has shipped it under both, and the
// two places that recognize it read the same constants so that a third name
// cannot be learned by one and not the other.
const (
	toolAgent = "Agent"
	toolTask  = "Task"
)

// extractMetadata derives privacy-filtered metadata from a tool's input.
//
// Extraction is an allowlist. A tool Axiom does not recognise, including every
// MCP tool, contributes no metadata at all: a denylist would leak the first
// time an agent gained a tool we had not reviewed.
func extractMetadata(toolName string, input json.RawMessage) *event.ToolMetadata {
	if len(input) == 0 {
		return nil
	}

	switch toolName {
	case "Read":
		return fileMetadata(input, event.AccessRead)
	case "Write":
		return fileMetadata(input, event.AccessWrite)
	case "Edit":
		return fileMetadata(input, event.AccessEdit)
	case "Bash", "PowerShell":
		return shellMetadata(input)
	case "Grep":
		return searchMetadata(input, event.SearchContent)
	case "Glob":
		return searchMetadata(input, event.SearchFilename)
	case toolAgent, toolTask:
		return subagentMetadata(input)
	default:
		return nil
	}
}

// extractResult derives privacy-filtered detail from what a tool returned.
//
// This is the only place Axiom reads a tool's response, and it reads one field
// of one tool. A response is where a tool's output lives — file contents,
// command output, an agent's whole answer — so the rule here is narrower than
// the input allowlist rather than merely equal to it: one recognized tool, one
// named field, one opaque string, and no fallback that could widen it.
//
// Nothing here judges the call. A response is read whatever the record says
// became of the call, because an identity that was returned was returned, and
// treating its presence as proof the launch succeeded would answer a question
// the response was never asked.
func extractResult(toolName string, response json.RawMessage) *event.ToolResult {
	if len(response) == 0 {
		return nil
	}

	switch toolName {
	case toolAgent, toolTask:
		return subagentResult(response)
	default:
		return nil
	}
}

func fileMetadata(input json.RawMessage, access string) *event.ToolMetadata {
	var in struct {
		FilePath string `json:"file_path"`
		Offset   *int   `json:"offset"`
		Limit    *int   `json:"limit"`
	}
	if json.Unmarshal(input, &in) != nil || in.FilePath == "" {
		return nil
	}
	return &event.ToolMetadata{File: &event.FileOp{
		Path:   in.FilePath,
		Access: access,
		Offset: in.Offset,
		Limit:  in.Limit,
	}}
}

func shellMetadata(input json.RawMessage) *event.ToolMetadata {
	var in struct {
		Command         string `json:"command"`
		RunInBackground bool   `json:"run_in_background"`
	}
	if json.Unmarshal(input, &in) != nil || in.Command == "" {
		return nil
	}
	// Only the digest is kept. Commands routinely carry credentials, and
	// naming the executable would require parsing arbitrary shell syntax.
	return &event.ToolMetadata{Shell: &event.ShellOp{
		CommandDigest: digest.Command(in.Command),
		Background:    in.RunInBackground,
	}}
}

func searchMetadata(input json.RawMessage, kind string) *event.ToolMetadata {
	var in struct {
		Pattern    string `json:"pattern"`
		Path       string `json:"path"`
		Glob       string `json:"glob"`
		OutputMode string `json:"output_mode"`
	}
	if json.Unmarshal(input, &in) != nil || in.Pattern == "" {
		return nil
	}
	return &event.ToolMetadata{Search: &event.SearchOp{
		Kind:          kind,
		PatternDigest: digest.Pattern(in.Pattern),
		Root:          in.Path,
		Glob:          in.Glob,
		OutputMode:    in.OutputMode,
	}}
}

func subagentMetadata(input json.RawMessage) *event.ToolMetadata {
	var in struct {
		SubagentType string `json:"subagent_type"`
	}
	if json.Unmarshal(input, &in) != nil || in.SubagentType == "" {
		return nil
	}
	return &event.ToolMetadata{Subagent: &event.SubagentOp{Type: in.SubagentType}}
}

// subagentResult takes the nested agent's identity out of a launch's response.
//
// Two response shapes carry it and both were observed: a synchronous launch
// reports a completed agent, and an asynchronous one reports a launched agent
// and no result at all. The field is read by name, so the two need no cases
// here and a third shape carrying the same field would need none either.
//
// Everything else in the response is left where it is. The captures behind this
// held the prompt the agent was given, its answer, a description, a path to its
// output file, its token usage and its tool counts; none of that is read, and
// reading the identity does not require decoding any of it.
//
// A value that is absent, empty, or not a string leaves the identity
// unrecorded. A shape Axiom cannot read is never turned into an identity it
// might share with another agent.
func subagentResult(response json.RawMessage) *event.ToolResult {
	var out struct {
		AgentID json.RawMessage `json:"agentId"`
	}
	if json.Unmarshal(response, &out) != nil || len(out.AgentID) == 0 {
		return nil
	}

	var id string
	if json.Unmarshal(out.AgentID, &id) != nil || id == "" {
		return nil
	}
	return &event.ToolResult{Subagent: &event.SubagentResult{AgentID: id}}
}
