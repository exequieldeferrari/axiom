package cli

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/event"
)

// crossreadSection returns just the section, so a test cannot pass on wording
// that happens to appear somewhere else in the report.
func crossreadSection(t *testing.T, out string) string {
	t.Helper()

	_, after, ok := strings.Cut(out, "\nRead across related agent scopes\n")
	if !ok {
		t.Fatalf("the report has no cross-scope reading section:\n%s", out)
	}
	section, _, ok := strings.Cut(after, "\nRead again in a later context epoch\n")
	if !ok {
		t.Fatalf("the cross-scope reading section does not end:\n%s", out)
	}
	return section
}

// nestedLaunch is a launch one nested agent made, which carries the identity
// of the agent that made it and the identity of the agent it created.
func nestedLaunch(session, turn, invocation, by, returns string, when time.Time) event.Event {
	ev := identifiedLaunch(session, turn, invocation, returns, when)
	ev.SubagentID = by
	return ev
}

// The shape a capture produced: one scope delegates twice, and the file it had
// already read is read again inside both agents it launched.
func TestProfileReportsReadingAcrossRelatedScopes(t *testing.T) {
	t.Parallel()

	const shared = "/repo/internal/store/store.go"
	section := crossreadSection(t, profileOutput(t, seed(t,
		startEvent("session-1", "startup", at(0)),
		readIn("session-1", "turn-1", shared, at(time.Second)),
		readIn("session-1", "turn-1", shared, at(2*time.Second)),
		identifiedLaunch("session-1", "turn-1", "call-1", "agent-a", at(3*time.Second)),
		identifiedLaunch("session-1", "turn-1", "call-2", "agent-b", at(4*time.Second)),
		nestedRead("session-1", "turn-1", shared, "agent-a", at(5*time.Second)),
		nestedRead("session-1", "turn-1", shared, "agent-b", at(6*time.Second)),
		nestedRead("session-1", "turn-1", "/repo/only-once.go", "agent-b", at(7*time.Second)),
	)))

	assertContains(t, section,
		shared,
		"session session-1",
		"the session scope and the agents it launched",
		"the session scope, 2 reads",
		"agent 1, 1 read",
		"agent 2, 1 read",
		"1 path read in more than one related agent scope.",
	)
	// A path only one scope read is not a relation.
	if strings.Contains(section, "/repo/only-once.go") {
		t.Errorf("a path read in one scope was reported:\n%s", section)
	}
	rejectOverlongLines(t, profileOutput(t, seed(t,
		startEvent("session-1", "startup", at(0)),
		identifiedLaunch("session-1", "turn-1", "call-1", "agent-a", at(time.Second)),
		nestedRead("session-1", "turn-1", "/repo/a.go", "agent-a", at(2*time.Second)),
		readIn("session-1", "turn-1", "/repo/a.go", at(3*time.Second)),
	)))
}

// Two agents launched by one scope, reading a path that scope never read. The
// fan-out is the relation, and the launching scope names the group without
// appearing in it.
func TestProfileReportsSiblingScopes(t *testing.T) {
	t.Parallel()

	section := crossreadSection(t, profileOutput(t, seed(t,
		startEvent("session-1", "startup", at(0)),
		identifiedLaunch("session-1", "turn-1", "call-1", "agent-a", at(time.Second)),
		identifiedLaunch("session-1", "turn-1", "call-2", "agent-b", at(2*time.Second)),
		nestedRead("session-1", "turn-1", "/repo/a.go", "agent-a", at(3*time.Second)),
		nestedRead("session-1", "turn-1", "/repo/a.go", "agent-b", at(4*time.Second)),
	)))

	assertContains(t, section,
		"/repo/a.go",
		"the session scope and the agents it launched",
		"agent 1, 1 read",
		"agent 2, 1 read",
	)
	if strings.Contains(section, "the session scope, ") {
		t.Errorf("a scope that read nothing was listed as one that read:\n%s", section)
	}
}

