package cli

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/event"
)

// turnsSection returns the part of a report between the turns heading and the
// section after it, so an assertion about a turn cannot be satisfied elsewhere.
func turnsSection(t *testing.T, out string) string {
	t.Helper()

	_, below, ok := strings.Cut(out, "\nRecorded turns\n")
	if !ok {
		t.Fatalf("the report has no turns section:\n%s", out)
	}
	above, _, ok := strings.Cut(below, "\nRead again in a later context epoch\n")
	if !ok {
		t.Fatalf("the turns section does not end:\n%s", out)
	}
	return above
}

// turnCall is one tool call the agent identified, in a named turn.
func turnCall(session, turn string, when time.Time, m *event.ToolMetadata) event.Event {
	return event.Event{
		SchemaVersion: event.SchemaVersion,
		Agent:         "claude-code",
		Type:          event.TypeToolCall,
		Timestamp:     when,
		SessionID:     session,
		TurnID:        turn,
		Tool: &event.ToolCall{
			Name:       "Read",
			Outcome:    event.OutcomeSuccess,
			DurationMS: ms(2),
			Metadata:   m,
		},
	}
}

func readIn(session, turn, path string, when time.Time) event.Event {
	return turnCall(session, turn, when, &event.ToolMetadata{
		File: &event.FileOp{Path: path, Access: event.AccessRead},
	})
}

func shellIn(session, turn, digest string, when time.Time) event.Event {
	ev := shellEvent(session, digest, when, 300)
	ev.TurnID = turn
	return ev
}

// A turn reports the work that named it and what was observed under the same
// identity, and nothing that would read as the price of that work.
func TestProfileReportsRecordedTurns(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		startEvent("session-1", "startup", at(0)),
		readIn("session-1", "turn-1", "/repo/a.go", at(time.Second)),
		readIn("session-1", "turn-1", "/repo/b.go", at(2*time.Second)),
		shellIn("session-1", "turn-1", "digest-1", at(3*time.Second)),
	)
	seedUsage(t, dir,
		request("session-1", "turn-1", firstRequestTokens, micros(213915)),
		request("session-1", "turn-1", secondRequestTokens, micros(74085)),
	)

	out := turnsSection(t, profileOutput(t, dir))

	for _, want := range []string{
		"session-1",
		"turn 1  ·  2026-08-10 20:25:05 → 20:25:07 UTC",
		"Context epoch                 1",
		"Tool calls                    3",
		"Whole-file reads              2",
		"Shell                         1",
		"Observed model consumption",
		"Model requests              2",
		"Model cost                  $0.2880",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the turn is missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(flat(out), "not the cost of the tool calls above it") {
		t.Errorf("the consumption reads as the cost of the work:\n%s", out)
	}
}

// A category with nothing recorded is left out, so a small turn stays small
// rather than becoming a block of zeros.
func TestProfileOmitsEmptyTurnCategories(t *testing.T) {
	t.Parallel()

	out := turnsSection(t, profileOutput(t, seed(t,
		startEvent("session-1", "startup", at(0)),
		readIn("session-1", "turn-1", "/repo/a.go", at(time.Second)),
	)))

	for _, absent := range []string{
		"Ranged reads", "Searches", "Shell", "Writes", "Edits",
		"Subagent launches", "Calls by a nested agent", "Uninterpreted",
	} {
		if strings.Contains(out, absent) {
			t.Errorf("a turn that recorded none printed %q:\n%s", absent, out)
		}
	}
}

