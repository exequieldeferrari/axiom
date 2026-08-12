package cli

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/delegation"
	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/work"
)

// identifiedLaunch is a launch whose record carried the identity the agent
// returned for the nested agent it created.
func identifiedLaunch(session, turn, invocation, returns string, when time.Time) event.Event {
	ev := launchIn(session, turn, when, event.OutcomeSuccess)
	ev.Tool.InvocationID = invocation
	ev.Tool.Result = &event.ToolResult{Subagent: &event.SubagentResult{AgentID: returns}}
	return ev
}

// nestedRead is a whole-file read one nested agent made.
func nestedRead(session, turn, path, by string, when time.Time) event.Event {
	ev := readIn(session, turn, path, when)
	ev.SubagentID = by
	return ev
}

func nestedShell(session, turn, by string, when time.Time) event.Event {
	ev := shellIn(session, turn, "digest-nested", when)
	ev.SubagentID = by
	return ev
}

// The three states a launch can be in have to be distinguishable on the page,
// because reading any of them as another answers from evidence the log does
// not hold.
func TestProfileDistinguishesTheThreeLaunchStates(t *testing.T) {
	t.Parallel()

	out := turnsSection(t, profileOutput(t, seed(t,
		startEvent("session-1", "startup", at(0)),
		identifiedLaunch("session-1", "turn-1", "call-1", "agent-1", at(time.Second)),
		nestedRead("session-1", "turn-1", "/repo/a.go", "agent-1", at(2*time.Second)),
		nestedShell("session-1", "turn-1", "agent-1", at(3*time.Second)),
		identifiedLaunch("session-1", "turn-1", "call-2", "agent-2", at(4*time.Second)),
		launchIn("session-1", "turn-1", at(5*time.Second), event.OutcomeSuccess),
	)))

	for _, want := range []string{
		// Identity recorded, with work reporting it.
		"subagent launch 1  ·  2 calls\n           1 whole-file read, 1 shell call",
		// Identity recorded, nothing in the log reported it.
		"subagent launch 2\n           no calls recorded with its returned identity",
		// No identity recorded at all.
		"1 launch with no returned agent identity recorded",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the launch states are not distinguishable, missing %q:\n%s", want, out)
		}
	}
	// The third launch keeps its place in the numbering rather than being
	// renumbered around, and is never described as one with no work.
	if strings.Contains(out, "subagent launch 3") {
		t.Errorf("a launch with no identity was described as one that had work:\n%s", out)
	}
	rejectOverlongLines(t, out)
}

// A launch with nothing reporting its identity describes the log. The report
// must not say the agent did nothing.
func TestProfileDoesNotClaimAnAgentDidNoWork(t *testing.T) {
	t.Parallel()

	out := flat(turnsSection(t, profileOutput(t, seed(t,
		startEvent("session-1", "startup", at(0)),
		identifiedLaunch("session-1", "turn-1", "call-1", "agent-1", at(time.Second)),
	))))

	if !strings.Contains(out, "no calls recorded with its returned identity") {
		t.Errorf("a launch with nothing reporting its identity is not stated:\n%s", out)
	}
	for _, forbidden := range []string{
		"performed no", "did no work", "the agent did no", "no tool work", "0 calls",
	} {
		if strings.Contains(out, forbidden) {
			t.Errorf("the report claims the agent did nothing, saying %q:\n%s", forbidden, out)
		}
	}
}

// Two launches in one turn, with their work interleaved. Each holds its own,
// and the report never merges them.
func TestProfileKeepsParallelLaunchesApart(t *testing.T) {
	t.Parallel()

	out := turnsSection(t, profileOutput(t, seed(t,
		startEvent("session-1", "startup", at(0)),
		identifiedLaunch("session-1", "turn-1", "call-1", "agent-1", at(time.Second)),
		identifiedLaunch("session-1", "turn-1", "call-2", "agent-2", at(2*time.Second)),
		nestedShell("session-1", "turn-1", "agent-1", at(3*time.Second)),
		nestedRead("session-1", "turn-1", "/repo/a.go", "agent-2", at(4*time.Second)),
		nestedRead("session-1", "turn-1", "/repo/b.go", "agent-1", at(5*time.Second)),
		nestedRead("session-1", "turn-1", "/repo/c.go", "agent-1", at(6*time.Second)),
	)))

	for _, want := range []string{
		"Subagent launches             2",
		"subagent launch 1  ·  3 calls\n           2 whole-file reads, 1 shell call",
		"subagent launch 2  ·  1 call\n           1 whole-file read",
		"Calls by a nested agent       4",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the parallel launches are not held apart, missing %q:\n%s", want, out)
		}
	}
	rejectOverlongLines(t, out)
}

