package profiler_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/profiler"
)

// only returns the single finding a stream is expected to produce.
func only(t *testing.T, report profiler.Report) profiler.Finding {
	t.Helper()

	if len(report.Findings) != 1 {
		t.Fatalf("got %d findings, want 1:\n%+v", len(report.Findings), report.Findings)
	}
	return report.Findings[0]
}

// The strongest evidence Axiom can have: the same command, attempted again
// after failing the same way, with nothing in between it could not see.
func TestRepeatedFailureFinding(t *testing.T) {
	t.Parallel()

	f := only(t, analyze(newStream("session-1").inTurn("turn-1").
		shell("go-test", failing("same"), exiting(1), took(1200)).
		shell("go-test", failing("same"), exiting(1), took(500)).
		shell("go-test", failing("same"), exiting(1), took(400))))

	if f.Kind != profiler.KindRepeatedFailure {
		t.Errorf("Kind = %q", f.Kind)
	}
	if f.Confidence != profiler.ConfidenceHigh {
		t.Errorf("Confidence = %q, want high when every attempt failed alike", f.Confidence)
	}
	if f.SessionID != "session-1" {
		t.Errorf("SessionID = %q", f.SessionID)
	}
	if f.Occurrences != 3 || f.Redundant != 2 {
		t.Errorf("Occurrences = %d, Redundant = %d, want 3 and 2", f.Occurrences, f.Redundant)
	}
	if f.CommandDigest != "go-test" {
		t.Errorf("CommandDigest = %q", f.CommandDigest)
	}
	if f.FailureDigest != "same" {
		t.Errorf("FailureDigest = %q, want the failure they shared", f.FailureDigest)
	}
	if f.ExitCode == nil || *f.ExitCode != 1 {
		t.Errorf("ExitCode = %v, want 1", f.ExitCode)
	}
	if f.Path != "" {
		t.Errorf("Path = %q, want it empty for a command", f.Path)
	}
	// The first attempt was worth making; only the repeats are the finding.
	if f.ObservedTotal == nil || *f.ObservedTotal != 900*time.Millisecond {
		t.Errorf("ObservedTotal = %v, want 900ms", f.ObservedTotal)
	}
	if len(f.Calls) != 3 {
		t.Errorf("Calls = %+v, want one per attempt", f.Calls)
	}
	for i, c := range f.Calls {
		if c.TurnID != "turn-1" {
			t.Errorf("Calls[%d].TurnID = %q", i, c.TurnID)
		}
	}
	if f.LaterSuccess {
		t.Error("LaterSuccess = true, but the command was never observed succeeding")
	}
}

// Real commands print elapsed times, so two runs of one failing test command
// rarely produce identical text. The repetition is still established; that the
// failures were alike is not.
func TestDifferentFailuresLowerConfidence(t *testing.T) {
	t.Parallel()

	f := only(t, analyze(newStream("a").inTurn("t1").
		shell("go-test", failing("first"), exiting(1)).
		shell("go-test", failing("second"), exiting(1))))

	if f.Confidence != profiler.ConfidenceMedium {
		t.Errorf("Confidence = %q, want medium", f.Confidence)
	}
	if f.FailureDigest != "" {
		t.Errorf("FailureDigest = %q, want none when the attempts reported different failures", f.FailureDigest)
	}
	// The exit status agreed even though the text did not, and it is
	// recorded independently of it.
	if f.ExitCode == nil || *f.ExitCode != 1 {
		t.Errorf("ExitCode = %v, want 1", f.ExitCode)
	}
}

// An agent that reports no failure detail leaves Axiom unable to compare the
// attempts, which is the same position as knowing they differed.
func TestUnreportedFailuresLowerConfidence(t *testing.T) {
	t.Parallel()

	cases := map[string]*stream{
		"nothing reported at all": newStream("a").inTurn("t1").
			shell("go-test", failed).shell("go-test", failed),

		"reported for one attempt only": newStream("a").inTurn("t1").
			shell("go-test", failing("x")).shell("go-test", failed),

		"reported for the later attempt only": newStream("a").inTurn("t1").
			shell("go-test", failed).shell("go-test", failing("x")),
	}

	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := only(t, analyze(s))
			if f.Confidence != profiler.ConfidenceMedium {
				t.Errorf("Confidence = %q, want medium", f.Confidence)
			}
			if f.FailureDigest != "" {
				t.Errorf("FailureDigest = %q, want none", f.FailureDigest)
			}
			if f.ExitCode != nil {
				t.Errorf("ExitCode = %v, want none", *f.ExitCode)
			}
		})
	}
}

