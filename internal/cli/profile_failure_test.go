package cli

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/event"
)

const (
	commandDigest = "c10ec4b070ab5f3d9e2a1b8c7d6e5f4a3b2c1d0e9f8a7b6c5d4e3f2a1b0c9d8e"
	failureDigest = "30303e9585c1f2a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f8091a2b"
)

func exitCode(n int) *int { return &n }

// report is one attempt's failure as an adapter recorded it: a digest standing
// for the exact text the agent reported, and what that text was classified as.
type report struct {
	digest    string
	reporting event.Reporting
}

// The shapes an adapter records, named after the controlled captures they came
// from. Two attempts given the same digest reported the same string.
func withDetail(d string) report { return report{d, event.ReportingDetail} }
func statusOnly(d string) report { return report{d, event.ReportingStatusOnly} }
func unreadable(d string) report { return report{d, event.ReportingUnrecognized} }
func historical(d string) report { return report{digest: d} }
func noReport() report           { return report{reporting: event.ReportingNoText} }

// attempt is one failed run of a command, as the hooks record it.
func attempt(session, turn, invocation string, r report, code *int, when time.Time, duration int64) event.Event {
	return event.Event{
		SchemaVersion: event.SchemaVersion,
		Agent:         "claude-code",
		Type:          event.TypeToolCall,
		Timestamp:     when,
		SessionID:     session,
		TurnID:        turn,
		Tool: &event.ToolCall{
			Name:         "Bash",
			InvocationID: invocation,
			Outcome:      event.OutcomeFailure,
			DurationMS:   ms(duration),
			Failure: &event.Failure{
				Kind:      event.FailureKindError,
				Digest:    r.digest,
				Reporting: r.reporting,
				ExitCode:  code,
			},
			Metadata: &event.ToolMetadata{
				Shell: &event.ShellOp{CommandDigest: commandDigest},
			},
		},
	}
}

// succeeds is the same command running without failing.
func succeeds(session, turn, invocation string, when time.Time) event.Event {
	ev := shellEvent(session, commandDigest, when, 900)
	ev.TurnID = turn
	ev.Tool.InvocationID = invocation
	return ev
}

// repeatedFailure seeds three attempts of one command, all failing alike.
func repeatedFailure(t *testing.T, extra ...event.Event) string {
	t.Helper()

	return seed(t, append([]event.Event{
		attempt("session-1", "turn-1", "call-1", withDetail(failureDigest), exitCode(3), at(0), 1200),
		attempt("session-1", "turn-1", "call-2", withDetail(failureDigest), exitCode(3), at(time.Second), 500),
		attempt("session-1", "turn-1", "call-3", withDetail(failureDigest), exitCode(3), at(2*time.Second), 400),
	}, extra...)...)
}

// Words that would turn an observation into a claim Axiom cannot support.
// Words that would turn what Axiom observed into a claim about why it
// happened.
//
// The second group was added with the interval, which is where the temptation
// is strongest: a list of work printed between a failed attempt and a later
// success reads as the reason the failure went away unless the page refuses to
// say so. It covers the three readings the block invites — that the work
// explains the success, that it was needed, and that less of it would have
// been better — and the diagnoses an empty interval invites.
var forbidden = []string{
	"recovered", "recovery", "fixed", "resolved", "unresolved",
	"wasted", "saved", "failed to", "loop", "cost of the failure",

	"caused", "root cause", "explains the", "explains why", "thanks to",
	"flaky", "flake", "intermittent", "unblocked", "trajectory",
	"was needed", "were needed", "necessary", "useful", "wasteful",
	"efficient", "efficiency", "retry", "retried", "remediat",
}

func rejectForbidden(t *testing.T, out string) {
	t.Helper()

	reject(t, out, forbidden)
}

// What a repeated failed attempt must never be graded or explained with.
//
// Axiom used to grade these findings by whether the attempts reported their
// failures in the same words, which was neither a grade nor about the
// failures: silent commands agreed exactly while a failing test that named the
// same assertion every time disagreed over a timestamp. The words that made
// that reading available are kept out.
//
// "identical" is missing on purpose. It is the honest word for two reports
// that were the same string, and the report says exactly that. What it must
// not attach to is a failure.
var failureClaims = []string{
	"high", "medium", "confidence", "stronger", "weaker", "quality",
	"same failure", "identical failure", "consistent failure",
	"same cause", "root cause", "reliable", "corroborat",
}

