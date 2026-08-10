package claude

import (
	"encoding/json"

	"github.com/exequieldeferrari/axiom/internal/digest"
	"github.com/exequieldeferrari/axiom/internal/event"
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
	case "Agent", "Task":
		// Claude Code has shipped the subagent tool under both names.
		return subagentMetadata(input)
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
