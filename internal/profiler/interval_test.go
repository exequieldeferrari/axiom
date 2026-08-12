package profiler_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/profiler"
)

// sequence returns the single repeated-failure finding a stream is expected to
// produce, ignoring any redundancy findings recorded alongside it.
func sequence(t *testing.T, report profiler.Report) profiler.Finding {
	t.Helper()

	var found []profiler.Finding
	for _, f := range report.Findings {
		if f.Kind == profiler.KindRepeatedFailure {
			found = append(found, f)
		}
	}
	if len(found) != 1 {
		t.Fatalf("got %d sequences of failed attempts, want 1:\n%+v", len(found), report.Findings)
	}
	return found[0]
}

// interval renders what a finding says was recorded between its last attempt
// and the success, so that a test can state what it expects on one line.
//
// It reads as the operation count, then the categories that were not empty,
// then the paths, then the turn boundary. Writes and edits read
// "succeeded,failed,unestablished". Paths read "[a b]+2" where two more
// distinct paths were recorded than the finding retained.
//
// Every rendering checks that the categories account for the total. The
// composition claims to be exhaustive, and a category added later without
// being counted in the total would otherwise pass unnoticed.
func interval(t *testing.T, f profiler.Finding) string {
	t.Helper()

	if f.Interval == nil {
		t.Fatalf("no interval on a finding whose command was observed succeeding later:\n%+v", f)
	}
	iv := *f.Interval

	parts := []string{fmt.Sprint(iv.Operations)}
	add := func(name string, n int) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", name, n))
		}
	}
	add("whole", iv.WholeReads)
	add("ranged", iv.RangedReads)
	add("search", iv.Searches)
	add("shell", iv.Shell)
	for name, o := range map[string]profiler.Outcomes{"write": iv.Writes, "edit": iv.Edits} {
		if o.Total() > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d,%d,%d", name, o.Succeeded, o.Failed, o.Unestablished))
		}
	}
	add("subagent", iv.Subagents)
	add("uninterpreted", iv.Uninterpreted)
	// The map above is ordered arbitrarily, and the rendering is compared
	// literally by the tests that use it.
	slices.Sort(parts[1:])

	if total := sum(iv); total != iv.Operations {
		t.Errorf("categories account for %d of %d operations: %s", total, iv.Operations, strings.Join(parts, " "))
	}
	if len(iv.Paths) > 0 || iv.OmittedPaths > 0 {
		paths := fmt.Sprintf("[%s]", strings.Join(iv.Paths, " "))
		if iv.OmittedPaths > 0 {
			paths += fmt.Sprintf("+%d", iv.OmittedPaths)
		}
		parts = append(parts, paths)
	}
	return strings.Join(parts, " ") + " turn=" + string(iv.TurnBoundary)
}

func sum(iv profiler.Interval) int {
	return iv.WholeReads + iv.RangedReads + iv.Searches + iv.Shell +
		iv.Writes.Total() + iv.Edits.Total() + iv.Subagents + iv.Uninterpreted
}

// The interval Axiom's own capture produced, and the one the implementation
// exists to get right. The sequence of failed attempts closed at the opaque
// tool call, which is where the finding was produced, but the read before it
// was recorded after the last attempt and belongs to the interval just as much
// as the work that followed. Reconstructing the start from the close would
// lose it.
func TestIntervalStartsAtTheLastAttemptAndNotWhereTheSequenceClosed(t *testing.T) {
	t.Parallel()

	f := sequence(t, analyze(newStream("a").inTurn("t1").
		shell("go-test", failing("x")).
		shell("go-test", failing("x")).
		read("/src/status.go").
		unrecognised("mcp__linear__issue").
		read("/src/main.go").
		edit("/src/status.go").
		shell("go-test")))

	if got, want := interval(t, f), "4 edit=1,0,0 uninterpreted=1 whole=2 [/src/status.go] turn=none"; got != want {
		t.Errorf("interval = %q, want %q", got, want)
	}
}

