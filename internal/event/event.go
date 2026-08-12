// Package event defines Axiom's canonical, agent-neutral event model.
//
// Events describe what an AI coding agent did, not how a particular agent
// reported it. Agent-specific payloads are translated into this model by an
// adapter before they reach storage.
package event

import "time"

// SchemaVersion is written with every event so that stored history remains
// readable after the model evolves.
const SchemaVersion = 1

// Type identifies what an event records.
type Type string

const (
	TypeSessionStart Type = "session_start"
	TypeSessionEnd   Type = "session_end"
	TypeToolCall     Type = "tool_call"
)

// Outcome records whether a tool call succeeded.
type Outcome string

const (
	OutcomeSuccess Outcome = "success"
	OutcomeFailure Outcome = "failure"
)

// Event is a single observation about an agent.
//
// SessionID is the agent's own session identifier and is not a durable unit of
// work. Claude Code was observed keeping it across compaction and across a
// resume, and reporting a new one on /clear and on a fork: one sitting can span
// several identifiers, and one identifier can span several contexts. Analysis
// that needs "one sitting" has to reconstruct it, and nothing recorded links one
// identifier to another.
type Event struct {
	SchemaVersion int       `json:"schema_version"`
	Agent         string    `json:"agent"`
	Type          Type      `json:"type"`
	Timestamp     time.Time `json:"timestamp"`

	SessionID  string `json:"session_id"`
	TurnID     string `json:"turn_id,omitempty"`
	SubagentID string `json:"subagent_id,omitempty"`
	Cwd        string `json:"cwd,omitempty"`

	Session *Session  `json:"session,omitempty"`
	Tool    *ToolCall `json:"tool,omitempty"`
}

// Version reports the schema this record was written under.
func (e Event) Version() int { return e.SchemaVersion }

// Session carries lifecycle detail for session_start and session_end events.
//
// A session_end event is not guaranteed to exist: agents may be killed, and
// Claude Code caps how long it waits for end-of-session hooks. Analysis must
// tolerate sessions that never end.
type Session struct {
	Source string `json:"source,omitempty"`
	Reason string `json:"reason,omitempty"`
	Model  string `json:"model,omitempty"`
}

// ToolCall describes one completed tool invocation.
//
// Only successful and started-then-failed calls are observable today. Calls
// rejected before execution, such as permission denials, are not recorded, so
// tool call counts are a lower bound.
type ToolCall struct {
	Name         string        `json:"name"`
	InvocationID string        `json:"invocation_id,omitempty"`
	Outcome      Outcome       `json:"outcome"`
	DurationMS   *int64        `json:"duration_ms,omitempty"`
	Failure      *Failure      `json:"failure,omitempty"`
	Metadata     *ToolMetadata `json:"metadata,omitempty"`
}

// Failure describes why a tool call failed. The agent's error text is never
// stored; Digest exists so that identical failures can be grouped.
type Failure struct {
	Kind     string `json:"kind"`
	ExitCode *int   `json:"exit_code,omitempty"`
	Digest   string `json:"digest,omitempty"`
}

const (
	FailureKindError     = "error"
	FailureKindInterrupt = "interrupt"
)

// ToolMetadata holds privacy-filtered detail derived from a tool's input.
// Exactly one field is non-nil.
//
// Metadata is grouped by operation shape rather than by tool name so that the
// model stays agent-neutral, and it is kept behind a single field so that a
// future strict mode can drop everything derived from tool input at once.
type ToolMetadata struct {
	File     *FileOp     `json:"file,omitempty"`
	Shell    *ShellOp    `json:"shell,omitempty"`
	Search   *SearchOp   `json:"search,omitempty"`
	Subagent *SubagentOp `json:"subagent,omitempty"`
}

// FileOp describes an operation against a single file.
type FileOp struct {
	Path   string `json:"path"`
	Access string `json:"access"`
	Offset *int   `json:"offset,omitempty"`
	Limit  *int   `json:"limit,omitempty"`
}

const (
	AccessRead  = "read"
	AccessWrite = "write"
	AccessEdit  = "edit"
)

// ShellOp describes a shell command execution. The command text is never
// stored; CommandDigest is enough to detect the same command running twice.
type ShellOp struct {
	CommandDigest string `json:"command_digest"`
	Background    bool   `json:"background,omitempty"`
}

// SearchOp describes a search. Root is where the search started, which is
// deliberately not the same concept as FileOp.Path.
type SearchOp struct {
	Kind          string `json:"kind"`
	PatternDigest string `json:"pattern_digest"`
	Root          string `json:"root,omitempty"`
	Glob          string `json:"glob,omitempty"`
	OutputMode    string `json:"output_mode,omitempty"`
}

const (
	SearchContent  = "content"
	SearchFilename = "filename"
)

// SubagentOp describes spawning a nested agent.
type SubagentOp struct {
	Type string `json:"type,omitempty"`
}