// A status that does not describe every attempt describes none of them.
func TestDisagreeingExitCodesAreWithheld(t *testing.T) {
	t.Parallel()

	f := only(t, analyze(newStream("a").inTurn("t1").
		shell("go-test", failing("x"), exiting(1)).
		shell("go-test", failing("x"), exiting(2))))

	if f.ExitCode != nil {
		t.Errorf("ExitCode = %d, want none when the attempts exited differently", *f.ExitCode)
	}
	// The failures themselves still matched, so the finding keeps its level.
	if f.Confidence != profiler.ConfidenceHigh {
		t.Errorf("Confidence = %q, want high", f.Confidence)
	}
}

// Looking at the tree cannot change it, so a sequence survives the agent
// investigating between attempts.
func TestReadOnlyWorkDoesNotEndTheSequence(t *testing.T) {
	t.Parallel()

	f := only(t, analyze(newStream("a").inTurn("t1").
		shell("go-test", failing("x")).
		read("/src/main.go").
		search("TODO").
		readRange("/src/main.go", 40).
		shell("go-test", failing("x"))))

	if f.Kind != profiler.KindRepeatedFailure || f.Occurrences != 2 {
		t.Errorf("Kind = %q, Occurrences = %d, want a repeated failure of 2", f.Kind, f.Occurrences)
	}
}

// A turn is where input Axiom cannot see may have arrived, so attempts either
// side of one are separate sequences rather than a single trajectory.
func TestASequenceStopsAtTheTurnBoundary(t *testing.T) {
	t.Parallel()

	report := analyze(newStream("a").inTurn("t1").
		shell("go-test", failing("x")).
		shell("go-test", failing("x")).
		inTurn("t2").
		shell("go-test", failing("x")).
		shell("go-test", failing("x")))

	if len(report.Findings) != 2 {
		t.Fatalf("got %d findings, want one per turn:\n%+v", len(report.Findings), report.Findings)
	}
	for i, f := range report.Findings {
		if f.Occurrences != 2 {
			t.Errorf("finding %d: Occurrences = %d, want 2", i, f.Occurrences)
		}
		if len(f.Calls) != 2 || f.Calls[0].TurnID != f.Calls[1].TurnID {
			t.Errorf("finding %d spans more than one turn: %+v", i, f.Calls)
		}
	}
}

// Both agents may run the same command, and neither is repeating the other:
// they reason in contexts of their own.
func TestNestedAgentsFailSeparately(t *testing.T) {
	t.Parallel()

	report := analyze(newStream("a").inTurn("t1").
		shell("go-test", failing("x")).
		shell("go-test", failing("x")).
		inSubagent("sub-1").
		shell("go-test", failing("x")).
		shell("go-test", failing("x")))

	if len(report.Findings) != 2 {
		t.Fatalf("got %d findings, want one per agent:\n%+v", len(report.Findings), report.Findings)
	}
	var parent, nested int
	for _, f := range report.Findings {
		if f.SubagentID == "" {
			parent++
		} else {
			nested++
		}
	}
	if parent != 1 || nested != 1 {
		t.Errorf("got %d findings for the session and %d for the subagent, want one each", parent, nested)
	}
}

