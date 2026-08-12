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
// stored; Digest exists so that identical reports can be grouped, and
// Reporting records what the adapter could establish about the text before it
// was discarded.
type Failure struct {
	Kind     string `json:"kind"`
	ExitCode *int   `json:"exit_code,omitempty"`
	Digest   string `json:"digest,omitempty"`
	// Reporting is derived by the adapter while the report is still in hand.
	// It is empty in every record written before the field existed, which
	// means no classification was recorded and never that one was negative.
	Reporting Reporting `json:"reporting,omitempty"`
}

const (
	FailureKindError     = "error"
	FailureKindInterrupt = "interrupt"
)

// Reporting is what an adapter could establish about the failure report an
// agent handed it, derived at ingestion and recorded in place of the text.
//
// Every value describes the report and never the command behind it. Whether a
// report carried anything is not whether the command produced anything: the
// text is the agent's own summary of the call, whitespace-only output was
// observed being stripped out of it before Axiom saw it, and a command can
// write where no report describes.
//
// An empty value is not one of the states below. It means the record carries
// no classification, which is what every record written before this field
// existed says, and reading it as any of them would answer from evidence that
// was never recorded.
type Reporting string

const (
	// ReportingDetail means the report carried content beyond a recognized
	// exit-status representation.
	//
	// It says nothing about what that content was, how much of it there was,
	// where it came from, or whether it describes the failure at all. A
	// command's ordinary output reaches a report the same way an error
	// message does.
	ReportingDetail Reporting = "detail"

	// ReportingStatusOnly means the whole report was a recognized
	// exit-status representation and nothing else.
	//
	// It establishes that the agent reported nothing beyond the status. It
	// does not establish that the command was silent, that either output
	// stream was empty, or that no description of the failure existed
	// somewhere Axiom cannot see.
	ReportingStatusOnly Reporting = "status_only"

	// ReportingUnrecognized means a report was present and this adapter could
	// not classify it. Failures outside the shell reach it, and so does a
	// shell report in a shape the adapter does not know.
	//
	// It is the conservative outcome, and it is deliberately not
	// ReportingStatusOnly: a report Axiom cannot read is not a report that
	// said nothing.
	ReportingUnrecognized Reporting = "unrecognized"

	// ReportingNoText means the agent reported a failure and gave no text
	// with it. It is held apart from a report that could not be classified,
	// because reporting nothing and reporting something unreadable are
	// different observations.
	ReportingNoText Reporting = "no_text"
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