// Missing consumption is not zero, and it is not silence either: a reader has
// to be able to tell a turn Axiom saw consuming nothing from one it did not
// see at all.
func TestProfileReportsTurnsWithoutObservedConsumption(t *testing.T) {
	t.Parallel()

	out := turnsSection(t, profileOutput(t, seed(t,
		startEvent("session-1", "startup", at(0)),
		readIn("session-1", "turn-1", "/repo/a.go", at(time.Second)),
	)))

	if !strings.Contains(out, "Observed model consumption    not recorded") {
		t.Errorf("a turn with nothing observed does not say so:\n%s", out)
	}
	for _, absent := range []string{"Model requests", "Input tokens", "Model cost"} {
		if strings.Contains(out, absent) {
			t.Errorf("an empty consumption block was rendered with %q:\n%s", absent, out)
		}
	}
}

// A withheld dimension is left out rather than printed as zero, exactly as it
// is against a finding.
func TestProfileWithholdsPartialTurnConsumption(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		startEvent("session-1", "startup", at(0)),
		readIn("session-1", "turn-1", "/repo/a.go", at(time.Second)),
	)
	seedUsage(t, dir,
		request("session-1", "turn-1", firstRequestTokens, micros(213915)),
		request("session-1", "turn-1", nil, micros(74085)),
	)

	out := turnsSection(t, profileOutput(t, dir))

	if !strings.Contains(out, "Model requests              2") {
		t.Errorf("the observed requests are missing:\n%s", out)
	}
	if strings.Contains(out, "Input tokens") {
		t.Errorf("a partial token total was reported:\n%s", out)
	}
	if !strings.Contains(out, "Model cost                  $0.2880") {
		t.Errorf("the cost was withheld with the tokens:\n%s", out)
	}
}

// Consumption is recorded under turn identifiers no tool call named. It is
// neither dropped nor folded into a turn that did record work.
func TestProfileAccountsForConsumptionOutsideRecordedTurns(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		startEvent("session-1", "startup", at(0)),
		readIn("session-1", "turn-1", "/repo/a.go", at(time.Second)),
	)
	seedUsage(t, dir,
		request("session-1", "turn-1", firstRequestTokens, micros(213915)),
		request("session-1", "turn-2", secondRequestTokens, micros(74085)),
		request("session-1", "turn-3", secondRequestTokens, micros(74085)),
	)

	out := turnsSection(t, profileOutput(t, dir))

	if !strings.Contains(flat(out), "2 model requests observed under 2 turn identifiers that no recorded tool call named") {
		t.Errorf("consumption outside the recorded turns is not accounted for:\n%s", out)
	}
	if strings.Contains(out, "turn 2") {
		t.Errorf("a turn was created from usage alone:\n%s", out)
	}
	if !strings.Contains(out, "Model requests              1") {
		t.Errorf("the outside requests were folded into the recorded turn:\n%s", out)
	}
}

// A turn identifier on a session start or end named no recorded work.
func TestProfileDoesNotRecordLifecycleOnlyTurns(t *testing.T) {
	t.Parallel()

	start := startEvent("session-1", "startup", at(0))
	start.TurnID = "turn-start"
	end := endEvent("session-1", "clear", at(3*time.Second))
	end.TurnID = "turn-end"

	out := turnsSection(t, profileOutput(t, seed(t,
		start,
		readIn("session-1", "turn-1", "/repo/a.go", at(time.Second)),
		end,
	)))

	if got := strings.Count(out, "    turn "); got != 1 {
		t.Errorf("%d turns were listed, want only the one that recorded work:\n%s", got, out)
	}
}

// A call that named no turn is counted rather than assigned to a neighbour.
func TestProfileAccountsForCallsOutsideTurns(t *testing.T) {
	t.Parallel()

	out := turnsSection(t, profileOutput(t, seed(t,
		startEvent("session-1", "startup", at(0)),
		readIn("session-1", "turn-1", "/repo/a.go", at(time.Second)),
		readEvent("session-1", "/repo/b.go", at(2*time.Second), 2),
	)))

	if !strings.Contains(flat(out), "1 recorded tool call named no turn") {
		t.Errorf("the call that named no turn is unaccounted for:\n%s", out)
	}
	if !strings.Contains(out, "Tool calls                    1") {
		t.Errorf("the call was folded into the turn beside it:\n%s", out)
	}
}