func TestLaterSuccessIsObserved(t *testing.T) {
	t.Parallel()

	cases := map[string]*stream{
		"straight after the attempts": newStream("a").inTurn("t1").
			shell("go-test", failing("x")).
			shell("go-test", failing("x")).
			shell("go-test"),

		// The edit ends the sequence, so the finding is already reported by
		// the time the command is observed succeeding.
		"after work that ended the sequence": newStream("a").inTurn("t1").
			shell("go-test", failing("x")).
			shell("go-test", failing("x")).
			edit("/src/main.go").
			shell("go-test"),

		// The success arrives in a turn of its own, which bounds the
		// sequence but not what Axiom later saw of the same command.
		"in a later turn": newStream("a").inTurn("t1").
			shell("go-test", failing("x")).
			shell("go-test", failing("x")).
			inTurn("t2").
			shell("go-test"),
	}

	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := only(t, analyze(s))
			if f.Kind != profiler.KindRepeatedFailure {
				t.Fatalf("Kind = %q", f.Kind)
			}
			if !f.LaterSuccess {
				t.Error("LaterSuccess = false, but the same command was observed succeeding afterwards")
			}
		})
	}
}

// Nothing may be inferred from silence: these are all sequences Axiom never
// saw succeed, and none of them is evidence that the agent did not get past it.
func TestLaterSuccessIsNotAssumed(t *testing.T) {
	t.Parallel()

	cases := map[string]*stream{
		"never tried again": newStream("a").inTurn("t1").
			shell("go-test", failing("x")).
			shell("go-test", failing("x")),

		"only another command succeeded": newStream("a").inTurn("t1").
			shell("go-test", failing("x")).
			shell("go-test", failing("x")).
			shell("go-vet"),

		"the success came first": newStream("a").inTurn("t1").
			shell("go-test").
			shell("go-test", failing("x")).
			shell("go-test", failing("x")),

		"the success belongs to a later session": newStream("a").inTurn("t1").
			shell("go-test", failing("x")).
			shell("go-test", failing("x")).
			as("b").shell("go-test"),
	}

	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := only(t, analyze(s))
			if f.Kind != profiler.KindRepeatedFailure {
				t.Fatalf("Kind = %q", f.Kind)
			}
			if f.LaterSuccess {
				t.Error("LaterSuccess = true, but no success of that command was observed in the sequence's scope")
			}
		})
	}
}

// Outcomes a record can carry that say nothing about what became of the call.
// Nothing validates the field on the way into or out of the log, and "SUCCESS"
// is there because the value is compared exactly: a near miss is still unknown.
var unestablishedOutcomes = []string{"", "blocked", "timeout", "SUCCESS"}

// An outcome that was never established is not evidence of failure, so it cannot
// start a sequence of failed attempts no matter how often it repeats.
func TestAnUnestablishedOutcomeCannotStartASequenceOfFailures(t *testing.T) {
	t.Parallel()

	// The outcome is the claim that a call failed. Failure detail beside an
	// outcome that was never established does not make the claim: a record can
	// hold one field and not the other, and reading the detail as the outcome
	// would establish failure from something that is not it.
	shapes := map[string][]option{
		"with no failure detail":               {},
		"with failure detail beside it":        {failing("x")},
		"with an exit code beside it":          {exiting(1)},
		"with an interrupt reported beside it": {interrupted},
	}

	for _, state := range unestablishedOutcomes {
		for shape, detail := range shapes {
			t.Run(fmt.Sprintf("outcome %q %s", state, shape), func(t *testing.T) {
				t.Parallel()

				// The unestablished outcome is applied last so that it is what
				// the record ends up carrying.
				opts := append(append([]option{}, detail...), unestablished(state))
				report := analyze(newStream("a").inTurn("t1").
					shell("go-test", opts...).
					shell("go-test", opts...).
					shell("go-test", opts...))

				if len(report.Findings) != 0 {
					t.Errorf("got %d findings, want none: no attempt was observed failing:\n%+v",
						len(report.Findings), report.Findings)
				}
			})
		}
	}
}

// It cannot join one either. A sequence covers the attempts the agent reported
// failing, and a call whose result is unknown ends it rather than counting in it.
func TestAnUnestablishedOutcomeCannotJoinASequenceOfFailures(t *testing.T) {
	t.Parallel()

	for _, state := range unestablishedOutcomes {
		t.Run(fmt.Sprintf("outcome %q", state), func(t *testing.T) {
			t.Parallel()

			f := only(t, analyze(newStream("a").inTurn("t1").
				shell("go-test", failing("x"), exiting(1)).
				shell("go-test", failing("x"), exiting(1)).
				shell("go-test", unestablished(state)).
				shell("go-test", failing("x"), exiting(1))))

			if f.Kind != profiler.KindRepeatedFailure {
				t.Fatalf("Kind = %q", f.Kind)
			}
			if f.Occurrences != 2 || f.Redundant != 1 {
				t.Errorf("Occurrences = %d, Redundant = %d, want 2 and 1: only the attempts observed failing count",
					f.Occurrences, f.Redundant)
			}
			// The unknown outcome closed the sequence. Closing it is not the
			// same as observing the command get past the failure.
			if f.LaterSuccess {
				t.Error("LaterSuccess = true, but no success of that command was observed")
			}
		})
	}
}