// A nested agent that launches another is the launching scope of its own
// group, and the group is named after it.
func TestProfileReportsNestedDelegation(t *testing.T) {
	t.Parallel()

	section := crossreadSection(t, profileOutput(t, seed(t,
		startEvent("session-1", "startup", at(0)),
		identifiedLaunch("session-1", "turn-1", "call-1", "agent-a", at(time.Second)),
		nestedLaunch("session-1", "turn-1", "call-2", "agent-a", "agent-b", at(2*time.Second)),
		nestedRead("session-1", "turn-1", "/repo/a.go", "agent-a", at(3*time.Second)),
		nestedRead("session-1", "turn-1", "/repo/a.go", "agent-b", at(4*time.Second)),
	)))

	assertContains(t, section,
		"/repo/a.go",
		"agent 1 and the agents it launched",
		"agent 1, 1 read",
		"agent 2, 1 read",
	)
}

// The relation is not followed transitively. A scope and the agent its own
// agent launched are two steps apart, and two steps are not compared.
func TestProfileDoesNotRelateScopesTransitively(t *testing.T) {
	t.Parallel()

	section := crossreadSection(t, profileOutput(t, seed(t,
		startEvent("session-1", "startup", at(0)),
		identifiedLaunch("session-1", "turn-1", "call-1", "agent-a", at(time.Second)),
		nestedLaunch("session-1", "turn-1", "call-2", "agent-a", "agent-b", at(2*time.Second)),
		readIn("session-1", "turn-1", "/repo/a.go", at(3*time.Second)),
		nestedRead("session-1", "turn-1", "/repo/a.go", "agent-b", at(4*time.Second)),
	)))

	assertContains(t, section, "No path was read in more than one related agent scope")
	if strings.Contains(section, "/repo/a.go") {
		t.Errorf("two scopes with no launch between them were compared:\n%s", section)
	}
}

// Nested work reaching the log before the launch that names it is the ordinary
// shape of a synchronous launch, and must produce the same report.
func TestProfileRelatesScopesWhateverTheOrder(t *testing.T) {
	t.Parallel()

	before := crossreadSection(t, profileOutput(t, seed(t,
		startEvent("session-1", "startup", at(0)),
		readIn("session-1", "turn-1", "/repo/a.go", at(time.Second)),
		nestedRead("session-1", "turn-1", "/repo/a.go", "agent-a", at(2*time.Second)),
		identifiedLaunch("session-1", "turn-1", "call-1", "agent-a", at(3*time.Second)),
	)))
	after := crossreadSection(t, profileOutput(t, seed(t,
		startEvent("session-1", "startup", at(0)),
		readIn("session-1", "turn-1", "/repo/a.go", at(time.Second)),
		identifiedLaunch("session-1", "turn-1", "call-1", "agent-a", at(2*time.Second)),
		nestedRead("session-1", "turn-1", "/repo/a.go", "agent-a", at(3*time.Second)),
	)))

	assertContains(t, before, "/repo/a.go", "the session scope, 1 read", "agent 1, 1 read")
	if before != after {
		t.Errorf("the order the records arrived in changed the report:\n%s\n---\n%s", before, after)
	}
}

// The four empty states mean different things and have to read differently.
func TestCrossReadSectionEmptyStates(t *testing.T) {
	t.Parallel()

	t.Run("nothing delegated", func(t *testing.T) {
		t.Parallel()
		section := crossreadSection(t, profileOutput(t, seed(t,
			startEvent("session-1", "startup", at(0)),
			readIn("session-1", "turn-1", "/repo/a.go", at(time.Second)),
		)))
		assertContains(t, section, "No recorded call handed work to a nested agent")
		// A log that delegated nothing has already been accounted for by
		// the line above.
		if strings.Contains(section, "set aside") {
			t.Errorf("a log with no delegation counted reading it set aside:\n%s", section)
		}
	})

	t.Run("launches with no returned identity", func(t *testing.T) {
		t.Parallel()
		section := crossreadSection(t, profileOutput(t, seed(t,
			startEvent("session-1", "startup", at(0)),
			launchIn("session-1", "turn-1", at(time.Second), event.OutcomeSuccess),
			launchIn("session-1", "turn-1", at(2*time.Second), event.OutcomeFailure),
			readIn("session-1", "turn-1", "/repo/a.go", at(3*time.Second)),
		)))
		assertSentence(t, section,
			"2 launches recorded, and none of them carried a returned agent identity, so no delegated scope was established to compare.")
	})

	t.Run("related scopes that read nothing", func(t *testing.T) {
		t.Parallel()
		section := crossreadSection(t, profileOutput(t, seed(t,
			startEvent("session-1", "startup", at(0)),
			identifiedLaunch("session-1", "turn-1", "call-1", "agent-a", at(time.Second)),
			nestedShell("session-1", "turn-1", "agent-a", at(2*time.Second)),
		)))
		assertContains(t, section,
			"No scope taking part in a delegation relation recorded a successful")
	})

	t.Run("related scopes with nothing read in common", func(t *testing.T) {
		t.Parallel()
		section := crossreadSection(t, profileOutput(t, seed(t,
			startEvent("session-1", "startup", at(0)),
			identifiedLaunch("session-1", "turn-1", "call-1", "agent-a", at(time.Second)),
			readIn("session-1", "turn-1", "/repo/a.go", at(2*time.Second)),
			nestedRead("session-1", "turn-1", "/repo/b.go", "agent-a", at(3*time.Second)),
		)))
		assertContains(t, section, "No path was read in more than one related agent scope")
	})
}