// Compaction opens a context in the middle of a turn. Both epochs are named,
// and one epoch is still rendered as one.
func TestProfileReportsTurnsSpanningEpochs(t *testing.T) {
	t.Parallel()

	out := turnsSection(t, profileOutput(t, seed(t,
		startEvent("session-1", "startup", at(0)),
		readIn("session-1", "turn-1", "/repo/a.go", at(time.Second)),
		startEvent("session-1", "compact", at(2*time.Second)),
		readIn("session-1", "turn-1", "/repo/b.go", at(3*time.Second)),
		readIn("session-1", "turn-2", "/repo/c.go", at(4*time.Second)),
	)))

	if !strings.Contains(out, "Context epochs                1, 2") {
		t.Errorf("the turn was forced into one epoch:\n%s", out)
	}
	if !strings.Contains(out, "Context epoch                 2") {
		t.Errorf("a turn in one epoch is not rendered compactly:\n%s", out)
	}
}

// A log of many turns is bounded, and what is left out is accounted for so the
// limit hides no work.
func TestProfileBoundsTheTurnsItShows(t *testing.T) {
	t.Parallel()

	events := []event.Event{startEvent("session-1", "startup", at(0))}
	for i := range turnsShown + 3 {
		events = append(events, readIn("session-1",
			fmt.Sprintf("turn-%02d", i), "/repo/a.go", at(time.Duration(i+1)*time.Second)))
	}

	out := turnsSection(t, profileOutput(t, seed(t, events...)))

	if got := strings.Count(out, "    turn "); got != turnsShown {
		t.Errorf("%d turns were listed, want %d:\n%s", got, turnsShown, out)
	}
	if !strings.Contains(out, "3 earlier turns omitted (3 tool calls)") {
		t.Errorf("the omitted turns are unaccounted for:\n%s", out)
	}
	// The most recently recorded are kept, as everywhere else in the report.
	if !strings.Contains(out, fmt.Sprintf("turn %d", turnsShown+3)) {
		t.Errorf("the latest turn was omitted:\n%s", out)
	}
	rejectOverlongLines(t, out)
}

// A turn that straddled many context resets names them without running the
// line past the width the report is written to.
func TestEpochOrdinalsAreBounded(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		epochs []int
		want   string
	}{
		"one":   {[]int{1}, "1"},
		"a few": {[]int{1, 2, 3}, "1, 2, 3"},
		"every one that fits": {
			[]int{1, 2, 3, 4, 5}, "1, 2, 3, 4, 5",
		},
		"the rest counted": {
			[]int{1, 2, 3, 4, 5, 6, 7, 8}, "1, 2, 3, 4, 5 and 3 more",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := epochsOf(c.epochs); got != c.want {
				t.Errorf("epochsOf(%v) = %q, want %q", c.epochs, got, c.want)
			}
		})
	}
}

// A log where no tool call named a turn says so, rather than showing an empty
// section that reads as no work.
func TestProfileWithoutRecordedTurns(t *testing.T) {
	t.Parallel()

	out := turnsSection(t, profileOutput(t, seed(t,
		startEvent("session-1", "startup", at(0)),
		readEvent("session-1", "/repo/a.go", at(time.Second), 2),
	)))

	if !strings.Contains(out, "No recorded tool call named a turn") {
		t.Errorf("the empty state is not explained:\n%s", out)
	}
}

// The nested-agent limitation is stated wherever consumption is, because a
// turn's total is read as everything it caused unless something says otherwise.
func TestProfileStatesTheNestedAgentLimitation(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		startEvent("session-1", "startup", at(0)),
		readIn("session-1", "turn-1", "/repo/a.go", at(time.Second)),
	)
	seedUsage(t, dir, request("session-1", "turn-1", firstRequestTokens, micros(213915)))

	out := flat(turnsSection(t, profileOutput(t, dir)))

	if !strings.Contains(out, "does not contain everything a subagent it launched spent") {
		t.Errorf("the nested-agent limitation is missing:\n%s", out)
	}
}

