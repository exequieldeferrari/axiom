package cli

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/event"
)

func fileEvent(session, turn, path, access string, outcome event.Outcome, when time.Time) event.Event {
	return event.Event{
		SchemaVersion: event.SchemaVersion,
		Agent:         "claude-code",
		Type:          event.TypeToolCall,
		Timestamp:     when,
		SessionID:     session,
		TurnID:        turn,
		Tool: &event.ToolCall{
			Name:       "Edit",
			Outcome:    outcome,
			DurationMS: ms(3),
			Metadata:   &event.ToolMetadata{File: &event.FileOp{Path: path, Access: access}},
		},
	}
}

func editInTurn(session, turn, path string, when time.Time) event.Event {
	return fileEvent(session, turn, path, event.AccessEdit, event.OutcomeSuccess, when)
}

func subagentReadInTurn(session, turn, agent, path string, when time.Time) event.Event {
	ev := readInTurn(session, turn, path, when)
	ev.SubagentID = agent
	return ev
}

// reacquiredSection returns just the section, so a test cannot pass on wording
// that happens to appear somewhere else in the report.
func reacquiredSection(t *testing.T, out string) string {
	t.Helper()

	_, after, ok := strings.Cut(out, "\nRead again in a later context epoch\n")
	if !ok {
		t.Fatalf("the report has no re-acquisition section:\n%s", out)
	}
	section, _, ok := strings.Cut(after, "\nObserved operations\n")
	if !ok {
		t.Fatalf("the re-acquisition section does not end:\n%s", out)
	}
	return section
}

func assertContains(t *testing.T, section string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(section, w) {
			t.Errorf("section is missing %q:\n%s", w, section)
		}
	}
}

// assertSentence matches an explanation whose line breaks depend on the counts
// a log happened to produce, so a test can state the sentence rather than the
// wrapping.
func assertSentence(t *testing.T, section string, want ...string) {
	t.Helper()

	flat := strings.Join(strings.Fields(section), " ")
	for _, w := range want {
		if !strings.Contains(flat, strings.Join(strings.Fields(w), " ")) {
			t.Errorf("section is missing the sentence %q:\n%s", w, section)
		}
	}
}

// This is the sequence Claude Code 2.1.228 was observed producing in the
// controlled capture for this milestone: one identity resumed repeatedly, with
// one path read on either side of a boundary and an edit of it in the epoch in
// between, and another path read and edited inside two later epochs.
func TestProfileReportsReadsAcrossEpochs(t *testing.T) {
	t.Parallel()

	const (
		notes = "/private/tmp/proj/notes.txt"
		other = "/private/tmp/proj/other.txt"
	)
	dir := seed(t,
		startEvent("session-1", "startup", at(0)),
		readInTurn("session-1", "turn-1", notes, at(time.Second)),
		endEvent("session-1", "other", at(2*time.Second)),
		startEvent("session-1", "resume", at(3*time.Second)),
		editInTurn("session-1", "turn-2", notes, at(4*time.Second)),
		endEvent("session-1", "other", at(5*time.Second)),
		startEvent("session-1", "resume", at(6*time.Second)),
		readInTurn("session-1", "turn-3", notes, at(7*time.Second)),
		endEvent("session-1", "other", at(8*time.Second)),
		startEvent("session-1", "resume", at(9*time.Second)),
		readInTurn("session-1", "turn-4", other, at(10*time.Second)),
		editInTurn("session-1", "turn-4", other, at(11*time.Second)),
	)

	section := reacquiredSection(t, profileOutput(t, dir))

	assertContains(t, section,
		notes,
		"session session-1",
		// The edit of notes.txt fell in epoch 2, which read nothing, so it is
		// after no acquisition and the two acquisitions say so.
		"epoch 1, opened by startup, 1 read, no later write or edit recorded",
		"epoch 3, opened by resume, 1 read, no later write or edit recorded",
	)
	// other.txt was read only in epoch 4, so it crossed no boundary.
	if strings.Contains(section, other) {
		t.Errorf("a path read in one epoch was reported:\n%s", section)
	}
	assertContains(t, section, "1 path read in more than one context epoch.")
	rejectOverlongLines(t, profileOutput(t, dir))
}

