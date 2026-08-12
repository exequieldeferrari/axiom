package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/event"
)

// editCall is one edit of a path, as the hooks record it.
func editCall(session, turn, path string, when time.Time, opts ...func(*event.ToolCall)) event.Event {
	ev := readEvent(session, path, when, 6)
	ev.TurnID = turn
	ev.Tool.Name = "Edit"
	ev.Tool.Metadata.File.Access = event.AccessEdit
	for _, opt := range opts {
		opt(ev.Tool)
	}
	return ev
}

// block returns the lines the interval was rendered on, so a test can pin the
// whole of it rather than the parts it remembered to look for.
func block(t *testing.T, out string) string {
	t.Helper()

	_, rest, ok := strings.Cut(out, findingIndent+"Recorded before the later success\n")
	if !ok {
		t.Fatalf("no interval under the finding:\n%s", out)
	}
	end, _, ok := strings.Cut(rest, "\n\n")
	if !ok {
		t.Fatalf("the interval runs to the end of the report:\n%s", out)
	}
	return end + "\n"
}

// The ordering Axiom's own controlled capture produced, rendered. The read
// before the opaque call is inside the interval, and the report has to show it
// there: that is where the record puts it.
func TestProfileReportsWhatWasRecordedBeforeTheLaterSuccess(t *testing.T) {
	t.Parallel()

	out := profileOutput(t, repeatedFailure(t,
		readCall("session-1", "turn-1", "call-4", "/repo/status.txt", at(3*time.Second)),
		opaqueCall("session-1", "turn-1", "mcp__tracker__issue", at(4*time.Second)),
		readCall("session-1", "turn-1", "call-6", "/repo/main.go", at(5*time.Second)),
		editCall("session-1", "turn-1", "/repo/status.txt", at(6*time.Second)),
		succeeds("session-1", "turn-1", "call-8", at(7*time.Second)),
	))

	want := strings.Join([]string{
		"      Operations recorded             4",
		"      Whole-file reads                2",
		"      Edits                           1",
		"      Unrecognized                    1",
		"      Writes or edits recorded at",
		"        /repo/status.txt",
		"      Turn boundary                   none recorded",
	}, "\n") + "\n"

	if got := block(t, out); got != want {
		t.Errorf("interval rendered as\n%s\nwant\n%s", got, want)
	}
	rejectForbidden(t, out)

	// The explanations below the findings are Axiom's own sentences and are
	// written to the report's width, the same as every other section.
	_, explanations, _ := strings.Cut(out, "1 finding.")
	for line := range strings.Lines(explanations) {
		if text := strings.TrimRight(line, "\n"); len(text) > reportWidth {
			t.Errorf("a line runs to %d characters, past the report's %d:\n%s", len(text), reportWidth, text)
		}
	}
}

// The other half of the capture. The command was observed failing and then
// observed succeeding with nothing recorded in between, and the report has to
// say that without letting it become a claim about the execution.
func TestProfileReportsAnEmptyIntervalExplicitly(t *testing.T) {
	t.Parallel()

	out := profileOutput(t, repeatedFailure(t,
		succeeds("session-1", "turn-1", "call-4", at(3*time.Second))))

	want := "      No tool operation was recorded between them.\n" +
		"      Turn boundary                   none recorded\n"
	if got := block(t, out); got != want {
		t.Errorf("interval rendered as\n%s\nwant\n%s", got, want)
	}

	// The report has to distinguish what the log holds from what happened.
	// The controlled run that produced this case changed a file on disk
	// between the two observations, from inside the command.
	if got := flat(out); !strings.Contains(got, "that describes the log and not the execution") {
		t.Errorf("an empty interval reads as an empty execution:\n%s", out)
	}
	for _, absent := range []string{
		"nothing happened", "no work", "nothing changed", "same state", "identical conditions",
	} {
		if strings.Contains(strings.ToLower(out), absent) {
			t.Errorf("output reads an empty interval as evidence, containing %q:\n%s", absent, out)
		}
	}
	rejectForbidden(t, out)
}

// The three states are not interchangeable, and the one Axiom could not settle
// must not read as the absence of a boundary.
func TestProfileDistinguishesTheTurnBoundaryStates(t *testing.T) {
	t.Parallel()

	noTurn := func(ev event.Event) event.Event {
		ev.TurnID = ""
		return ev
	}

	cases := map[string]struct {
		extra []event.Event
		want  string
	}{
		"every call reported the same turn": {
			[]event.Event{succeeds("session-1", "turn-1", "call-4", at(3*time.Second))},
			"none recorded",
		},
		"the success came in a later turn": {
			[]event.Event{succeeds("session-1", "turn-2", "call-4", at(3*time.Second))},
			"recorded between them",
		},
		"a call between them carried no turn": {
			[]event.Event{
				noTurn(readCall("session-1", "", "call-4", "/repo/main.go", at(3*time.Second))),
				succeeds("session-1", "turn-1", "call-5", at(4*time.Second)),
			},
			"not established",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			out := profileOutput(t, repeatedFailure(t, tc.extra...))

			if got := value(t, out, "Turn boundary"); got != tc.want {
				t.Errorf("Turn boundary = %q, want %q", got, tc.want)
			}
			if got := flat(out); !strings.Contains(got, "input Axiom does not observe may have arrived") {
				t.Errorf("nothing says what a turn boundary means here:\n%s", out)
			}
			rejectForbidden(t, out)
		})
	}
}

