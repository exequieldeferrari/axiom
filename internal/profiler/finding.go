// Package profiler turns canonical events into evidence-based findings.
//
// The profiler consumes only the agent-neutral event model, so any agent with
// an adapter feeds the same analysis. It prefers false negatives: a finding is
// produced only when the recorded evidence rules out the ordinary reasons an
// operation would legitimately repeat.
package profiler

import "time"

// Kind identifies what a finding describes.
type Kind string

const (
	// KindRepeatedShell reports the same shell command running more than once.
	KindRepeatedShell Kind = "repeated_shell"
	// KindRepeatedRead reports the same file read more than once.
	KindRepeatedRead Kind = "repeated_read"
	// KindRepeatedFailure reports the same shell command being attempted
	// again after it failed.
	KindRepeatedFailure Kind = "repeated_failure"
)

// FailureReporting is what the attempts of a KindRepeatedFailure run were
// observed reporting about their failures, folded across all of them.
//
// It describes the reports and never the commands behind them, and it is not
// an ordering: no value here is better evidence than another. What it is for
// is to keep the reader from reading a report Axiom could not classify, or one
// that carried nothing, as though the agent had described the failure.
type FailureReporting string

const (
	// FailureReportingDetail means every attempt's report carried content
	// beyond a recognized exit status. It says nothing about what that
	// content was or whether the attempts described the same thing.
	FailureReportingDetail FailureReporting = "detail"

	// FailureReportingStatusOnly means every attempt's report was a
	// recognized exit status and nothing else. The commands themselves may
	// have produced anything at all.
	FailureReportingStatusOnly FailureReporting = "status_only"

	// FailureReportingNoText means no attempt was reported with any text.
	FailureReportingNoText FailureReporting = "no_text"

	// FailureReportingMixed means the attempts were classified and did not
	// all land in the same state. It is kept rather than collapsed into any
	// of them, because a run where one attempt described its failure and
	// another did not is neither.
	FailureReportingMixed FailureReporting = "mixed"

	// FailureReportingUnestablished means at least one attempt could not be
	// placed: its report was in a shape the adapter does not classify, or the
	// record predates the classification. One such attempt leaves the whole
	// question open, because a run cannot be described as reporting alike
	// when one of its reports was never read.
	FailureReportingUnestablished FailureReporting = "unestablished"
)

// ReportIdentity is what the record establishes about whether the attempts of
// a KindRepeatedFailure run reported their failures in the same words.
//
// It is a statement about text and nothing else. Identical reports are not
// evidence of a shared reason for failing, and differing reports are not
// evidence against one: reports were observed differing over an elapsed time
// and an output path while naming the same failing assertion. Neither value
// grades the finding.
type ReportIdentity string

const (
	// ReportsIdentical means every attempt reported a failure and all of the
	// reports produced the same digest.
	ReportsIdentical ReportIdentity = "identical"
	// ReportsDiffered means every attempt reported a failure and at least two
	// of the reports produced different digests.
	ReportsDiffered ReportIdentity = "differed"
	// ReportsUnestablished means at least one attempt reported no text, so
	// there was nothing to compare. It is held apart from ReportsDiffered:
	// having no report is not having a different one.
	ReportsUnestablished ReportIdentity = "unestablished"
)

// Finding is a single piece of evidence about repeated work.
//
// A finding describes what was observed, never what to do about it. Findings
// carry no grade: every one of them is established by the same deterministic
// rules, and a run that does not meet them is not reported at all.
type Finding struct {
	Kind      Kind
	SessionID string
	// SubagentID names the nested agent the work belongs to, empty when the
	// session's own agent did it. A subagent reasons in its own context, so
	// attributing its repetition to the session would name the wrong actor.
	SubagentID string

	// Occurrences counts the operations in the run, and Redundant is how many
	// of them repeated something the run had already done: work that had
	// already produced a result, or an attempt that had already failed.
	Occurrences int
	Redundant   int

	// Calls identifies every occurrence in the order observed. Calls[0] did
	// the work first and so repeated nothing; the rest are the redundant
	// ones. Identity is recorded here, while the run is being observed,
	// because nothing later can recover which occurrence was which: the two
	// recorded streams are written independently and their timestamps
	// interleave.
	Calls []Call

	// First and Last bound the run.
	First time.Time
	Last  time.Time

	// ObservedTotal is the tool execution time of the repeated occurrences,
	// excluding the first, or nil when any of them was recorded without a
	// duration. It is not the total time of the operation, and it measures
	// nothing about context, tokens, or cost.
	ObservedTotal *time.Duration

	// Path identifies the file for KindRepeatedRead.
	Path string
	// CommandDigest identifies the command for KindRepeatedShell and
	// KindRepeatedFailure. The command itself is never recorded and cannot be
	// recovered from the digest.
	CommandDigest string

	// Reporting and Reports are what a KindRepeatedFailure run's attempts
	// were observed reporting, and whether those reports came out the same.
	//
	// The two are independent: a run can report detail every time and still
	// differ, and a run that reports nothing but a status can match exactly.
	// Neither is a measure of the other, and neither grades the finding.
	Reporting FailureReporting
	Reports   ReportIdentity

	// ExitCode is the status every attempt of a KindRepeatedFailure run
	// exited with, and is nil when they differed or when one of them reported
	// none. A missing exit code is not a zero one.
	//
	// It is read out of the same reported text the fields above describe, so
	// it is not separate corroboration of them.
	ExitCode *int

	// LaterSuccess reports that the same command was afterwards observed
	// succeeding in the same scope.
	//
	// It is an observation about a later attempt and nothing more. What
	// happened in between is not evidence of what made the difference, so the
	// field must never be presented as recovery. Its absence carries no
	// meaning at all: a command that is never tried again simply leaves
	// Axiom with nothing to report.
	LaterSuccess bool

	// Interval is what Axiom recorded between the last attempt and that
	// success, and is nil unless LaterSuccess.
	Interval *Interval
}