// Nor may it be read as a success. Treating it as one would credit the command
// with an execution it cannot be shown to have completed.
func TestAnUnestablishedOutcomeIsNotASuccessEither(t *testing.T) {
	t.Parallel()

	for _, state := range unestablishedOutcomes {
		t.Run(fmt.Sprintf("outcome %q", state), func(t *testing.T) {
			t.Parallel()

			report := analyze(newStream("a").inTurn("t1").
				shell("go-test").
				shell("go-test", unestablished(state)).
				shell("go-test"))

			if len(report.Findings) != 0 {
				t.Errorf("got %d findings, want none: one of the three executions was never established:\n%+v",
					len(report.Findings), report.Findings)
			}
		})
	}
}

// The positive controls: an established outcome still decides everything it did
// before, so the fix above cannot have been made by silencing the analysis.
func TestEstablishedOutcomesStillDecideTheFinding(t *testing.T) {
	t.Parallel()

	t.Run("failure", func(t *testing.T) {
		t.Parallel()

		f := only(t, analyze(newStream("a").inTurn("t1").
			shell("go-test", failing("x"), exiting(1)).
			shell("go-test", failing("x"), exiting(1)).
			shell("go-test", failing("x"), exiting(1))))

		if f.Kind != profiler.KindRepeatedFailure || f.Occurrences != 3 {
			t.Errorf("Kind = %q with %d occurrences, want a repeated failure of 3", f.Kind, f.Occurrences)
		}
	})

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		f := only(t, analyze(newStream("a").inTurn("t1").
			shell("go-test").
			shell("go-test").
			shell("go-test")))

		if f.Kind != profiler.KindRepeatedShell || f.Occurrences != 3 {
			t.Errorf("Kind = %q with %d occurrences, want a repeated shell operation of 3", f.Kind, f.Occurrences)
		}
	})

	t.Run("a success after failures is still observed", func(t *testing.T) {
		t.Parallel()

		f := only(t, analyze(newStream("a").inTurn("t1").
			shell("go-test", failing("x")).
			shell("go-test", failing("x")).
			shell("go-test")))

		if !f.LaterSuccess {
			t.Error("LaterSuccess = false, but the command was observed succeeding afterwards")
		}
	})
}

// A sum missing part of itself would understate the time without saying so.
func TestUntimedAttemptsWithholdTheTotal(t *testing.T) {
	t.Parallel()

	f := only(t, analyze(newStream("a").inTurn("t1").
		shell("go-test", failing("x"), took(900)).
		shell("go-test", failing("x"), untimed)))

	if f.ObservedTotal != nil {
		t.Errorf("ObservedTotal = %v, want none when an attempt was recorded without a duration", *f.ObservedTotal)
	}
}

// Repeated failures and repeated work are separate findings about separate
// behavior, and one must not swallow the other.
func TestFailuresAndRedundancyAreReportedApart(t *testing.T) {
	t.Parallel()

	report := analyze(newStream("a").inTurn("t1").
		shell("go-test", failing("x")).
		shell("go-test", failing("x")).
		read("/src/main.go").
		read("/src/main.go"))

	if len(report.Findings) != 2 {
		t.Fatalf("got %d findings, want 2:\n%+v", len(report.Findings), report.Findings)
	}
	kinds := map[profiler.Kind]int{}
	for _, f := range report.Findings {
		kinds[f.Kind]++
	}
	if kinds[profiler.KindRepeatedFailure] != 1 || kinds[profiler.KindRepeatedRead] != 1 {
		t.Errorf("got %v, want one repeated failure and one repeated read", kinds)
	}
}