// The other half of the capture: the command was observed failing and then
// observed succeeding with nothing recorded in between. That is a statement
// about the log alone. The controlled run that produced it changed a file on
// disk between the two, from inside the command, where no tool call reports it.
func TestIntervalIsEmptyWhenNothingWasRecordedBetween(t *testing.T) {
	t.Parallel()

	f := sequence(t, analyze(newStream("a").inTurn("t1").
		shell("go-test", failing("x")).
		shell("go-test", failing("x")).
		shell("go-test")))

	if got, want := interval(t, f), "0 turn=none"; got != want {
		t.Errorf("interval = %q, want %q", got, want)
	}
	iv := f.Interval
	if iv.Paths != nil || iv.OmittedPaths != 0 {
		t.Errorf("Paths = %v with %d omitted, want none", iv.Paths, iv.OmittedPaths)
	}
}

// Every shape of call the log can carry is counted as itself. An interval that
// dropped the ones this version cannot describe would report less work than
// was recorded while looking complete.
func TestIntervalCountsEveryShapeOfRecordedCall(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		between func(*stream) *stream
		want    string
	}{
		"whole-file read": {
			func(s *stream) *stream { return s.read("/src/main.go") },
			"1 whole=1 turn=none",
		},
		"ranged read": {
			func(s *stream) *stream { return s.readRange("/src/main.go", 40) },
			"1 ranged=1 turn=none",
		},
		"search": {
			func(s *stream) *stream { return s.search("TODO") },
			"1 search=1 turn=none",
		},
		"another command": {
			func(s *stream) *stream { return s.shell("go-vet") },
			"1 shell=1 turn=none",
		},
		// Its effects are unbounded, which is why it ends the sequence, but
		// what was recorded is still a command.
		"a command left running": {
			func(s *stream) *stream { return s.background("serve") },
			"1 shell=1 turn=none",
		},
		"write": {
			func(s *stream) *stream { return s.write("/src/main.go") },
			"1 write=1,0,0 [/src/main.go] turn=none",
		},
		"edit": {
			func(s *stream) *stream { return s.edit("/src/main.go") },
			"1 edit=1,0,0 [/src/main.go] turn=none",
		},
		// The nested agent's own calls are recorded against a scope of its
		// own, so what belongs here is the one call that started it.
		"subagent": {
			func(s *stream) *stream { return s.subagent("explore") },
			"1 subagent=1 turn=none",
		},
		"a tool Axiom cannot describe": {
			func(s *stream) *stream { return s.unrecognised("NotebookEdit") },
			"1 uninterpreted=1 turn=none",
		},
		// Metadata carrying nothing this version recognises, which is what a
		// record written by a later schema looks like from here.
		"metadata with no operation in it": {
			func(s *stream) *stream { return s.tool("WebFetch", &event.ToolMetadata{}) },
			"1 uninterpreted=1 turn=none",
		},
		// A file operation that named no path cannot be attributed to one, so
		// it is counted as work Axiom could not interpret rather than as a
		// write whose path is missing.
		"a write that named no path": {
			func(s *stream) *stream { return s.write("") },
			"1 uninterpreted=1 turn=none",
		},
		// A later model may record an access this version has no category
		// for. It is neither a read nor a modification until something says
		// which, and guessing either would be the claim, not the record.
		"a file access Axiom does not know": {
			func(s *stream) *stream {
				return s.tool("NotebookEdit", &event.ToolMetadata{
					File: &event.FileOp{Path: "/src/notes.ipynb", Access: "execute"},
				})
			},
			"1 uninterpreted=1 turn=none",
		},
		"nothing at all": {
			func(s *stream) *stream { return s },
			"0 turn=none",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := newStream("a").inTurn("t1").
				shell("go-test", failing("x")).
				shell("go-test", failing("x"))
			f := sequence(t, analyze(tc.between(s).shell("go-test")))

			if got := interval(t, f); got != tc.want {
				t.Errorf("interval = %q, want %q", got, tc.want)
			}
		})
	}
}