// Interval is the tool calls recorded in one scope between the last attempt of
// a sequence of failed attempts and the first later observed success of the
// same command.
//
// It is an ordering of recorded calls and nothing else. None of it is
// established to have made the difference, no part of it is established to
// have been needed, and a smaller interval is not a better one. What the calls
// left behind is outside the record: the log says what became of a call and
// never what it returned or wrote.
//
// The counts describe calls that reached the log. A call rejected before it
// ran is never recorded, and a command can change state that no tool call
// reports, so an interval is a lower bound on what happened between the two
// observations.
type Interval struct {
	// Operations counts every recorded call in the interval, and equals the
	// sum of the categories below. They partition the calls by the shape of
	// the operation each carried, so the total can be reconciled against
	// them.
	Operations int

	// WholeReads and RangedReads count reads of a whole file and of part of
	// one. They are apart because they acquire different things, which is the
	// distinction the rest of Axiom already draws.
	WholeReads  int
	RangedReads int
	// Searches count recorded searches, and Shell counts recorded commands
	// other than the one the finding is about. A command's text is never
	// recorded, so nothing here says what any of them did.
	Searches int
	Shell    int
	// Writes and Edits stay apart because the record distinguishes them.
	Writes Outcomes
	Edits  Outcomes
	// Subagents counts calls that started a nested agent. The nested agent's
	// own calls are recorded against a scope of its own and are not here.
	Subagents int
	// Uninterpreted counts recorded calls this version cannot describe: a
	// tool outside the metadata allowlist, an MCP tool, and input it could
	// not parse. They are counted rather than dropped, because an interval
	// that omitted them would describe less work than was recorded while
	// looking complete.
	Uninterpreted int

	// Paths names the files that recorded writes or edits in the interval, in
	// the order they were first recorded, as the exact strings the agent
	// named. Retention is bounded, and OmittedPaths counts the distinct paths
	// left out.
	//
	// A path here says a call was recorded at it. It does not say the file
	// changed, and it is not the reason the later attempt was observed
	// succeeding.
	Paths        []string
	OmittedPaths int

	// TurnBoundary says what the record establishes about turn boundaries
	// falling between the two observations.
	TurnBoundary TurnBoundary
}

// Outcomes counts recorded calls by what the record established became of
// them.
//
// The three are kept apart because an outcome that was never established is
// not a failure. Folding it into either would infer one from missing evidence,
// and a failed write may still have applied in part, so neither is it nothing.
type Outcomes struct {
	Succeeded     int
	Failed        int
	Unestablished int
}

// Total counts the calls recorded, whatever became of them.
func (o Outcomes) Total() int { return o.Succeeded + o.Failed + o.Unestablished }

// TurnBoundary says what the record establishes about turn boundaries inside
// an interval.
//
// A turn boundary is where input Axiom does not observe may have arrived,
// which is why a sequence of failed attempts is confined to one turn. An
// interval is not, so whether one fell inside it is reported rather than
// assumed either way.
//
// The question is asked of the closed span from the last attempt through the
// first later success, both included. Their own turns are part of it: a
// boundary between the two falls inside the interval even where nothing was
// recorded between them. Every consecutive pair in that span is compared, and
// no call outside it is.
type TurnBoundary string

const (
	// TurnBoundaryNone means every call in the span, both ends included,
	// reported a turn, and each was the same as the one before it.
	TurnBoundaryNone TurnBoundary = "none"
	// TurnBoundaryRecorded means every call in the span reported a turn and
	// at least one differed from the one before it.
	TurnBoundaryRecorded TurnBoundary = "recorded"
	// TurnBoundaryUnknown means a call in the span reported no turn, leaving
	// a pair that cannot be compared. It is not the absence of a boundary.
	//
	// A missing identifier counts the same wherever it falls in the span,
	// including on either end. Comparing across the gap to the nearest call
	// that did report one would compare two calls that were never adjacent,
	// and answer from evidence the record does not hold.
	TurnBoundaryUnknown TurnBoundary = "unknown"
)

// Call identifies one occurrence of a repeated operation.
//
// The identifiers are the agent's own, carried through unchanged, so that a
// measurement the same agent reported elsewhere can be attached to the exact
// occurrence it belongs to. A finding remains complete evidence without them:
// an agent that reports no identifiers still produces findings, which simply
// cannot be joined to anything.
type Call struct {
	TurnID       string
	InvocationID string
}

// Report summarizes one analysis of the event log.
type Report struct {
	Events    int
	Sessions  int
	ToolCalls int
	Findings  []Finding
}