// The section must say what it means by an ordering, and must refuse both
// inferences a reader would otherwise supply.
func TestReacquiredSectionStatesItsLimits(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		startEvent("session-1", "startup", at(0)),
		readInTurn("session-1", "turn-1", "/repo/a.go", at(time.Second)),
		startEvent("session-1", "compact", at(2*time.Second)),
		readInTurn("session-1", "turn-2", "/repo/a.go", at(3*time.Second)),
		editInTurn("session-1", "turn-2", "/repo/a.go", at(4*time.Second)),
	)

	section := reacquiredSection(t, profileOutput(t, dir))

	assertContains(t, section,
		"epoch 2, opened by compact, 1 read, later write or edit recorded",
		"not proof that the agent's context was",
		"discarded",
		"lower bound",
		"It is an ordering of two recorded operations.",
		"not evidence the read achieved nothing",
		"Session identities are never compared.",
	)
}

// The wording is the product here. Every one of these would state something the
// evidence does not establish, and several of them were the reason an earlier
// version of this analysis was rejected.
func TestReacquiredSectionAvoidsUnsupportedWording(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		startEvent("session-1", "startup", at(0)),
		readInTurn("session-1", "turn-1", "/repo/a.go", at(time.Second)),
		startEvent("session-1", "compact", at(2*time.Second)),
		readInTurn("session-1", "turn-2", "/repo/a.go", at(3*time.Second)),
		editInTurn("session-1", "turn-2", "/repo/a.go", at(4*time.Second)),
		subagentReadInTurn("session-1", "turn-2", "nested", "/repo/b.go", at(5*time.Second)),
	)

	section := strings.ToLower(reacquiredSection(t, profileOutput(t, dir)))

	for _, reject := range []string{
		"waste", "unnecessary", "redundant", "forget", "memory loss",
		"context loss", "rediscover", "required", "unavoidable",
		"explained", "unexplained", "caused by", "cost of", "efficiency",
		"lost", "confus", "should", "recommend", "savings",
		// A write or edit was recorded. Whether it left the file different is
		// not observable, and a call the agent reported failing is recorded
		// here too, so the section never says a file was modified.
		"modif",
	} {
		if strings.Contains(section, reject) {
			t.Errorf("the section claims more than the evidence supports with %q:\n%s", reject, section)
		}
	}
}

// A nested agent's reading is not part of this, and must not disappear either.
func TestReacquiredSectionAccountsForSubagentReads(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		startEvent("session-1", "startup", at(0)),
		subagentReadInTurn("session-1", "turn-1", "nested", "/repo/a.go", at(time.Second)),
		startEvent("session-1", "compact", at(2*time.Second)),
		subagentReadInTurn("session-1", "turn-2", "nested", "/repo/a.go", at(3*time.Second)),
	)

	section := reacquiredSection(t, profileOutput(t, dir))

	assertContains(t, section, "No path was read in more than one context epoch")
	assertSentence(t, section,
		"A nested agent reasons in a context of its own, and these epochs are the session's, so its reads are not part of this. 2 successful whole-file reads were set aside.")
}