// A write that was never established is not a write that failed, and a write
// that failed may still have applied in part. Neither may be folded into the
// other, nor into the writes the agent reported succeeding.
func TestIntervalKeepsWriteAndEditOutcomesApart(t *testing.T) {
	t.Parallel()

	f := sequence(t, analyze(newStream("a").inTurn("t1").
		shell("go-test", failing("x")).
		shell("go-test", failing("x")).
		write("/src/a.go").
		write("/src/b.go", failed).
		write("/src/c.go", unestablished("")).
		edit("/src/d.go", failed).
		shell("go-test")))

	want := "4 edit=0,1,0 write=1,1,1 [/src/a.go /src/b.go /src/c.go /src/d.go] turn=none"
	if got := interval(t, f); got != want {
		t.Errorf("interval = %q, want %q", got, want)
	}
}

// A path is retained because a call naming it was recorded, whatever became of
// the call, so the paths and the counts describe the same set of operations.
func TestIntervalPathsAreExactlyWhatWasRecorded(t *testing.T) {
	t.Parallel()

	f := sequence(t, analyze(newStream("a").inTurn("t1").
		shell("go-test", failing("x")).
		shell("go-test", failing("x")).
		edit("./src/../src/main.go").
		edit("./src/../src/main.go").
		edit("/src/main.go").
		shell("go-test")))

	// The two spellings are separate paths: normalising them would claim the
	// agent named one file when the record shows it naming two.
	want := "3 edit=3,0,0 [./src/../src/main.go /src/main.go] turn=none"
	if got := interval(t, f); got != want {
		t.Errorf("interval = %q, want %q", got, want)
	}
}

// Retention is bounded, and what the bound leaves out is counted rather than
// dropped. The operation count stays complete either way.
func TestIntervalPathRetentionIsBoundedAndTheRestCounted(t *testing.T) {
	t.Parallel()

	s := newStream("a").inTurn("t1").
		shell("go-test", failing("x")).
		shell("go-test", failing("x"))
	for i := range 8 {
		s = s.edit(fmt.Sprintf("/src/%d.go", i))
	}
	f := sequence(t, analyze(s.shell("go-test")))

	want := "8 edit=8,0,0 [/src/0.go /src/1.go /src/2.go /src/3.go /src/4.go]+3 turn=none"
	if got := interval(t, f); got != want {
		t.Errorf("interval = %q, want %q", got, want)
	}
}

// The interval is bounded by the first success, which is the observation that
// ends it. A later one is a different observation and cannot reopen it.
func TestTheFirstSuccessFreezesTheInterval(t *testing.T) {
	t.Parallel()

	f := sequence(t, analyze(newStream("a").inTurn("t1").
		shell("go-test", failing("x")).
		shell("go-test", failing("x")).
		read("/src/main.go").
		shell("go-test").
		edit("/src/other.go").
		read("/src/third.go").
		shell("go-test")))

	if got, want := interval(t, f), "1 whole=1 turn=none"; got != want {
		t.Errorf("interval = %q, want %q: work after the first success is outside it", got, want)
	}
}

// Without an observed success there is nothing to bound an interval, and the
// finding carries none rather than one that runs to the end of the log.
func TestNoIntervalWithoutAnObservedSuccess(t *testing.T) {
	t.Parallel()

	cases := map[string]*stream{
		"never tried again": newStream("a").inTurn("t1").
			shell("go-test", failing("x")).
			shell("go-test", failing("x")).
			read("/src/main.go"),

		"only another command succeeded": newStream("a").inTurn("t1").
			shell("go-test", failing("x")).
			shell("go-test", failing("x")).
			shell("go-vet"),

		// The outcome says nothing about what became of the call, so it can
		// no more end an interval than it can start a sequence.
		"the later call's outcome was never established": newStream("a").inTurn("t1").
			shell("go-test", failing("x")).
			shell("go-test", failing("x")).
			shell("go-test", unestablished("")),
	}

	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := sequence(t, analyze(s))
			if f.LaterSuccess {
				t.Fatal("LaterSuccess = true, but no success of that command was observed")
			}
			if f.Interval != nil {
				t.Errorf("Interval = %+v, want none", *f.Interval)
			}
		})
	}
}