// launchIn is a call the adapter recognized as handing work to a nested agent,
// carrying the outcome the record established for it.
func launchIn(session, turn string, when time.Time, outcome event.Outcome) event.Event {
	ev := subagentCall(session, turn, when)
	ev.Tool.Outcome = outcome
	return ev
}

// A nested agent's calls carry the turn that launched them, and are counted
// both as the turn's work and as nested. The launch that declared the work and
// the work itself are reported as two separate quantities.
func TestProfileCountsLaunchesAndNestedCallsApart(t *testing.T) {
	t.Parallel()

	nested := readIn("session-1", "turn-1", "/repo/b.go", at(2*time.Second))
	nested.SubagentID = "agent-1"

	out := turnsSection(t, profileOutput(t, seed(t,
		startEvent("session-1", "startup", at(0)),
		launchIn("session-1", "turn-1", at(time.Second), event.OutcomeSuccess),
		nested,
	)))

	for _, want := range []string{
		"Tool calls                    2",
		"Subagent launches             1",
		"Calls by a nested agent       1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the turn is missing %q:\n%s", want, out)
		}
	}
	// The launch was classified, so it is no longer reported as a call this
	// version cannot describe. That contradiction between two sections of one
	// report is the defect this feature exists to remove.
	if strings.Contains(out, "Uninterpreted") {
		t.Errorf("a recognized launch was reported as uninterpreted:\n%s", out)
	}
}

// The report must not let a reader take the two subagent numbers for one
// quantity, or read either as evidence for the other. A launch that recorded
// no returned identity relates to nothing, whatever was recorded beside it.
func TestProfileDoesNotRelateLaunchesToNestedWork(t *testing.T) {
	t.Parallel()

	nested := readIn("session-1", "turn-1", "/repo/b.go", at(2*time.Second))
	nested.SubagentID = "agent-1"

	out := flat(turnsSection(t, profileOutput(t, seed(t,
		startEvent("session-1", "startup", at(0)),
		launchIn("session-1", "turn-1", at(time.Second), event.OutcomeSuccess),
		nested,
	))))

	for _, want := range []string{
		"Neither is derived from the other",
		"adding them together counts nothing meaningful",
		"nested calls appear with no launch beside them",
		"1 launch with no returned agent identity recorded",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the section does not hold the two counts apart, missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "subagent launch 1") {
		t.Errorf("a launch with no recorded identity was related to a call:\n%s", out)
	}
}

// A turn that recorded neither says nothing about either, rather than
// explaining a distinction that is not on the page.
func TestProfileOmitsTheDelegationCaveatWithoutDelegation(t *testing.T) {
	t.Parallel()

	out := flat(turnsSection(t, profileOutput(t, seed(t,
		startEvent("session-1", "startup", at(0)),
		readIn("session-1", "turn-1", "/repo/a.go", at(time.Second)),
	))))

	if strings.Contains(out, "Subagent launches counts calls") {
		t.Errorf("a turn with no delegation explained one:\n%s", out)
	}
}

// A launch call that failed started no nested agent, and one whose outcome was
// never recorded settles nothing. Both are reported, and neither is reported as
// a launch that happened.
func TestProfileDistinguishesLaunchOutcomes(t *testing.T) {
	t.Parallel()

	out := turnsSection(t, profileOutput(t, seed(t,
		startEvent("session-1", "startup", at(0)),
		launchIn("session-1", "turn-1", at(time.Second), event.OutcomeSuccess),
		launchIn("session-1", "turn-1", at(2*time.Second), event.OutcomeFailure),
		launchIn("session-1", "turn-1", at(3*time.Second), event.Outcome("")),
	)))

	want := "Subagent launches             3  (1 reported failing, 1 with no outcome recorded)"
	if !strings.Contains(out, want) {
		t.Errorf("the launch outcomes are not held apart, want %q:\n%s", want, out)
	}
	// The width of the report is not asserted here. A turn qualifying both
	// states on one line runs past it, which Writes and Edits have done since
	// they gained outcomes: the length comes from the shared rendering and the
	// label contributes none of it. Narrowing it is a change to that
	// convention everywhere it is used, and not part of this one.
}

