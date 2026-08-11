package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/event"
)

// The numbers here are from a real paired capture: two model requests the
// agent reported under one turn identifier.
var (
	firstRequestTokens  = &event.Tokens{Input: 2, Output: 93, CacheRead: 0, CacheCreation: 35419}
	secondRequestTokens = &event.Tokens{Input: 6, Output: 308, CacheRead: 117147, CacheCreation: 5722}
)

func micros(n int64) *int64 { return &n }

func request(session, turn string, tokens *event.Tokens, cost *int64) event.Usage {
	return event.Usage{
		SchemaVersion: event.SchemaVersion,
		Agent:         "claude-code",
		Kind:          event.UsageModelRequest,
		Timestamp:     at(0),
		SessionID:     session,
		TurnID:        turn,
		Model:         "claude-sonnet-5",
		Tokens:        tokens,
		CostMicros:    cost,
	}
}

// turnOfRequests seeds a file read three times in one turn, together with the
// model requests the agent reported for that turn.
func turnOfRequests(t *testing.T, usage ...event.Usage) string {
	t.Helper()

	return repeatedReadWithTelemetry(t, append([]event.Usage{
		measurement("session-1", "turn-1", "call-1", bytesOf(firstReadBytes)),
		measurement("session-1", "turn-1", "call-2", bytesOf(repeatReadBytes)),
		measurement("session-1", "turn-1", "call-3", bytesOf(repeatReadBytes)),
	}, usage...)...)
}

// flat collapses the report's wrapping so that a sentence can be asserted as
// the sentence a reader sees rather than as the lines it was printed on.
func flat(out string) string { return strings.Join(strings.Fields(out), " ") }

// value returns what the report printed against a label.
func value(t *testing.T, out, label string) string {
	t.Helper()

	for line := range strings.Lines(out) {
		trimmed := strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(trimmed, label); ok {
			return strings.TrimSpace(rest)
		}
	}
	t.Fatalf("no line for %q:\n%s", label, out)
	return ""
}

// Cache reads run to six figures, which is unreadable ungrouped.
func TestCountsAreGroupedForReading(t *testing.T) {
	t.Parallel()

	cases := map[int64]string{
		0:        "0",
		8:        "8",
		401:      "401",
		1000:     "1,000",
		117147:   "117,147",
		9876543:  "9,876,543",
		-1234567: "-1,234,567",
	}
	for n, want := range cases {
		if got := thousands(n); got != want {
			t.Errorf("thousands(%d) = %q, want %q", n, got, want)
		}
	}
}

// Every request recorded against the turn counts. Reporting one of them would
// describe less than the agent said it did there.
func TestProfileReportsWhatTheTurnConsumed(t *testing.T) {
	t.Parallel()

	dir := turnOfRequests(t,
		request("session-1", "turn-1", firstRequestTokens, micros(213915)),
		request("session-1", "turn-1", secondRequestTokens, micros(74085)),
	)

	out := profileOutput(t, dir)

	if !strings.Contains(out, "Observed model consumption in the turn where this happened") {
		t.Fatalf("the consumption block is missing:\n%s", out)
	}
	for label, want := range map[string]string{
		"Model requests": "2",
		"Input tokens":   "8",
		"Output tokens":  "401",
		"Cache read":     "117,147",
		"Cache creation": "41,141",
		"Model cost":     "$0.2880",
	} {
		if got := value(t, out, label); got != want {
			t.Errorf("%s = %q, want %q", label, got, want)
		}
	}
}

// The two kinds of evidence support different claims, and the report has to
// say which is which before it shows a number that looks like a price.
func TestProfileDistinguishesMeasuredImpactFromConsumption(t *testing.T) {
	t.Parallel()

	dir := turnOfRequests(t,
		request("session-1", "turn-1", firstRequestTokens, micros(213915)),
	)

	out := profileOutput(t, dir)

	measured := strings.Index(out, "Redundant tool output")
	heading := strings.Index(out, "Observed model consumption in the turn")
	if measured < 0 || heading < 0 {
		t.Fatalf("both kinds of evidence should be present:\n%s", out)
	}
	if measured > heading {
		t.Errorf("consumption is reported before the finding's own measurement:\n%s", out)
	}
	if !strings.Contains(flat(out), "This is the observed model consumption for that turn, not the cost of the repetition.") {
		t.Errorf("the consumption is not qualified where it is read:\n%s", out)
	}
	// Nothing here is attributable to the repetition, so nothing may be
	// named as though it were.
	for _, forbidden := range []string{"wasted", "saved", "avoidable", "Redundant tokens", "Redundant cost"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("the report claims %q:\n%s", forbidden, out)
		}
	}
}