// The relation is the session and the returned identity. Nested calls that
// named another turn still belong to the launch that returned their identity,
// and the launch is still shown under the turn its own call was recorded in.
func TestProfileRelatesWorkAcrossTurnIdentifiers(t *testing.T) {
	t.Parallel()

	out := turnsSection(t, profileOutput(t, seed(t,
		startEvent("session-1", "startup", at(0)),
		identifiedLaunch("session-1", "turn-1", "call-1", "agent-1", at(time.Second)),
		nestedRead("session-1", "turn-1", "/repo/a.go", "agent-1", at(2*time.Second)),
		nestedRead("session-1", "turn-2", "/repo/b.go", "agent-1", at(3*time.Second)),
	)))

	if !strings.Contains(out, "subagent launch 1  ·  2 calls\n           2 whole-file reads") {
		t.Errorf("work that named another turn was dropped from the launch:\n%s", out)
	}
	// The launch belongs to the turn that recorded it, and the second turn
	// records only the call that named it.
	first, second, ok := strings.Cut(out, "turn 2")
	if !ok {
		t.Fatalf("the second turn is missing:\n%s", out)
	}
	if !strings.Contains(first, "subagent launch 1") {
		t.Errorf("the launch is not shown under the turn that recorded it:\n%s", out)
	}
	if strings.Contains(second, "subagent launch") {
		t.Errorf("the launch was repeated under a turn that did not record it:\n%s", out)
	}
}

// Nested work no recorded launch accounts for is stated and never given to a
// launch nearby.
func TestProfileAccountsForUnrelatedNestedWork(t *testing.T) {
	t.Parallel()

	out := turnsSection(t, profileOutput(t, seed(t,
		startEvent("session-1", "startup", at(0)),
		nestedRead("session-1", "turn-1", "/repo/gone.go", "agent-gone", at(time.Second)),
		nestedShell("session-1", "turn-1", "agent-gone", at(2*time.Second)),
		nestedRead("session-1", "turn-1", "/repo/other.go", "agent-other", at(3*time.Second)),
		identifiedLaunch("session-1", "turn-1", "call-1", "agent-1", at(4*time.Second)),
		nestedRead("session-1", "turn-1", "/repo/a.go", "agent-1", at(5*time.Second)),
	)))

	if !strings.Contains(flat(out),
		"3 recorded calls by a nested agent reported an agent identity that no recorded launch returned, across 2 agent identities") {
		t.Errorf("unrelated nested work is unaccounted for:\n%s", out)
	}
	if !strings.Contains(out, "subagent launch 1  ·  1 call\n           1 whole-file read") {
		t.Errorf("the launch absorbed work that was not its own:\n%s", out)
	}
}

// The claim the launch lines make is exact, and the report may make no other.
func TestProfileStatesTheRelationExactly(t *testing.T) {
	t.Parallel()

	out := flat(turnsSection(t, profileOutput(t, seed(t,
		startEvent("session-1", "startup", at(0)),
		identifiedLaunch("session-1", "turn-1", "call-1", "agent-1", at(time.Second)),
		nestedRead("session-1", "turn-1", "/repo/a.go", "agent-1", at(2*time.Second)),
	))))

	for _, want := range []string{
		"the recorded calls that reported that same identity",
		"That is the whole claim",
		"does not establish that everything the agent did reached the log",
		"not a breakdown of calls by a nested agent above",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the relation is not stated exactly, missing %q:\n%s", want, out)
		}
	}

	// Nothing in the section may read as a judgement of the delegation, a
	// causal claim, or an attribution of consumption to an agent.
	for _, forbidden := range []string{
		"caused", "efficient", "inefficient", "wasted", "waste", "useful",
		"helped", "unnecessary", "redundant", "should", "recommend",
		"the subagent cost", "agent cost", "cost of the launch",
	} {
		if strings.Contains(out, forbidden) {
			t.Errorf("the section makes a claim it cannot defend, saying %q:\n%s", forbidden, out)
		}
	}
}

// The identity is what Axiom matched on and names nothing a reader can use.
func TestProfileDoesNotPrintTheReturnedIdentity(t *testing.T) {
	t.Parallel()

	const agent = "aa727c39085ae1c77"
	out := profileOutput(t, seed(t,
		startEvent("session-1", "startup", at(0)),
		identifiedLaunch("session-1", "turn-1", "call-1", agent, at(time.Second)),
		nestedRead("session-1", "turn-1", "/repo/a.go", agent, at(2*time.Second)),
	))

	if strings.Contains(turnsSection(t, out), agent) {
		t.Errorf("the returned identity was printed:\n%s", out)
	}
}

