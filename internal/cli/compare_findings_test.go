package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/analysis"
	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/profiler"
)

// The digests the capture below repeats. They are distinct so that the
// successful command and the failing one accumulate separately.
const (
	repeatedDigest = "1f0e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"
	failingDigest  = "2e1d0c3b4a59687796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f001"
)

// findingCapture is a capture that produces all three kinds of finding.
//
// The order is load-bearing. Two reads of one path open a sequence of repeated
// reads; the first shell call ends it, because a command is a barrier for a
// read and not for another command. Two calls of one digest are then a
// repeated shell operation, which the first failing call ends. Two failures of
// one digest in one turn are a repeated failed attempt.
func findingCapture(t *testing.T, session string) string {
	t.Helper()

	shell := func() event.ToolCall {
		return event.ToolCall{
			Name: "Bash", Outcome: event.OutcomeSuccess, DurationMS: ms(20),
			Metadata: &event.ToolMetadata{Shell: &event.ShellOp{CommandDigest: repeatedDigest}},
		}
	}
	failed := func(invocation string) event.ToolCall {
		return event.ToolCall{
			Name: "Bash", InvocationID: invocation,
			Outcome: event.OutcomeFailure, DurationMS: ms(30),
			Failure: &event.Failure{
				Kind: event.FailureKindError, Digest: failureDigest,
				Reporting: event.ReportingDetail, ExitCode: exitCode(1),
			},
			Metadata: &event.ToolMetadata{Shell: &event.ShellOp{CommandDigest: failingDigest}},
		}
	}

	return seed(t,
		startEvent(session, "startup", at(0)),
		callEvent(session, "turn-1", "", at(time.Second), wholeRead("/repo/pkg/service.go")),
		callEvent(session, "turn-1", "", at(2*time.Second), wholeRead("/repo/pkg/service.go")),
		callEvent(session, "turn-1", "", at(3*time.Second), shell()),
		callEvent(session, "turn-1", "", at(4*time.Second), shell()),
		callEvent(session, "turn-1", "", at(5*time.Second), failed("inv-1")),
		callEvent(session, "turn-1", "", at(6*time.Second), failed("inv-2")),
	)
}

// findingsIn reports the kinds of finding a capture holds, so that a guard
// over a capture holding none cannot pass by accident.
func findingsIn(t *testing.T, dir string) map[profiler.Kind]int {
	t.Helper()

	log, err := analysis.Analyze(dir, analysis.Options{})
	if err != nil {
		t.Fatalf("Analyze(%s): %v", dir, err)
	}
	kinds := make(map[profiler.Kind]int)
	for _, f := range log.Findings.Findings {
		kinds[f.Kind]++
	}
	return kinds
}

// findingRenderings is every concrete thing a finding is called, read from the
// packages that own the names rather than copied, so that a renamed kind or a
// reworded headline stays covered.
//
// Only renderings a finding alone produces are listed. A loose word like
// "repeated" or "findings" appears in the comparison's own prose, which
// explains why findings are not compared, and asserting on those would fail on
// a rewording rather than on a leak.
func findingRenderings() []string {
	names := []string{
		// What a finding carries. Each label is printed only under a
		// finding, and several of them name a quantity that this
		// comparison has no way to establish across two captures.
		"Potentially redundant reads",
		"Potentially redundant executions",
		"Failed attempts",
		"Repeated after a failure",
		"Failure reporting",
		"Same exit code",
		"Redundant tool output",
		"Repeated-call tool time",
		"Command digest",
		// What a finding states, minus the count.
		"with no agent modification observed in between",
		"with only read-only operations in between",
	}
	for _, k := range []profiler.Kind{
		profiler.KindRepeatedRead,
		profiler.KindRepeatedShell,
		profiler.KindRepeatedFailure,
	} {
		names = append(names, string(k), headline(k))
	}
	return names
}

// A finding is a statement about one uninterrupted sequence of repeated calls,
// and where that sequence ends is decided by the recording rather than by the
// work: a context reset, an agent scope, a turn boundary or one intervening
// operation forms a finding, shortens it, or prevents it entirely. So a
// finding held by one capture and not the other establishes nothing about a
// difference in behavior, and a missing finding is not an established zero.
//
// ADR 0020 refuses the comparison for that reason. This pins the refusal at
// the only place it can be observed: whatever the profiler finds in either
// capture, none of it may reach the comparison — not as a section, not as a
// count, and not as a single detail line a reader could subtract by eye.
func TestCompareCarriesNoFindingDetail(t *testing.T) {
	t.Parallel()

	withFindings := findingCapture(t, sessionA)
	alsoWithFindings := findingCapture(t, sessionB)
	// Two reads of different paths and one command repeat nothing, so this
	// side holds no finding at all. It is the asymmetry the refusal is
	// about: one capture with findings against one without.
	withoutFindings := delegationCapture(t, sessionB, 1)

	// Without this the rest of the test would pass over captures that
	// simply have nothing to leak.
	kinds := findingsIn(t, withFindings)
	for _, want := range []profiler.Kind{
		profiler.KindRepeatedRead,
		profiler.KindRepeatedShell,
		profiler.KindRepeatedFailure,
	} {
		if kinds[want] == 0 {
			t.Fatalf("the capture holds no %s finding, so this guard would prove nothing: %v", want, kinds)
		}
	}
	if n := len(findingsIn(t, withoutFindings)); n != 0 {
		t.Fatalf("the capture meant to hold no finding holds %d kinds", n)
	}

	// Every shape that writes the report. The order of the two sides is the
	// operator's, so a side that holds findings is put in each position.
	reports := map[string]string{
		"findings on both sides":         compareOutput(t, sides(withFindings, alsoWithFindings)),
		"findings in the baseline":       compareOutput(t, sides(withFindings, withoutFindings)),
		"findings in the candidate":      compareOutput(t, sides(withoutFindings, withFindings)),
		"a capture compared with itself": compareOutput(t, sides(withFindings, withFindings)),
	}
	// A refusal writes no report, and its reason is the other text a caller
	// reads. Nothing a finding is called belongs in it either.
	reports["a refused comparison"] = compareRefusal(t, sides(withFindings, t.TempDir()))

	for shape, out := range reports {
		for _, forbidden := range findingRenderings() {
			if strings.Contains(out, forbidden) {
				t.Errorf("%s carries finding detail (%q):\n%s", shape, forbidden, out)
			}
		}
	}
}