// The paths shown are bounded and the bound is declared. What it leaves out is
// counted, and the operation count above it stays whole.
func TestProfileBoundsThePathsItShowsWithoutShorteningTheInterval(t *testing.T) {
	t.Parallel()

	extra := []event.Event{}
	for i := range 8 {
		extra = append(extra, editCall("session-1", "turn-1",
			string(rune('a'+i))+".go", at(time.Duration(3+i)*time.Second)))
	}
	extra = append(extra, succeeds("session-1", "turn-1", "call-last", at(12*time.Second)))

	out := profileOutput(t, repeatedFailure(t, extra...))

	if got := value(t, out, "Operations recorded"); got != "8" {
		t.Errorf("Operations recorded = %q, want 8: the count is not bounded by the paths", got)
	}
	if got := value(t, out, "Edits"); got != "8" {
		t.Errorf("Edits = %q, want 8", got)
	}
	interval := block(t, out)
	if !strings.Contains(interval, "and 3 more paths recorded") {
		t.Errorf("the paths left out are not accounted for:\n%s", interval)
	}
	for _, want := range []string{"a.go", "e.go"} {
		if !strings.Contains(interval, "        "+want+"\n") {
			t.Errorf("interval is missing the path %q:\n%s", want, interval)
		}
	}
	if strings.Contains(interval, "f.go") {
		t.Errorf("more paths were shown than the bound allows:\n%s", interval)
	}
	rejectForbidden(t, out)
}

// A write that was never established is not a write that failed. Both are
// named, and neither is counted as one the agent reported succeeding.
func TestProfileKeepsWriteOutcomesApartInTheInterval(t *testing.T) {
	t.Parallel()

	unestablished := func(c *event.ToolCall) { c.Outcome = event.Outcome("") }
	reportedFailing := func(c *event.ToolCall) { c.Outcome = event.OutcomeFailure }

	out := profileOutput(t, repeatedFailure(t,
		editCall("session-1", "turn-1", "/repo/a.go", at(3*time.Second)),
		editCall("session-1", "turn-1", "/repo/b.go", at(4*time.Second), reportedFailing),
		editCall("session-1", "turn-1", "/repo/c.go", at(5*time.Second), unestablished),
		succeeds("session-1", "turn-1", "call-last", at(6*time.Second)),
	))

	if got := value(t, out, "Edits"); got != "3  (1 reported failing, 1 with no outcome recorded)" {
		t.Errorf("Edits = %q, want the outcomes kept apart", got)
	}
	// A path is retained because a call named it, whatever became of the
	// call, so all three are here.
	for _, want := range []string{"/repo/a.go", "/repo/b.go", "/repo/c.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing the path %q:\n%s", want, out)
		}
	}
	if got := flat(out); !strings.Contains(got, "a path names a file a call was recorded at, not a file left different") {
		t.Errorf("a recorded path reads as a file that changed:\n%s", out)
	}
	rejectForbidden(t, out)
}

// Without an observed success there is nothing to bound an interval, and the
// report prints neither the block nor the explanation of one.
func TestProfileSaysNothingAboutAnIntervalItCannotBound(t *testing.T) {
	t.Parallel()

	out := profileOutput(t, repeatedFailure(t,
		readCall("session-1", "turn-1", "call-4", "/repo/main.go", at(3*time.Second))))

	for _, absent := range []string{"Recorded before the later success", "Turn boundary"} {
		if strings.Contains(out, absent) {
			t.Errorf("output contains %q for a command never observed succeeding:\n%s", absent, out)
		}
	}
	rejectForbidden(t, out)
}

// The block sits under the finding it belongs to. Lifting it into a population
// of its own would make the interval the subject and invite intervals to be
// compared against each other, which is a ranking of nothing.
func TestProfileKeepsTheIntervalSubordinateToTheFinding(t *testing.T) {
	t.Parallel()

	out := profileOutput(t, repeatedFailure(t,
		succeeds("session-1", "turn-1", "call-4", at(3*time.Second))))

	heading, _, ok := strings.Cut(out, "Recorded before the later success")
	if !ok {
		t.Fatalf("no interval in the report:\n%s", out)
	}
	if !strings.Contains(heading, "Repeated failed attempt") {
		t.Errorf("the interval is not printed under a finding:\n%s", out)
	}
	if !strings.Contains(heading, "Same command later succeeded") {
		t.Errorf("the interval is printed above the success that bounds it:\n%s", out)
	}
	// The report's own count is of findings. An interval is part of one.
	if !strings.Contains(out, "1 finding.") {
		t.Errorf("the interval was counted as a finding of its own:\n%s", out)
	}
}

// Two profiles of one log are the same report.
func TestProfileRendersIntervalsDeterministically(t *testing.T) {
	t.Parallel()

	dir := repeatedFailure(t,
		editCall("session-1", "turn-1", "/repo/a.go", at(3*time.Second)),
		editCall("session-1", "turn-1", "/repo/b.go", at(4*time.Second)),
		opaqueCall("session-1", "turn-1", "mcp__tracker__issue", at(5*time.Second)),
		succeeds("session-1", "turn-1", "call-last", at(6*time.Second)),
	)

	first := profileOutput(t, dir)
	for range 8 {
		if got := profileOutput(t, dir); got != first {
			t.Fatalf("two profiles of one log differ:\n%s\n---\n%s", first, got)
		}
	}
}