func rejectFailureClaims(t *testing.T, out string) {
	t.Helper()

	reject(t, out, failureClaims)
}

func reject(t *testing.T, out string, words []string) {
	t.Helper()

	lower := strings.ToLower(out)
	for _, word := range words {
		if strings.Contains(lower, word) {
			t.Errorf("output claims more than it observed, containing %q:\n%s", word, out)
		}
	}
}

func TestProfileReportsARepeatedFailedAttempt(t *testing.T) {
	t.Parallel()

	out := profileOutput(t, repeatedFailure(t))

	for _, want := range []string{
		"Findings",
		"Repeated failed attempt",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	if got := flat(out); !strings.Contains(got, "Failed 3 times in one turn, with only read-only operations in between") {
		t.Errorf("the evidence line does not say what was observed:\n%s", out)
	}
	if got := value(t, out, "Failed attempts"); got != "3" {
		t.Errorf("Failed attempts = %q, want 3", got)
	}
	if got := value(t, out, "Repeated after a failure"); got != "2" {
		t.Errorf("Repeated after a failure = %q, want 2", got)
	}
	if got := value(t, out, "Same exit code"); got != "3" {
		t.Errorf("Same exit code = %q, want 3", got)
	}
	// The first attempt was worth making; only the repeats are reported.
	if got := value(t, out, "Repeated-call tool time"); got != "900ms" {
		t.Errorf("Repeated-call tool time = %q, want 900ms", got)
	}
	if got := value(t, out, "Command digest"); got != "c10ec4b070ab…" {
		t.Errorf("Command digest = %q", got)
	}
	// The agent reported no size for these attempts, so there is nothing
	// measured to call redundant output, and printing one would describe a
	// failed attempt as work that produced something.
	if strings.Contains(out, "Redundant tool output") {
		t.Errorf("a failed attempt was reported as redundant output:\n%s", out)
	}
	rejectForbidden(t, out)
	rejectFailureClaims(t, out)
}

// The three shapes controlled captures of Claude Code produced, and what the
// report must say about each.
//
// They are here because the pair of them is what the old single grade could
// not express. W1 reported a failing assertion every time and was graded down
// for printing an elapsed time with it; SILENT reported nothing but a status
// and was graded up for printing it identically. Whatever the report says
// about them now, it cannot be that one is the better evidence.
func TestProfileReportsWhatTheAttemptsReported(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		attempts  []report
		reporting string
		reports   string
	}{
		"W1, a failing test naming one assertion under a changing timestamp": {
			attempts:  []report{withDetail("run-1"), withDetail("run-2"), withDetail("run-3")},
			reporting: "detail beyond status, every attempt",
			reports:   "differed",
		},
		"W2, a failing build reporting the same bytes every time": {
			attempts:  []report{withDetail("same"), withDetail("same"), withDetail("same")},
			reporting: "detail beyond status, every attempt",
			reports:   "identical",
		},
		"SILENT, a command that exited non-zero having printed nothing": {
			attempts:  []report{statusOnly("exit-1"), statusOnly("exit-1"), statusOnly("exit-1")},
			reporting: "recognized status only, every attempt",
			reports:   "identical",
		},
		"attempts whose reports were classified differently": {
			attempts:  []report{withDetail("x"), statusOnly("y"), withDetail("z")},
			reporting: "mixed across the attempts",
			reports:   "differed",
		},
		"a status reported alone by attempts that exited differently": {
			attempts:  []report{statusOnly("exit-1"), statusOnly("exit-2")},
			reporting: "recognized status only, every attempt",
			reports:   "differed",
		},
		"attempts that reported no text at all": {
			attempts:  []report{noReport(), noReport()},
			reporting: "no text at all, every attempt",
			reports:   "not established",
		},
		"a report the adapter could not read": {
			attempts:  []report{withDetail("x"), unreadable("y")},
			reporting: "not established",
			reports:   "differed",
		},
		"records written before reports were classified": {
			attempts:  []report{historical("x"), historical("x")},
			reporting: "not established",
			reports:   "identical",
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var events []event.Event
			for i, r := range c.attempts {
				events = append(events, attempt("session-1", "turn-1",
					fmt.Sprintf("call-%d", i), r, nil, at(time.Duration(i)*time.Second), 500))
			}
			out := profileOutput(t, seed(t, events...))

			if got := value(t, out, "Failure reporting"); got != c.reporting {
				t.Errorf("Failure reporting = %q, want %q", got, c.reporting)
			}
			if got := value(t, out, "Reports"); got != c.reports {
				t.Errorf("Reports = %q, want %q", got, c.reports)
			}
			// Every one of them opens the same way. What the attempts
			// reported is stated on its own lines and changes nothing about
			// how well the repetition itself is established.
			if got := flat(out); !strings.Contains(got, "in one turn, with only read-only operations in between") {
				t.Errorf("the evidence line reads differently for this shape:\n%s", out)
			}
			rejectForbidden(t, out)
			rejectFailureClaims(t, out)
		})
	}
}