// A turn that delegated many times is bounded, and what is left out is
// accounted for rather than dropped.
func TestProfileBoundsTheLaunchesItDescribes(t *testing.T) {
	t.Parallel()

	events := []event.Event{startEvent("session-1", "startup", at(0))}
	for i := range launchesShown + 2 {
		agent := fmt.Sprintf("agent-%d", i)
		when := at(time.Duration(i+1) * time.Second)
		events = append(events,
			identifiedLaunch("session-1", "turn-1", fmt.Sprintf("call-%d", i), agent, when),
			nestedRead("session-1", "turn-1", "/repo/a.go", agent, when.Add(100*time.Millisecond)))
	}

	out := turnsSection(t, profileOutput(t, seed(t, events...)))

	if got := strings.Count(out, "subagent launch "); got != launchesShown {
		t.Errorf("%d launches were described, want %d:\n%s", got, launchesShown, out)
	}
	if !strings.Contains(out, "2 further launches not described") {
		t.Errorf("the launches left out are unaccounted for:\n%s", out)
	}
	if !strings.Contains(out, "Subagent launches             7") {
		t.Errorf("the total count of launches changed with the bound:\n%s", out)
	}
	rejectOverlongLines(t, out)
}

// A turn that delegated nothing says nothing about delegation, and neither
// explanation is printed where the page holds nothing it explains.
func TestProfileOmitsLaunchExplanationsWithoutLaunches(t *testing.T) {
	t.Parallel()

	out := flat(turnsSection(t, profileOutput(t, seed(t,
		startEvent("session-1", "startup", at(0)),
		readIn("session-1", "turn-1", "/repo/a.go", at(time.Second)),
	))))

	for _, absent := range []string{
		"subagent launch", "returned agent identity", "That is the whole claim",
	} {
		if strings.Contains(out, absent) {
			t.Errorf("a turn with no delegation printed %q:\n%s", absent, out)
		}
	}
}

// A description is fitted to the width the report is written to, and what does
// not fit is counted rather than dropped.
func TestLaunchDescriptionFitsTheReportWidth(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		done delegation.Work
		want string
	}{
		"nothing reported it": {
			done: delegation.Work{},
			want: "no calls recorded with its returned identity",
		},
		"one call": {
			done: delegation.Work{Calls: 1, Composition: work.Composition{WholeReads: 1}},
			want: "1 whole-file read",
		},
		"every shape a turn distinguishes": {
			done: delegation.Work{Calls: 6, Composition: work.Composition{
				WholeReads: 1, RangedReads: 1, Searches: 1, Shell: 1,
				Writes:   work.Outcomes{Succeeded: 1},
				Launches: work.Outcomes{Succeeded: 1},
			}},
			want: "1 whole-file read, 1 ranged read, 1 search, and 3 more",
		},
		"outcomes are carried": {
			done: delegation.Work{Calls: 3, Composition: work.Composition{
				Writes: work.Outcomes{Succeeded: 1, Failed: 1},
				Edits:  work.Outcomes{Unestablished: 1},
			}},
			want: "2 writes (1 not established), 1 edit (1 not established)",
		},
		"every category at once is counted rather than cut": {
			done: delegation.Work{Calls: 800, Composition: work.Composition{
				WholeReads: 100, RangedReads: 100, Searches: 100, Shell: 100,
				Writes:        work.Outcomes{Succeeded: 100},
				Edits:         work.Outcomes{Succeeded: 100},
				Launches:      work.Outcomes{Succeeded: 100},
				Uninterpreted: 100,
			}},
			want: "100 whole-file reads, 100 ranged reads, 100 searches, and 5 more",
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := describeLaunch(c.done)
			if got != c.want {
				t.Errorf("describeLaunch = %q, want %q", got, c.want)
			}
			if width := len(launchDetailIndent) + len(got); width > reportWidth {
				t.Errorf("the line runs to %d characters, past the report's %d", width, reportWidth)
			}
		})
	}
}