// The three empty states mean different things and have to read differently.
func TestReacquiredSectionEmptyStates(t *testing.T) {
	t.Parallel()

	t.Run("no boundary in the log", func(t *testing.T) {
		t.Parallel()
		dir := seed(t,
			startEvent("session-1", "startup", at(0)),
			readInTurn("session-1", "turn-1", "/repo/a.go", at(time.Second)),
			readInTurn("session-1", "turn-1", "/repo/a.go", at(2*time.Second)),
		)
		section := reacquiredSection(t, profileOutput(t, dir))
		assertContains(t, section, "No session identity recorded more than one context epoch")
		if strings.Contains(section, "No path was read in more than one") {
			t.Errorf("a log with no boundary was described as one with nothing read across a boundary:\n%s", section)
		}
	})

	t.Run("boundaries with nothing read across them", func(t *testing.T) {
		t.Parallel()
		dir := seed(t,
			startEvent("session-1", "startup", at(0)),
			readInTurn("session-1", "turn-1", "/repo/a.go", at(time.Second)),
			startEvent("session-1", "compact", at(2*time.Second)),
			readInTurn("session-1", "turn-2", "/repo/b.go", at(3*time.Second)),
		)
		section := reacquiredSection(t, profileOutput(t, dir))
		assertContains(t, section, "No path was read in more than one context epoch", "lower bound")
	})

	// Records the timeline could place nowhere establish no epoch, so the log
	// has no boundary rather than a boundary nothing crossed.
	t.Run("nothing the timeline could place", func(t *testing.T) {
		t.Parallel()
		unidentified := readInTurn("", "turn-1", "/repo/a.go", at(time.Second))
		dir := seed(t, unidentified, endEvent("session-1", "other", at(2*time.Second)))
		section := reacquiredSection(t, profileOutput(t, dir))
		assertContains(t, section, "No session identity recorded more than one context epoch")
	})
}

// Selecting a session scopes this analysis with everything else, because the
// filter runs before the single pass that feeds it.
func TestReacquiredSectionFollowsSessionSelection(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		startEvent("session-1", "startup", at(0)),
		readInTurn("session-1", "turn-1", "/repo/a.go", at(time.Second)),
		startEvent("session-1", "compact", at(2*time.Second)),
		readInTurn("session-1", "turn-2", "/repo/a.go", at(3*time.Second)),
		startEvent("session-2", "startup", at(4*time.Second)),
		readInTurn("session-2", "turn-3", "/repo/b.go", at(5*time.Second)),
		startEvent("session-2", "compact", at(6*time.Second)),
		readInTurn("session-2", "turn-4", "/repo/b.go", at(7*time.Second)),
	)

	whole := reacquiredSection(t, profileOutput(t, dir))
	assertContains(t, whole, "/repo/a.go", "/repo/b.go", "2 paths read in more than one context epoch.")

	scoped := reacquiredSection(t, scopedProfileOutput(t, dir, profileOptions{session: "session-2"}))
	assertContains(t, scoped, "/repo/b.go", "1 path read in more than one context epoch.")
	if strings.Contains(scoped, "/repo/a.go") {
		t.Errorf("a scoped report reported another session's reading:\n%s", scoped)
	}
}

// The seam. A path read on either side of a recorded context reset produces no
// repeated-read finding, because the profiler stops comparing at the boundary,
// and exactly one relation here, because that is the observation the profiler
// declines to make. The two analyses answer different questions on purpose, and
// this is where that is asserted rather than described.
func TestContextResetSeparatesFindingsFromReacquisition(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		startEvent("session-1", "startup", at(0)),
		readInTurn("session-1", "turn-1", "/repo/a.go", at(time.Second)),
		startEvent("session-1", "compact", at(2*time.Second)),
		readInTurn("session-1", "turn-2", "/repo/a.go", at(3*time.Second)),
	)

	out := profileOutput(t, dir)

	if !strings.Contains(out, "session session-1  ·  2 epochs") {
		t.Errorf("the reset was not reported as a boundary:\n%s", out)
	}
	// The profiler saw the repetition and refused it: the context was reset in
	// between, so it cannot rule out that the second read was worth making.
	if !strings.Contains(out, "No high-confidence redundant work") {
		t.Errorf("a repeated read across a context reset became a finding:\n%s", out)
	}
	// This analysis reports the same two reads as one relation, and says only
	// where they happened.
	section := reacquiredSection(t, out)
	assertContains(t, section,
		"/repo/a.go",
		"epoch 1, opened by startup, 1 read",
		"epoch 2, opened by compact, 1 read",
		"1 path read in more than one context epoch.",
	)

	// Two reads inside one epoch are the opposite case: a finding, and no
	// relation, from the same pass over the same kind of records.
	within := seed(t,
		startEvent("session-2", "startup", at(0)),
		readInTurn("session-2", "turn-1", "/repo/a.go", at(time.Second)),
		readInTurn("session-2", "turn-1", "/repo/a.go", at(2*time.Second)),
	)
	out = profileOutput(t, within)

	if !strings.Contains(out, "Repeated file read") {
		t.Errorf("a repeated read inside one epoch was not a finding:\n%s", out)
	}
	assertContains(t, reacquiredSection(t, out),
		"No session identity recorded more than one context epoch")
}