// A nested agent whose identity no launch returned takes part in nothing, and
// its reading must not disappear from the report either.
func TestCrossReadSectionAccountsForUnrelatedScopes(t *testing.T) {
	t.Parallel()

	section := crossreadSection(t, profileOutput(t, seed(t,
		startEvent("session-1", "startup", at(0)),
		identifiedLaunch("session-1", "turn-1", "call-1", "agent-a", at(time.Second)),
		readIn("session-1", "turn-1", "/repo/a.go", at(2*time.Second)),
		nestedRead("session-1", "turn-1", "/repo/a.go", "agent-a", at(3*time.Second)),
		// An orphan: no recorded launch returned this identity.
		nestedRead("session-1", "turn-1", "/repo/a.go", "agent-orphan", at(4*time.Second)),
		nestedRead("session-1", "turn-1", "/repo/b.go", "agent-orphan", at(5*time.Second)),
	)))

	assertContains(t, section, "the session scope, 1 read", "agent 1, 1 read")
	// The orphan read the same path and is not in the group.
	if strings.Contains(section, "agent 2,") {
		t.Errorf("nested work with no launch was attached to a group:\n%s", section)
	}
	assertSentence(t, section,
		"2 successful whole-file reads recorded in scopes that no launch relates to another were set aside.")
}

// The wording is the product here. Every one of these would state something the
// evidence does not establish.
func TestCrossReadSectionAvoidsUnsupportedWording(t *testing.T) {
	t.Parallel()

	section := strings.ToLower(crossreadSection(t, profileOutput(t, seed(t,
		startEvent("session-1", "startup", at(0)),
		readIn("session-1", "turn-1", "/repo/a.go", at(time.Second)),
		identifiedLaunch("session-1", "turn-1", "call-1", "agent-a", at(2*time.Second)),
		identifiedLaunch("session-1", "turn-1", "call-2", "agent-b", at(3*time.Second)),
		nestedRead("session-1", "turn-1", "/repo/a.go", "agent-a", at(4*time.Second)),
		nestedRead("session-1", "turn-1", "/repo/a.go", "agent-b", at(5*time.Second)),
		nestedRead("session-1", "turn-1", "/repo/b.go", "agent-orphan", at(6*time.Second)),
	))))

	for _, reject := range []string{
		"redundant", "redundancy", "waste", "wasted", "unnecessary", "avoidable",
		"inefficien", "duplicate", "should", "optimi", "caused", "because",
		"saved", "savings", "required", "reread", "re-read", "overlap",
		"handoff", "cost", "token", "recommend", "instead of",
	} {
		if strings.Contains(section, reject) {
			t.Errorf("the section claims more than the evidence supports with %q:\n%s", reject, section)
		}
	}
}

// The section states what a relation is, what it is not, and what it refuses
// to infer.
func TestCrossReadSectionStatesItsLimits(t *testing.T) {
	t.Parallel()

	section := crossreadSection(t, profileOutput(t, seed(t,
		startEvent("session-1", "startup", at(0)),
		readIn("session-1", "turn-1", "/repo/a.go", at(time.Second)),
		identifiedLaunch("session-1", "turn-1", "call-1", "agent-a", at(2*time.Second)),
		nestedRead("session-1", "turn-1", "/repo/a.go", "agent-a", at(3*time.Second)),
	)))

	assertSentence(t, section,
		"Scopes are related only through a launch whose record carried the agent identity it returned.",
		"Timing, proximity, turn identifiers and tool names take no part in any of it.",
		"A group is one launching scope together with the scopes it launched directly.",
		"Nothing here is an ordering.",
		"Paths are compared as the exact strings the agent recorded.",
		"the numbering is Axiom's own, and it names no agent outside this report.",
	)
}