// A run that repeats inside one turn happened once, in one turn.
func TestProfileCountsAnAffectedTurnOnce(t *testing.T) {
	t.Parallel()

	dir := turnOfRequests(t, request("session-1", "turn-1", firstRequestTokens, micros(213915)))

	out := profileOutput(t, dir)

	if !strings.Contains(out, "Observed model consumption in the turn where this happened") {
		t.Errorf("three calls in one turn were not counted as one turn:\n%s", out)
	}
	if got := value(t, out, "Model requests"); got != "1" {
		t.Errorf("Model requests = %q, want 1", got)
	}
}

// A run spanning turns is one finding, and each turn it touched contributes
// what it consumed.
func TestProfileReportsEveryTurnAFindingSpans(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		readCall("session-1", "turn-1", "call-1", "/repo/notes.txt", at(0)),
		readCall("session-1", "turn-2", "call-2", "/repo/notes.txt", at(time.Second)),
	)
	seedUsage(t, dir,
		request("session-1", "turn-1", firstRequestTokens, micros(213915)),
		request("session-1", "turn-2", secondRequestTokens, micros(74085)),
	)

	out := profileOutput(t, dir)

	if !strings.Contains(out, "Observed model consumption in the 2 turns where this happened") {
		t.Fatalf("the turns the finding spans are wrong:\n%s", out)
	}
	if got := value(t, out, "Model cost"); got != "$0.2880" {
		t.Errorf("Model cost = %q, want the total of both turns", got)
	}
}

// A receiver that was not running for part of a session leaves turns with
// nothing recorded. The finding still happened in all of them, so the report
// says how much of it the evidence covers instead of describing a smaller
// finding.
func TestProfileReportsPartialCoverageAsCoverage(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		readCall("session-1", "turn-1", "call-1", "/repo/notes.txt", at(0)),
		readCall("session-1", "turn-2", "call-2", "/repo/notes.txt", at(time.Second)),
		readCall("session-1", "turn-3", "call-3", "/repo/notes.txt", at(2*time.Second)),
	)
	seedUsage(t, dir, request("session-1", "turn-2", firstRequestTokens, micros(213915)))

	out := profileOutput(t, dir)

	if !strings.Contains(out, "Observed model consumption in 1 of the 3 turns where this happened") {
		t.Fatalf("partial coverage is not reported as coverage:\n%s", out)
	}
	// The two turns with nothing recorded are unknown, not zero, so the
	// totals are the observed turn's own.
	if got := value(t, out, "Model cost"); got != "$0.2139" {
		t.Errorf("Model cost = %q, want only what was recorded", got)
	}
	if got := value(t, out, "Input tokens"); got != "2" {
		t.Errorf("Input tokens = %q, want only what was recorded", got)
	}
	// "those turns" would read as all three.
	if !strings.Contains(flat(out), "This is the observed model consumption for the turn it was recorded in, not the cost of the repetition.") {
		t.Errorf("the caveat does not say which turns the totals came from:\n%s", out)
	}
}

// Coverage is reported per finding, so a finding whose turns were all recorded
// says so plainly even when another finding's were not.
func TestProfileNamesTheObservedTurnsWhenSeveralWereRecorded(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		readCall("session-1", "turn-1", "call-1", "/repo/notes.txt", at(0)),
		readCall("session-1", "turn-2", "call-2", "/repo/notes.txt", at(time.Second)),
		readCall("session-1", "turn-3", "call-3", "/repo/notes.txt", at(2*time.Second)),
	)
	seedUsage(t, dir,
		request("session-1", "turn-1", firstRequestTokens, micros(213915)),
		request("session-1", "turn-2", secondRequestTokens, micros(74085)),
	)

	out := profileOutput(t, dir)

	if !strings.Contains(out, "Observed model consumption in 2 of the 3 turns where this happened") {
		t.Fatalf("partial coverage of several turns is wrong:\n%s", out)
	}
	if !strings.Contains(flat(out), "for the turns it was recorded in, not the cost of the repetition.") {
		t.Errorf("the caveat does not agree with the heading:\n%s", out)
	}
}

