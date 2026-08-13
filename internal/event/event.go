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

	// Harness is what Axiom observed of the agent's project-local
	// configuration at the moment this start was recorded. It is set on a
	// session start and on nothing else.
	//
	// Absence means no provenance was recorded, which is what every record
	// written before this field existed says, what a start Axiom could not
	// resolve a project for says, and what a start recorded by an Axiom that
	// did not observe any of this says. It never means that the agent ran
	// with no configuration.
	Harness *Harness `json:"harness,omitempty"`
}

// Harness is the observable configuration Axiom found for itself when a
// session start was recorded.
//
// The claim is exactly this: at that moment, at the project root Axiom
// resolved from the session's working directory, each named path was in the
// state recorded beside it. Nothing here is the configuration the agent
// loaded. An agent reads configuration Axiom does not look at, from scopes
// Axiom cannot establish, and reaches further from the files it does read;
// none of that is observable from a hook, so none of it is claimed.
//
// Two records whose components match establish that these paths held the same
// bytes. They do not establish that two sessions ran under the same harness,
// and neither matching nor differing components establish anything about how
// either session behaved.
type Harness struct {
	// Components are the observations, one per eligible path, in a fixed
	// order that does not depend on the filesystem.
	//
	// The set of paths is the set this Axiom looked at. A component that is
	// not listed was not observed, which is a different fact from a
	// component listed as absent: the first says Axiom never looked, the
	// second says Axiom looked and found nothing there.
	Components []HarnessComponent `json:"components"`
}

// HarnessComponent is one observed path.
type HarnessComponent struct {
	Kind HarnessKind `json:"kind"`

	// Path is where the component was looked for, relative to the resolved
	// project root and always slash-separated. The root itself is not
	// recorded here; the session's working directory already is.
	//
	// It is the path that was looked at and never the path the bytes came
	// from. Where the observer followed a symlink within the project the
	// two differ, and only this one is recorded: where a link led is not a
	// fact about the project, and recording it would put a path Axiom was
	// asked to resolve into the log.
	Path string `json:"path"`

	Status HarnessStatus `json:"status"`

	// Digest identifies the file's exact bytes and is set only where a file
	// was observed. It is a digest and never a content sample: the bytes
	// themselves are read, hashed, and dropped.
	Digest string `json:"digest,omitempty"`
}

// HarnessKind says what an observed path is to the agent. It is recorded
// rather than derived from the path later, so that a report never has to
// recognize a component by parsing a string.
type HarnessKind string

const (
	// HarnessProjectInstructions is the project's instruction file.
	HarnessProjectInstructions HarnessKind = "project_instructions"
	// HarnessProjectSettings is the project's shared settings file.
	HarnessProjectSettings HarnessKind = "project_settings"
	// HarnessLocalProjectSettings is the project's local settings file,
	// which is where Axiom installs itself by default.
	HarnessLocalProjectSettings HarnessKind = "local_project_settings"
	// HarnessSubagentDirectory is the directory the project's subagent
	// definitions live in. It carries no digest: it is the record that
	// enumeration was attempted and what it found.
	HarnessSubagentDirectory HarnessKind = "subagent_directory"
	// HarnessSubagentDefinition is one subagent definition file.
	HarnessSubagentDefinition HarnessKind = "subagent_definition"
)

// HarnessStatus is what Axiom established about a path.
//
// They are held apart because they answer different questions, and collapsing
// any two of them would turn a limit of the observation into a statement about
// the project.
type HarnessStatus string

const (
	// HarnessObserved means Axiom read the path. A file carries the digest
	// of what it read; a directory carries the definitions it enumerated.
	HarnessObserved HarnessStatus = "observed"

	// HarnessAbsent means nothing was there to read. It is a fact about
	// that one path at that one moment, and says nothing about whether the
	// agent had such configuration somewhere Axiom does not look.
	HarnessAbsent HarnessStatus = "absent"

	// HarnessUnreadable means something was there and Axiom did not
	// establish its identity: a path it could not read, a path that was not
	// a regular file, a file past the size it will read, or a directory
	// holding more definitions than it will enumerate.
	//
	// It is deliberately not absent. A component Axiom could not read is
	// not a component that was not there.
	HarnessUnreadable HarnessStatus = "unreadable"

	// HarnessNotFollowed means the path is a symbolic link Axiom did not
	// read through. A link is followed only where it stays inside the
	// project; one that leads out of it is not, and neither is an absolute
	// one, so that a repository cannot name a file elsewhere on the machine
	// and have Axiom read it.
	//
	// It is deliberately neither absent nor unreadable. Something is there,
	// and what stopped the observation was the observer.
	//
	// The record does not say where the link led, and does not distinguish
	// a link that left the project from one inside it whose target would
	// not open. Both are links Axiom did not read through; telling them
	// apart in the record would mean saying something about the target.
	HarnessNotFollowed HarnessStatus = "not_followed"
)

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
	Result       *ToolResult   `json:"result,omitempty"`
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

// ToolResult holds privacy-filtered detail derived from what a tool returned.
//
// It is held apart from ToolMetadata, which is derived from what a tool was
// given, so that the provenance of every stored value is legible from where it
// sits: one side is what the agent asked for, the other is what came back. The
// two are separate fields rather than one so that a future strict mode can drop
// either without dropping the other.
//
// A tool's response is the largest and least structured thing a hook carries.
// Nothing here is a general reading of one: exactly one value is extracted,
// from one recognized shape, by the same allowlist discipline ToolMetadata uses.
type ToolResult struct {
	Subagent *SubagentResult `json:"subagent,omitempty"`
}

// SubagentResult is the identity an agent reported for the nested agent a
// launch created.
//
// This is not Event.SubagentID, and the two must never be read as one value.
// Event.SubagentID names the agent that MADE a call. AgentID here names the
// agent a call CREATED. On one launch record they describe different agents:
// a nested agent that launches another carries its own identity in
// Event.SubagentID and the identity it created here.
//
// It exists so that the calls a launched agent went on to make can be
// recognized as the ones that reported this identity. It establishes nothing
// else: not that the agent did any work, not that the work it did was
// recorded, and not that the launch achieved what it was asked for.
type SubagentResult struct {
	// AgentID is the agent's own opaque handle for the nested agent, stored
	// verbatim and never parsed.
	//
	// Absence means no identity was recorded, which is what every record
	// written before this field existed says, and what a launch that reported
	// failing says. It never means that no agent existed.
	AgentID string `json:"agent_id,omitempty"`
}