// A path is one exact recorded string. Two spellings of one file are two
// paths, which loses a relation rather than inventing one.
func TestCrossReadSectionComparesExactPaths(t *testing.T) {
	t.Parallel()

	section := crossreadSection(t, profileOutput(t, seed(t,
		startEvent("session-1", "startup", at(0)),
		readIn("session-1", "turn-1", "/tmp/proj/a.go", at(time.Second)),
		identifiedLaunch("session-1", "turn-1", "call-1", "agent-a", at(2*time.Second)),
		nestedRead("session-1", "turn-1", "/private/tmp/proj/a.go", "agent-a", at(3*time.Second)),
	)))

	assertContains(t, section, "No path was read in more than one related agent scope")
}

// Only a read the record establishes delivered a whole file counts.
func TestCrossReadSectionCountsOnlyQualifyingReads(t *testing.T) {
	t.Parallel()

	ranged := nestedRead("session-1", "turn-1", "/repo/ranged.go", "agent-a", at(4*time.Second))
	offset := 10
	ranged.Tool.Metadata.File.Offset = &offset

	failed := nestedRead("session-1", "turn-1", "/repo/failed.go", "agent-a", at(5*time.Second))
	failed.Tool.Outcome = event.OutcomeFailure

	unestablished := nestedRead("session-1", "turn-1", "/repo/unestablished.go", "agent-a", at(6*time.Second))
	unestablished.Tool.Outcome = event.Outcome("")

	empty := nestedRead("session-1", "turn-1", "", "agent-a", at(7*time.Second))

	section := crossreadSection(t, profileOutput(t, seed(t,
		startEvent("session-1", "startup", at(0)),
		identifiedLaunch("session-1", "turn-1", "call-1", "agent-a", at(time.Second)),
		readIn("session-1", "turn-1", "/repo/ranged.go", at(2*time.Second)),
		readIn("session-1", "turn-1", "/repo/failed.go", at(3*time.Second)),
		readIn("session-1", "turn-1", "/repo/unestablished.go", at(4*time.Second)),
		readIn("session-1", "turn-1", "", at(5*time.Second)),
		ranged, failed, unestablished, empty,
	)))

	assertContains(t, section, "No path was read in more than one related agent scope")
	for _, path := range []string{"ranged.go", "failed.go", "unestablished.go"} {
		if strings.Contains(section, path) {
			t.Errorf("a read that establishes no acquisition was counted as one (%s):\n%s", path, section)
		}
	}
}

// Selecting a session scopes this analysis with everything else, because the
// filter runs before the single pass that feeds it.
func TestCrossReadSectionFollowsSessionSelection(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		startEvent("session-1", "startup", at(0)),
		readIn("session-1", "turn-1", "/repo/a.go", at(time.Second)),
		identifiedLaunch("session-1", "turn-1", "call-1", "agent-a", at(2*time.Second)),
		nestedRead("session-1", "turn-1", "/repo/a.go", "agent-a", at(3*time.Second)),
		startEvent("session-2", "startup", at(4*time.Second)),
		readIn("session-2", "turn-2", "/repo/b.go", at(5*time.Second)),
		identifiedLaunch("session-2", "turn-2", "call-2", "agent-a", at(6*time.Second)),
		nestedRead("session-2", "turn-2", "/repo/b.go", "agent-a", at(7*time.Second)),
	)

	whole := crossreadSection(t, profileOutput(t, dir))
	assertContains(t, whole, "/repo/a.go", "/repo/b.go",
		"2 paths read in more than one related agent scope")

	scoped := crossreadSection(t, scopedProfileOutput(t, dir, profileOptions{session: "session-2"}))
	assertContains(t, scoped, "/repo/b.go", "1 path read in more than one related agent scope")
	if strings.Contains(scoped, "/repo/a.go") {
		t.Errorf("a scoped report reported another session's reading:\n%s", scoped)
	}
}