// A record written with no launch metadata says nothing about having been a
// launch, whatever the tool was called. It stays where it has always been.
func TestProfileLeavesUnrecordedLaunchMetadataUninterpreted(t *testing.T) {
	t.Parallel()

	out := turnsSection(t, profileOutput(t, seed(t,
		startEvent("session-1", "startup", at(0)),
		opaqueCall("session-1", "turn-1", "Agent", at(time.Second)),
	)))

	if !strings.Contains(out, "Uninterpreted                 1") {
		t.Errorf("a record with no launch metadata was not counted as uninterpreted:\n%s", out)
	}
	if strings.Contains(out, "Subagent launches") {
		t.Errorf("a record with no launch metadata was read as a launch:\n%s", out)
	}
}

// The empirical capture this feature was built from: turns of work in one
// session and one epoch, with model requests recorded under further identities
// that no tool call named.
//
// The launch carries the metadata the adapter derives for one. ADR 0013 read
// this capture as showing that Claude Code supplied none, and the raw-payload
// investigation behind ADR 0014 falsified that: the payload carried
// subagent_type and the adapter recorded it. The nested call is recorded before
// the launch that produced it, as the capture had it, because a launch reaches
// a hook only once its call has returned.
func TestProfileReplaysTheValidationCapture(t *testing.T) {
	t.Parallel()

	const session = "5eff7948-a49b-459b-9884-e1bc6e47d627"
	nested := readIn(session, "turn-3", "/proj/main.go", at(20*time.Second))
	nested.SubagentID = "agent-1"
	spawn := launchIn(session, "turn-3", at(21*time.Second), event.OutcomeSuccess)

	dir := seed(t,
		startEvent(session, "startup", at(0)),
		readIn(session, "turn-1", "/proj/main.go", at(time.Second)),
		readIn(session, "turn-1", "/proj/README.md", at(2*time.Second)),
		shellIn(session, "turn-1", "digest-1", at(3*time.Second)),
		shellIn(session, "turn-2", "digest-2", at(10*time.Second)),
		nested,
		spawn,
		endEvent(session, "clear", at(30*time.Second)),
	)
	seedUsage(t, dir,
		request(session, "turn-1", firstRequestTokens, micros(201600)),
		request(session, "turn-2", secondRequestTokens, micros(43600)),
		request(session, "turn-3", secondRequestTokens, micros(254800)),
		// Recorded under identities no tool call named, as the capture did.
		request(session, "turn-x", secondRequestTokens, micros(1000)),
		request(session, "turn-y", secondRequestTokens, micros(1000)),
	)

	out := turnsSection(t, profileOutput(t, dir))

	if got := strings.Count(out, "    turn "); got != 3 {
		t.Errorf("%d turns were listed, want the 3 that recorded work:\n%s", got, out)
	}
	if !strings.Contains(out, "Calls by a nested agent       1") {
		t.Errorf("the nested call is missing:\n%s", out)
	}
	if !strings.Contains(out, "Subagent launches             1") {
		t.Errorf("the launch is not accounted for:\n%s", out)
	}
	if strings.Contains(out, "Uninterpreted") {
		t.Errorf("the launch was counted as a call Axiom cannot describe:\n%s", out)
	}
	if !strings.Contains(flat(out), "2 model requests observed under 2 turn identifiers") {
		t.Errorf("the consumption outside the recorded turns is missing:\n%s", out)
	}
	rejectOverlongLines(t, out)
}