// Two findings in one turn each describe all of that turn. Neither is a share
// of it, which is why the report never adds them up.
func TestProfileRepeatsASharedTurnForEachFinding(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		readCall("session-1", "turn-1", "call-1", "/repo/notes.txt", at(0)),
		readCall("session-1", "turn-1", "call-2", "/repo/notes.txt", at(time.Second)),
		readCall("session-1", "turn-1", "call-3", "/repo/other.txt", at(2*time.Second)),
		readCall("session-1", "turn-1", "call-4", "/repo/other.txt", at(3*time.Second)),
	)
	seedUsage(t, dir, request("session-1", "turn-1", firstRequestTokens, micros(213915)))

	out := profileOutput(t, dir)

	if got := strings.Count(out, "Observed model consumption in the turn where this happened"); got != 2 {
		t.Errorf("the shared turn appears %d times, want once per finding:\n%s", got, out)
	}
	if got := strings.Count(out, "$0.2139"); got != 2 {
		t.Errorf("the turn's cost appears %d times, want it reported in full for each finding:\n%s", got, out)
	}
}

// Telemetry is recorded only while a receiver runs. A turn with nothing
// recorded consumed an unknown amount, not nothing.
func TestProfileOmitsConsumptionWithoutModelRequests(t *testing.T) {
	t.Parallel()

	cases := map[string][]event.Usage{
		"no telemetry at all":  nil,
		"only tool results":    {measurement("session-1", "turn-1", "call-2", bytesOf(repeatReadBytes))},
		"another turn's turns": {request("session-1", "turn-9", firstRequestTokens, micros(213915))},
		"another session":      {request("session-9", "turn-1", firstRequestTokens, micros(213915))},
	}
	for name, usage := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			out := profileOutput(t, repeatedReadWithTelemetry(t, usage...))

			if !strings.Contains(out, "Potentially redundant reads") {
				t.Fatalf("the finding is missing:\n%s", out)
			}
			if strings.Contains(out, "Observed model consumption") {
				t.Errorf("consumption was reported where none was observed:\n%s", out)
			}
			if strings.Contains(out, "Model requests") {
				t.Errorf("an empty consumption block was rendered:\n%s", out)
			}
		})
	}
}

// A withheld total is left out rather than printed as zero: reporting nothing
// and reporting none are different facts.
func TestProfileOmitsWithheldConsumption(t *testing.T) {
	t.Parallel()

	t.Run("counts", func(t *testing.T) {
		t.Parallel()

		out := profileOutput(t, turnOfRequests(t,
			request("session-1", "turn-1", firstRequestTokens, micros(213915)),
			request("session-1", "turn-1", nil, micros(74085)),
		))

		if got := value(t, out, "Model requests"); got != "2" {
			t.Errorf("Model requests = %q, want 2", got)
		}
		if strings.Contains(out, "Input tokens") {
			t.Errorf("a partial token total was reported:\n%s", out)
		}
		if got := value(t, out, "Model cost"); got != "$0.2880" {
			t.Errorf("Model cost = %q, want the cost both requests reported", got)
		}
	})

	t.Run("cost", func(t *testing.T) {
		t.Parallel()

		out := profileOutput(t, turnOfRequests(t,
			request("session-1", "turn-1", firstRequestTokens, micros(213915)),
			request("session-1", "turn-1", secondRequestTokens, nil),
		))

		if got := value(t, out, "Input tokens"); got != "8" {
			t.Errorf("Input tokens = %q, want the counts both requests reported", got)
		}
		if strings.Contains(out, "Model cost") {
			t.Errorf("a partial cost was reported:\n%s", out)
		}
	})
}

// Consumption is context for a finding, so a report with no findings has
// nothing to attach it to.
func TestProfileWithoutFindingsReportsNoConsumption(t *testing.T) {
	t.Parallel()

	dir := seed(t, readCall("session-1", "turn-1", "call-1", "/repo/notes.txt", at(0)))
	seedUsage(t, dir, request("session-1", "turn-1", firstRequestTokens, micros(213915)))

	out := profileOutput(t, dir)

	if strings.Contains(out, "Observed model consumption") {
		t.Errorf("consumption was reported without a finding:\n%s", out)
	}
}