// One agent identity reused in another session relates nothing across the two:
// an identity is the agent's own and means nothing outside its session.
func TestCrossReadSectionNeverComparesSessions(t *testing.T) {
	t.Parallel()

	section := crossreadSection(t, profileOutput(t, seed(t,
		startEvent("session-1", "startup", at(0)),
		identifiedLaunch("session-1", "turn-1", "call-1", "agent-a", at(time.Second)),
		nestedRead("session-1", "turn-1", "/repo/a.go", "agent-a", at(2*time.Second)),
		startEvent("session-2", "startup", at(3*time.Second)),
		readIn("session-2", "turn-2", "/repo/a.go", at(4*time.Second)),
	)))

	assertContains(t, section, "No path was read in more than one related agent scope")
}

// The section is bounded, and what it leaves out is accounted for rather than
// dropped.
func TestCrossReadSectionAccountsForOmittedPaths(t *testing.T) {
	t.Parallel()

	events := []event.Event{
		startEvent("session-1", "startup", at(0)),
		identifiedLaunch("session-1", "turn-1", "call-1", "agent-a", at(time.Second)),
	}
	when := 2 * time.Second
	for i := range crossreadShown + 2 {
		path := pathAt(i)
		events = append(events,
			readIn("session-1", "turn-1", path, at(when)),
			nestedRead("session-1", "turn-1", path, "agent-a", at(when+time.Second)))
		when += 2 * time.Second
	}

	section := crossreadSection(t, seedAndProfile(t, events))

	assertContains(t, section,
		"and 2 more paths read in more than one related scope",
		"10 paths read in more than one related agent scope",
	)
	if strings.Contains(section, pathAt(9)) {
		t.Errorf("an omitted path was listed:\n%s", section)
	}
	rejectOverlongLines(t, seedAndProfile(t, events))
}

func pathAt(i int) string { return fmt.Sprintf("/repo/f%02d.go", i) }

// A path can be in more groups, and a group hold more scopes, than the page
// describes. Neither limit may hide that the rest are there.
func TestCrossReadSectionBoundsGroupsAndScopes(t *testing.T) {
	t.Parallel()

	t.Run("more groups than are described", func(t *testing.T) {
		t.Parallel()

		events := []event.Event{
			startEvent("session-1", "startup", at(0)),
			readIn("session-1", "turn-1", "/repo/a.go", at(time.Second)),
		}
		when := 2 * time.Second
		launcher := ""
		for i := range crossreadGroupsShown + 2 {
			agent := fmt.Sprintf("agent-%d", i)
			ev := identifiedLaunch("session-1", "turn-1", fmt.Sprintf("call-%d", i), agent, at(when))
			ev.SubagentID = launcher
			events = append(events, ev,
				nestedRead("session-1", "turn-1", "/repo/a.go", agent, at(when+time.Second)))
			launcher = agent
			when += 2 * time.Second
		}

		section := crossreadSection(t, seedAndProfile(t, events))

		assertContains(t, section,
			"the session scope and the agents it launched",
			"agent 2 and the agents it launched",
			"and 2 more groups of related scopes read it",
		)
		if strings.Contains(section, "agent 3 and the agents it launched") {
			t.Errorf("more groups were described than the section bounds:\n%s", section)
		}
	})

	t.Run("more scopes than are named", func(t *testing.T) {
		t.Parallel()

		events := []event.Event{
			startEvent("session-1", "startup", at(0)),
			readIn("session-1", "turn-1", "/repo/a.go", at(time.Second)),
		}
		when := 2 * time.Second
		for i := range crossreadScopesShown + 1 {
			agent := fmt.Sprintf("agent-%d", i)
			events = append(events,
				identifiedLaunch("session-1", "turn-1", fmt.Sprintf("call-%d", i), agent, at(when)),
				nestedRead("session-1", "turn-1", "/repo/a.go", agent, at(when+time.Second)))
			when += 2 * time.Second
		}

		section := crossreadSection(t, seedAndProfile(t, events))

		assertContains(t, section, "the session scope, 1 read", "agent 5, 1 read",
			"and 2 more scopes in this group")
		if strings.Contains(section, "agent 6, 1 read") {
			t.Errorf("more scopes were named than the section bounds:\n%s", section)
		}
	})
}