// What the attempts reported is stated in a phrase rather than a word, and a
// phrase is easy to grow past the column it sits in.
func TestProfileKeepsAFailureReportInsideItsWidth(t *testing.T) {
	t.Parallel()

	for _, out := range []string{
		profileOutput(t, repeatedFailure(t)),
		profileOutput(t, seed(t,
			attempt("session-1", "turn-1", "call-1", statusOnly("x"), nil, at(0), 500),
			attempt("session-1", "turn-1", "call-2", noReport(), nil, at(time.Second), 500),
		)),
	} {
		for line := range strings.Lines(out) {
			if text := strings.TrimRight(line, "\n"); len(text) > reportWidth {
				t.Errorf("a line runs to %d characters, past the report's %d:\n%s",
					len(text), reportWidth, text)
			}
		}
	}
}

// The digest of a failure report is evidence Axiom compares, not a name a
// reader can use. Printing it invited the identity of two strings to be read
// as the identity of two failures.
func TestProfileDoesNotPrintTheFailureDigest(t *testing.T) {
	t.Parallel()

	out := profileOutput(t, repeatedFailure(t))

	if strings.Contains(out, "Failure digest") {
		t.Errorf("the failure digest was printed:\n%s", out)
	}
	if strings.Contains(out, failureDigest[:digestDisplayLen]) {
		t.Errorf("the failure digest was printed unlabelled:\n%s", out)
	}
}

// Having no report and having a different one are different observations, and
// the second is the only one that establishes anything about the reports.
func TestProfileHoldsAMissingReportApartFromADifferingOne(t *testing.T) {
	t.Parallel()

	missing := profileOutput(t, seed(t,
		attempt("session-1", "turn-1", "call-1", withDetail(failureDigest), exitCode(1), at(0), 1200),
		attempt("session-1", "turn-1", "call-2", noReport(), nil, at(time.Second), 500),
	))
	if got := value(t, missing, "Reports"); got != "not established" {
		t.Errorf("Reports = %q, want it unestablished when one attempt reported nothing", got)
	}

	differing := profileOutput(t, seed(t,
		attempt("session-1", "turn-1", "call-1", withDetail("first"), exitCode(1), at(0), 1200),
		attempt("session-1", "turn-1", "call-2", withDetail("second"), exitCode(1), at(time.Second), 500),
	))
	if got := value(t, differing, "Reports"); got != "differed" {
		t.Errorf("Reports = %q, want them differing", got)
	}
	// The exit status agreed even where the text did not.
	if got := value(t, differing, "Same exit code"); got != "1" {
		t.Errorf("Same exit code = %q, want 1", got)
	}
	rejectFailureClaims(t, differing)
}