// The capture this feature was built from, in the shapes it produced.
//
// A synchronous launch reaches a hook only once its call has returned, so its
// nested calls are recorded before it. An asynchronous one is recorded first
// and its calls arrive after. Two agents launched together interleaved their
// work between the launches. A launch against an agent type that did not exist
// reported failing and returned nothing. A live agent's work was recorded under
// a turn identifier other than its launch's. And the log holds nested calls
// from an agent whose launch is not in it.
func TestProfileReplaysTheDelegationCapture(t *testing.T) {
	t.Parallel()

	const session = "5eff7948-a49b-459b-9884-e1bc6e47d627"
	out := turnsSection(t, profileOutput(t, seed(t,
		startEvent(session, "startup", at(0)),

		// Turn 1: a synchronous launch, recorded after the work it produced.
		nestedRead(session, "turn-1", "/proj/main.go", "agent-sync", at(time.Second)),
		nestedShell(session, "turn-1", "agent-sync", at(2*time.Second)),
		identifiedLaunch(session, "turn-1", "call-1", "agent-sync", at(3*time.Second)),

		// Turn 2: two launched together, their work interleaved, and an
		// asynchronous one whose work arrives later still.
		identifiedLaunch(session, "turn-2", "call-2", "agent-par-1", at(10*time.Second)),
		identifiedLaunch(session, "turn-2", "call-3", "agent-par-2", at(11*time.Second)),
		nestedRead(session, "turn-2", "/proj/a.go", "agent-par-2", at(12*time.Second)),
		nestedRead(session, "turn-2", "/proj/b.go", "agent-par-1", at(13*time.Second)),
		identifiedLaunch(session, "turn-2", "call-4", "agent-async", at(14*time.Second)),
		// The asynchronous agent was still running when the next turn began,
		// and its work named that turn instead.
		nestedRead(session, "turn-3", "/proj/c.go", "agent-async", at(20*time.Second)),

		// Turn 3: a launch that reported failing, and one whose identity
		// nothing in the log reported.
		launchIn(session, "turn-3", at(21*time.Second), event.OutcomeFailure),
		identifiedLaunch(session, "turn-3", "call-6", "agent-quiet", at(22*time.Second)),
		// Work from an agent whose launch is not in this log.
		nestedShell(session, "turn-3", "agent-elsewhere", at(23*time.Second)),

		endEvent(session, "clear", at(30*time.Second)),
	)))

	for _, want := range []string{
		// The synchronous launch holds the work recorded before it.
		"subagent launch 1  ·  2 calls\n           1 whole-file read, 1 shell call",
		// Neither parallel agent holds the other's.
		"subagent launch 1  ·  1 call\n           1 whole-file read",
		"subagent launch 2  ·  1 call\n           1 whole-file read",
		// The asynchronous agent's work named another turn and is still its
		// launch's.
		"subagent launch 3  ·  1 call\n           1 whole-file read",
		// The failing launch returned nothing to match on.
		"1 launch with no returned agent identity recorded",
		// The launch nothing reported.
		"subagent launch 2\n           no calls recorded with its returned identity",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the capture does not replay, missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(flat(out),
		"1 recorded call by a nested agent reported an agent identity that no recorded launch returned, across 1 agent identity") {
		t.Errorf("the work with no launch in the log is unaccounted for:\n%s", out)
	}
	rejectOverlongLines(t, out)
}

// The counts a turn already reported must be exactly what they were: this
// change adds a relation and measures nothing new.
func TestProfileLeavesTheTurnCountsUnchanged(t *testing.T) {
	t.Parallel()

	events := []event.Event{
		startEvent("session-1", "startup", at(0)),
		readIn("session-1", "turn-1", "/repo/a.go", at(time.Second)),
		identifiedLaunch("session-1", "turn-1", "call-1", "agent-1", at(2*time.Second)),
		nestedRead("session-1", "turn-1", "/repo/b.go", "agent-1", at(3*time.Second)),
	}
	withIdentity := turnsSection(t, profileOutput(t, seed(t, events...)))

	// The same log, written before the identity was persisted.
	events[2].Tool.Result = nil
	without := turnsSection(t, profileOutput(t, seed(t, events...)))

	for _, want := range []string{
		"Tool calls                    3",
		"Whole-file reads              2",
		"Subagent launches             1",
		"Calls by a nested agent       1",
	} {
		if !strings.Contains(withIdentity, want) {
			t.Errorf("a recorded count changed, missing %q:\n%s", want, withIdentity)
		}
		if !strings.Contains(without, want) {
			t.Errorf("a historical count changed, missing %q:\n%s", want, without)
		}
	}
	if !strings.Contains(without, "1 launch with no returned agent identity recorded") {
		t.Errorf("the historical launch does not say its identity is unrecorded:\n%s", without)
	}
	if strings.Contains(without, "subagent launch 1") {
		t.Errorf("a historical launch was related to a call:\n%s", without)
	}
}