// The three states of "how the epoch opened" reach this section unchanged, and
// a source no version of Axiom has seen is printed as itself.
func TestReacquiredSectionCarriesEveryOpening(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		// Work with no start before it: the log began after the session did.
		readInTurn("session-1", "turn-1", "/repo/a.go", at(0)),
		startEvent("session-1", "", at(time.Second)),
		readInTurn("session-1", "turn-2", "/repo/a.go", at(2*time.Second)),
		startEvent("session-1", "teleport", at(3*time.Second)),
		readInTurn("session-1", "turn-3", "/repo/a.go", at(4*time.Second)),
	)

	assertContains(t, reacquiredSection(t, profileOutput(t, dir)),
		"epoch 1, no start recorded, 1 read",
		"epoch 2, opened by a start with no source, 1 read",
		"epoch 3, opened by teleport, 1 read",
	)
}

// The section is bounded, and what it leaves out is accounted for rather than
// dropped.
func TestReacquiredSectionAccountsForOmittedPaths(t *testing.T) {
	t.Parallel()

	events := []event.Event{startEvent("session-1", "startup", at(0))}
	for i := range reacquiredShown + 2 {
		events = append(events, readInTurn("session-1", "turn-1",
			fmt.Sprintf("/repo/f%02d.go", i), at(time.Duration(i+1)*time.Second)))
	}
	events = append(events, startEvent("session-1", "compact", at(time.Minute)))
	for i := range reacquiredShown + 2 {
		events = append(events, readInTurn("session-1", "turn-2",
			fmt.Sprintf("/repo/f%02d.go", i), at(time.Minute+time.Duration(i+1)*time.Second)))
	}

	section := reacquiredSection(t, seedAndProfile(t, events))

	assertContains(t, section,
		"/repo/f00.go",
		"and 2 more paths read in more than one epoch",
		"10 paths read in more than one context epoch.",
	)
	// The ninth and tenth by ordering are omitted from the list, not from the
	// count above.
	if strings.Contains(section, "/repo/f09.go\n") {
		t.Errorf("an omitted path was listed:\n%s", section)
	}
}

func seedAndProfile(t *testing.T, events []event.Event) string {
	t.Helper()
	return profileOutput(t, seed(t, events...))
}

// An operation whose outcome the record never established is not known to have
// run, so it is neither a recorded write or edit after the read nor evidence
// that none followed. The line holds it apart from both, and in particular
// never says nothing was recorded and then counts something that was.
func TestReacquiredSectionKeepsUnestablishedOutcomesApart(t *testing.T) {
	t.Parallel()

	base := []event.Event{
		startEvent("session-1", "startup", at(0)),
		readInTurn("session-1", "turn-1", "/repo/a.go", at(time.Second)),
		startEvent("session-1", "compact", at(2*time.Second)),
		readInTurn("session-1", "turn-2", "/repo/a.go", at(3*time.Second)),
		fileEvent("session-1", "turn-2", "/repo/a.go", event.AccessEdit, event.Outcome(""), at(4*time.Second)),
	}

	t.Run("on its own", func(t *testing.T) {
		t.Parallel()
		section := reacquiredSection(t, seedAndProfile(t, base))
		assertContains(t, section,
			"1 read, 1 write or edit afterwards with no outcome recorded")
	})

	t.Run("alongside an established one", func(t *testing.T) {
		t.Parallel()
		section := reacquiredSection(t, seedAndProfile(t, append(slices.Clone(base),
			editInTurn("session-1", "turn-2", "/repo/a.go", at(5*time.Second)))))
		assertContains(t, section,
			"1 read, later write or edit recorded, 1 more with no outcome recorded")
	})
}