func TestProfileWithholdsAnExitCodeTheAttemptsDidNotShare(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		attempt("session-1", "turn-1", "call-1", withDetail(failureDigest), exitCode(1), at(0), 1200),
		attempt("session-1", "turn-1", "call-2", withDetail(failureDigest), exitCode(2), at(time.Second), 500),
	)

	if out := profileOutput(t, dir); strings.Contains(out, "Same exit code") {
		t.Errorf("an exit code was printed that does not describe every attempt:\n%s", out)
	}
}

func TestProfileReportsALaterSuccessAsAnObservation(t *testing.T) {
	t.Parallel()

	out := profileOutput(t, repeatedFailure(t,
		succeeds("session-1", "turn-1", "call-4", at(3*time.Second))))

	if got := value(t, out, "Same command later succeeded"); got != "yes" {
		t.Errorf("Same command later succeeded = %q, want yes", got)
	}
	if got := flat(out); !strings.Contains(got, "what came between is not evidence of what made the difference") {
		t.Errorf("nothing warns the reader against reading the success as a cure:\n%s", out)
	}
	rejectForbidden(t, out)
}

// A command that was never tried again tells Axiom nothing about whether the
// agent got past it, so the report says nothing at all.
func TestProfileIsSilentAboutASuccessItNeverSaw(t *testing.T) {
	t.Parallel()

	out := profileOutput(t, repeatedFailure(t))

	if strings.Contains(out, "Same command later succeeded") {
		t.Errorf("a success was reported that was never observed:\n%s", out)
	}
	for _, absent := range []string{"never succeeded", "still failing", "no success"} {
		if strings.Contains(strings.ToLower(out), absent) {
			t.Errorf("output reads absence of evidence as evidence, containing %q:\n%s", absent, out)
		}
	}
	rejectForbidden(t, out)
}

// The behavior is recorded by the hooks alone. Telemetry adds context to a
// finding and is never what makes one.
func TestProfileReportsFailuresWithoutTelemetry(t *testing.T) {
	t.Parallel()

	whole := profileOutput(t, repeatedFailure(t))
	out := findingsSection(t, whole)

	if !strings.Contains(out, "Repeated failed attempt") {
		t.Errorf("the finding needs telemetry it should not need:\n%s", out)
	}
	if strings.Contains(out, "Observed model consumption") {
		t.Errorf("the finding reports consumption with no usage log present:\n%s", out)
	}
	if strings.Contains(whole, "Warning") {
		t.Errorf("output warns with no usage log present:\n%s", whole)
	}
}

// What the turn consumed is reported the same way for every finding: as
// context, never as the price of the behavior beside it.
func TestProfileAssociatesConsumptionWithAFailure(t *testing.T) {
	t.Parallel()

	dir := repeatedFailure(t)
	seedUsage(t, dir,
		request("session-1", "turn-1", firstRequestTokens, micros(213915)),
		request("session-1", "turn-1", secondRequestTokens, micros(74085)),
	)

	out := profileOutput(t, dir)

	if !strings.Contains(out, "Observed model consumption in the turn where this happened") {
		t.Errorf("the consumption of the turn is missing:\n%s", out)
	}
	if got := value(t, out, "Model requests"); got != "2" {
		t.Errorf("Model requests = %q, want 2", got)
	}
	if got := flat(out); !strings.Contains(got, "not the cost of the repetition") {
		t.Errorf("the consumption block reads as the cost of the failure:\n%s", out)
	}
	if strings.Contains(out, "Redundant tool output") {
		t.Errorf("a failed attempt was reported as redundant output:\n%s", out)
	}
}

// The two families of finding describe different behavior and are reported
// apart, under one heading that no longer promises only redundancy.
func TestProfileReportsFailuresAlongsideRedundancy(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		attempt("session-1", "turn-1", "call-1", withDetail(failureDigest), exitCode(3), at(0), 1200),
		attempt("session-1", "turn-1", "call-2", withDetail(failureDigest), exitCode(3), at(time.Second), 500),
		readEvent("session-1", "/repo/notes.txt", at(2*time.Second), 4),
		readEvent("session-1", "/repo/notes.txt", at(3*time.Second), 3),
	)

	out := profileOutput(t, dir)

	for _, want := range []string{
		"Findings",
		"Repeated failed attempt",
		"Repeated file read",
		"2 findings.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}