// A success the sequence's scope does not contain is not a success of that
// sequence, so it neither reaches the finding nor bounds an interval.
func TestASuccessOutsideTheScopeFreezesNothing(t *testing.T) {
	t.Parallel()

	cases := map[string]*stream{
		// The context was discarded, so what came after belongs to an agent
		// that no longer holds the attempts.
		"after the context was reset": newStream("a").inTurn("t1").
			shell("go-test", failing("x")).
			shell("go-test", failing("x")).
			read("/src/main.go").
			sessionStart("compact").
			shell("go-test"),

		"in a later session": newStream("a").inTurn("t1").
			shell("go-test", failing("x")).
			shell("go-test", failing("x")).
			as("b").shell("go-test"),

		"in a nested agent": newStream("a").inTurn("t1").
			shell("go-test", failing("x")).
			shell("go-test", failing("x")).
			inSubagent("sub-1").shell("go-test"),
	}

	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := sequence(t, analyze(s))
			if f.LaterSuccess {
				t.Error("LaterSuccess = true, but the success was recorded outside the sequence's scope")
			}
			if f.Interval != nil {
				t.Errorf("Interval = %+v, want none", *f.Interval)
			}
		})
	}
}

// A turn boundary is where input Axiom does not observe may have arrived.
// Whether one fell inside the interval is reported, and never inferred from
// calls that happened to carry an identifier.
func TestIntervalReportsWhatIsKnownAboutTurnBoundaries(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		stream *stream
		want   profiler.TurnBoundary
	}{
		"every call reported the same turn": {
			newStream("a").inTurn("t1").
				shell("go-test", failing("x")).
				shell("go-test", failing("x")).
				read("/src/main.go").
				shell("go-test"),
			profiler.TurnBoundaryNone,
		},
		"the work between them crossed a turn": {
			newStream("a").inTurn("t1").
				shell("go-test", failing("x")).
				shell("go-test", failing("x")).
				inTurn("t2").
				read("/src/main.go").
				shell("go-test"),
			profiler.TurnBoundaryRecorded,
		},
		// Nothing at all was recorded between them and the boundary still
		// falls inside the interval, because the observations bounding it
		// reported different turns.
		"nothing was recorded and the success came in a later turn": {
			newStream("a").inTurn("t1").
				shell("go-test", failing("x")).
				shell("go-test", failing("x")).
				inTurn("t2").
				shell("go-test"),
			profiler.TurnBoundaryRecorded,
		},
		"a call between them carried no turn": {
			newStream("a").inTurn("t1").
				shell("go-test", failing("x")).
				shell("go-test", failing("x")).
				inTurn("").
				read("/src/main.go").
				inTurn("t1").
				shell("go-test"),
			profiler.TurnBoundaryUnknown,
		},
		"the success carried no turn": {
			newStream("a").inTurn("t1").
				shell("go-test", failing("x")).
				shell("go-test", failing("x")).
				inTurn("").
				shell("go-test"),
			profiler.TurnBoundaryUnknown,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := sequence(t, analyze(tc.stream))
			if f.Interval == nil {
				t.Fatalf("no interval:\n%+v", f)
			}
			if got := f.Interval.TurnBoundary; got != tc.want {
				t.Errorf("TurnBoundary = %q, want %q", got, tc.want)
			}
		})
	}
}

