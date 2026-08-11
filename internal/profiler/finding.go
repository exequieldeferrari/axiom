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

// Confidence describes how well the recorded evidence supports a finding. It
// is not severity: it says nothing about how much the repetition cost.
type Confidence string

const (
	// ConfidenceHigh means the repetition happened inside one context scope
	// and every operation between the repeats is known to leave the observed
	// state unchanged, so there was no window in which a change could have
	// escaped observation. A repeated failure is high only when every attempt
	// also reported the same failure.
	ConfidenceHigh Confidence = "high"

	// ConfidenceMedium means the repetition itself is established on the same
	// terms, but the evidence that the attempts were alike stops short of the
	// failures themselves: they were reported differently, or at least one was
	// not reported at all. Only repeated failures reach this level.
	ConfidenceMedium Confidence = "medium"
)

// Finding is a single piece of evidence about repeated work.
//
// A finding describes what was observed, never what to do about it.
type Finding struct {
	Kind       Kind
	Confidence Confidence
	SessionID  string
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

	// FailureDigest identifies the failure every attempt of a
	// KindRepeatedFailure run reported, and is empty when they differed or
	// when one of them reported none.
	//
	// It says that the agent described the failures the same way, and only
	// that. The error text is never recorded, so nothing here establishes
	// that the attempts failed for the same reason.
	FailureDigest string

	// ExitCode is the status every attempt of a KindRepeatedFailure run
	// exited with, and is nil when they differed or when one of them reported
	// none. A missing exit code is not a zero one.
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
}

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
