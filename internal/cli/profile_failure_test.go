package cli

import (
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

// attempt is one failed run of a command, as the hooks record it.
func attempt(session, turn, invocation, failure string, code *int, when time.Time, duration int64) event.Event {
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
			Failure:      &event.Failure{Kind: event.FailureKindError, Digest: failure, ExitCode: code},
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
		attempt("session-1", "turn-1", "call-1", failureDigest, exitCode(3), at(0), 1200),
		attempt("session-1", "turn-1", "call-2", failureDigest, exitCode(3), at(time.Second), 500),
		attempt("session-1", "turn-1", "call-3", failureDigest, exitCode(3), at(2*time.Second), 400),
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

	lower := strings.ToLower(out)
	for _, word := range forbidden {
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
		"HIGH",
		"Repeated failed attempt",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	if got := flat(out); !strings.Contains(got, "Failed 3 times, each reporting the same observed failure") {
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
	if got := value(t, out, "Failure digest"); got != "30303e9585c1…" {
		t.Errorf("Failure digest = %q", got)
	}
	// The agent reported no size for these attempts, so there is nothing
	// measured to call redundant output, and printing one would describe a
	// failed attempt as work that produced something.
	if strings.Contains(out, "Redundant tool output") {
		t.Errorf("a failed attempt was reported as redundant output:\n%s", out)
	}
	rejectForbidden(t, out)
}

// Real commands print elapsed times, so identical failures are the exception.
// The repetition still holds; what Axiom knows about the failures does not.
func TestProfileLowersConfidenceForUncomparableFailures(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		attempt("session-1", "turn-1", "call-1", "first", exitCode(1), at(0), 1200),
		attempt("session-1", "turn-1", "call-2", "second", exitCode(1), at(time.Second), 500),
	)

	out := profileOutput(t, dir)

	if !strings.Contains(out, "MEDIUM") {
		t.Errorf("output does not carry the lower confidence:\n%s", out)
	}
	if got := flat(out); !strings.Contains(got, "Failed 2 times; identical failure reporting was not established") {
		t.Errorf("the evidence line overstates what was observed:\n%s", out)
	}
	if strings.Contains(out, "Failure digest") {
		t.Errorf("a failure digest was printed for attempts that did not share one:\n%s", out)
	}
	// The exit status agreed even though the text did not.
	if got := value(t, out, "Same exit code"); got != "1" {
		t.Errorf("Same exit code = %q, want 1", got)
	}
	rejectForbidden(t, out)
}

// Attempts that reported different failures and attempts that reported none
// are different observations, but they leave Axiom in the same position, and
// one sentence has to be true of both.
func TestProfileSaysTheSameThingAboutEveryUnestablishedFailure(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		attempt("session-1", "turn-1", "call-1", failureDigest, exitCode(1), at(0), 1200),
		attempt("session-1", "turn-1", "call-2", "", nil, at(time.Second), 500),
	)

	out := profileOutput(t, dir)

	if !strings.Contains(out, "MEDIUM") {
		t.Errorf("output does not carry the lower confidence:\n%s", out)
	}
	if got := flat(out); !strings.Contains(got, "Failed 2 times; identical failure reporting was not established") {
		t.Errorf("an unreported failure does not read like a differing one:\n%s", out)
	}
	rejectForbidden(t, out)
}

func TestProfileWithholdsAnExitCodeTheAttemptsDidNotShare(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		attempt("session-1", "turn-1", "call-1", failureDigest, exitCode(1), at(0), 1200),
		attempt("session-1", "turn-1", "call-2", failureDigest, exitCode(2), at(time.Second), 500),
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

	out := profileOutput(t, repeatedFailure(t))

	if !strings.Contains(out, "Repeated failed attempt") {
		t.Errorf("the finding needs telemetry it should not need:\n%s", out)
	}
	for _, absent := range []string{"Observed model consumption", "Warning"} {
		if strings.Contains(out, absent) {
			t.Errorf("output mentions %q with no usage log present:\n%s", absent, out)
		}
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
		attempt("session-1", "turn-1", "call-1", failureDigest, exitCode(3), at(0), 1200),
		attempt("session-1", "turn-1", "call-2", failureDigest, exitCode(3), at(time.Second), 500),
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