// The boundary question covers the closed span from the last attempt to the
// success, so both ends of it count the same way as anything between them.
//
// A turn identifier missing at either end leaves the question open rather than
// answering it, and a call outside the span cannot answer it either: what is
// compared is each pair of consecutive calls from the attempt through the
// success, and nothing before or after.
func TestTurnBoundaryTreatsBothEndsOfTheIntervalAlike(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		stream *stream
		want   string
	}{
		// Both ends reported a turn and the two differed, with nothing at all
		// recorded between them. The boundary is still inside the interval.
		"the two ends reported different turns": {
			newStream("a").inTurn("t1").
				shell("go-test", failing("x")).
				shell("go-test", failing("x")).
				inTurn("t2").
				shell("go-test"),
			"0 turn=recorded",
		},
		"the success reported no turn": {
			newStream("a").inTurn("t1").
				shell("go-test", failing("x")).
				shell("go-test", failing("x")).
				inTurn("").
				shell("go-test"),
			"0 turn=unknown",
		},
		"a call between them reported no turn": {
			newStream("a").inTurn("t1").
				shell("go-test", failing("x")).
				shell("go-test", failing("x")).
				inTurn("").
				read("/src/main.go").
				inTurn("t1").
				shell("go-test"),
			"1 whole=1 turn=unknown",
		},
		// The read is outside the span, and so is the gap between it and the
		// first attempt. Neither may reach the interval.
		"a call before the attempts reported no turn": {
			newStream("a").inTurn("").
				read("/src/main.go").
				inTurn("t1").
				shell("go-test", failing("x")).
				shell("go-test", failing("x")).
				shell("go-test"),
			"0 turn=none",
		},
		// The interval is frozen by the success, so what follows it is outside
		// the span at the other end.
		"a call after the success reported no turn": {
			newStream("a").inTurn("t1").
				shell("go-test", failing("x")).
				shell("go-test", failing("x")).
				shell("go-test").
				inTurn("").
				read("/src/main.go"),
			"0 turn=none",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := interval(t, sequence(t, analyze(tc.stream))); got != tc.want {
				t.Errorf("interval = %q, want %q", got, tc.want)
			}
		})
	}
}

// The start of an interval is never a call that reported no turn, because such
// a call cannot join a sequence of failed attempts in the first place.
//
// That is what makes the case above vacuous from this side rather than
// asymmetric: an attempt with no turn is not an endpoint, it is an operation
// recorded inside the interval of the sequence it ended, and it is counted as
// one. These pin the guard that keeps it so.
func TestATurnlessAttemptIsNotAnEndpoint(t *testing.T) {
	t.Parallel()

	t.Run("it falls inside the interval of the sequence it ended", func(t *testing.T) {
		t.Parallel()

		f := sequence(t, analyze(newStream("a").inTurn("t1").
			shell("go-test", failing("x")).
			shell("go-test", failing("x")).
			inTurn("").
			shell("go-test", failing("x")).
			inTurn("t1").
			shell("go-test")))

		// The sequence holds the two attempts that reported a turn, so the
		// interval starts after the second and the third is inside it.
		if f.Occurrences != 2 {
			t.Errorf("Occurrences = %d, want 2: an attempt with no turn cannot join the sequence", f.Occurrences)
		}
		if got, want := interval(t, f), "1 shell=1 turn=unknown"; got != want {
			t.Errorf("interval = %q, want %q", got, want)
		}
	})

	t.Run("no sequence forms when no attempt reported a turn", func(t *testing.T) {
		t.Parallel()

		report := analyze(newStream("a").inTurn("").
			shell("go-test", failing("x")).
			shell("go-test", failing("x")).
			shell("go-test", failing("x")).
			shell("go-test"))

		if len(report.Findings) != 0 {
			t.Errorf("got %d findings, want none: no attempt could join a sequence:\n%+v",
				len(report.Findings), report.Findings)
		}
	})
}

// Two commands failing in the same scope have intervals of their own, each
// bounded by its own last attempt and its own first later success. Whatever
// falls inside both belongs to both.
func TestSequencesOfDifferentCommandsGetIntervalsOfTheirOwn(t *testing.T) {
	t.Parallel()

	report := analyze(newStream("a").inTurn("t1").
		shell("go-test", failing("x")).
		shell("go-test", failing("x")).
		shell("go-vet", failing("y")).
		shell("go-vet", failing("y")).
		shell("go-test").
		shell("go-vet"))

	got := map[string]string{}
	for _, f := range report.Findings {
		if f.Kind == profiler.KindRepeatedFailure {
			got[f.CommandDigest] = interval(t, f)
		}
	}
	want := map[string]string{
		// The two attempts of the other command.
		"go-test": "2 shell=2 turn=none",
		// The other command being observed succeeding.
		"go-vet": "1 shell=1 turn=none",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("intervals = %v, want %v", got, want)
	}
}

// Where a command fails, is interrupted by a barrier, and fails again before
// being observed succeeding, both sequences are awaiting the same success and
// both are frozen by it. The intervals nest: the earlier one contains the
// later sequence's attempts, and the later one contains nothing.
func TestIntervalsNest(t *testing.T) {
	t.Parallel()

	report := analyze(newStream("a").inTurn("t1").
		shell("go-test", failing("x")).
		shell("go-test", failing("x")).
		unrecognised("mcp__tracker__issue").
		shell("go-test", failing("x")).
		shell("go-test", failing("x")).
		shell("go-test"))

	var got []string
	for _, f := range report.Findings {
		if f.Kind == profiler.KindRepeatedFailure {
			got = append(got, interval(t, f))
		}
	}
	want := []string{"3 shell=2 uninterpreted=1 turn=none", "0 turn=none"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("intervals = %v, want %v", got, want)
	}
}

// Two sequences of the same command in one scope are separate findings, and
// the success that ends the second cannot reach back into the first.
func TestOnlyTheSequencesAwaitingASuccessAreFrozen(t *testing.T) {
	t.Parallel()

	report := analyze(newStream("a").inTurn("t1").
		shell("go-test", failing("x")).
		shell("go-test", failing("x")).
		read("/src/a.go").
		shell("go-test").
		shell("go-test", failing("x")).
		shell("go-test", failing("x")).
		read("/src/b.go").
		read("/src/c.go").
		shell("go-test"))

	var got []string
	for _, f := range report.Findings {
		if f.Kind == profiler.KindRepeatedFailure {
			got = append(got, interval(t, f))
		}
	}
	want := []string{"1 whole=1 turn=none", "2 whole=2 turn=none"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("intervals = %v, want %v", got, want)
	}
}

// Analysis is a function of the log. Nothing about an interval may depend on
// map ordering or on anything else that varies between runs of the same input.
func TestIntervalsAreDeterministic(t *testing.T) {
	t.Parallel()

	build := func() *stream {
		s := newStream("a").inTurn("t1").
			shell("go-test", failing("x")).
			shell("go-test", failing("x")).
			shell("go-vet", failing("y")).
			shell("go-vet", failing("y")).
			search("TODO").
			unrecognised("NotebookEdit")
		for i := range 8 {
			s = s.edit(fmt.Sprintf("/src/%d.go", i))
		}
		return s.shell("go-test").shell("go-vet")
	}

	var first []string
	for run := range 12 {
		var got []string
		for _, f := range analyze(build()).Findings {
			if f.Kind == profiler.KindRepeatedFailure {
				got = append(got, f.CommandDigest+" "+interval(t, f))
			}
		}
		if run == 0 {
			first = got
			continue
		}
		if fmt.Sprint(got) != fmt.Sprint(first) {
			t.Fatalf("run %d reported %v, want %v", run, got, first)
		}
	}
}
