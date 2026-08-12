package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/event"
)

func startEvent(session, source string, when time.Time) event.Event {
	return event.Event{
		SchemaVersion: event.SchemaVersion,
		Agent:         "claude-code",
		Type:          event.TypeSessionStart,
		Timestamp:     when,
		SessionID:     session,
		Session:       &event.Session{Source: source},
	}
}

func endEvent(session, reason string, when time.Time) event.Event {
	return event.Event{
		SchemaVersion: event.SchemaVersion,
		Agent:         "claude-code",
		Type:          event.TypeSessionEnd,
		Timestamp:     when,
		SessionID:     session,
		Session:       &event.Session{Reason: reason},
	}
}

func readInTurn(session, turn, path string, when time.Time) event.Event {
	ev := readEvent(session, path, when, 4)
	ev.TurnID = turn
	return ev
}

// This is the sequence Claude Code was observed producing for a session that
// compacted and was then cleared: one identity holding two epochs, and a second
// identity for the work after the clear.
func TestProfileReportsContextEpochs(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		startEvent("session-1", "startup", at(0)),
		readInTurn("session-1", "turn-1", "/repo/a.go", at(time.Second)),
		startEvent("session-1", "compact", at(2*time.Second)),
		readInTurn("session-1", "turn-2", "/repo/b.go", at(3*time.Second)),
		endEvent("session-1", "clear", at(4*time.Second)),
		startEvent("session-2", "clear", at(5*time.Second)),
		readInTurn("session-2", "turn-3", "/repo/c.go", at(6*time.Second)),
	)

	out := profileOutput(t, dir)

	for _, want := range []string{
		"Context epochs",
		"session session-1  ·  2 epochs",
		"first recorded 2026-08-10 20:25:04 UTC",
		"1  opened by startup, ended by a context reset",
		"2  opened by compact, ended with the session (clear)",
		"session session-2  ·  1 epoch",
		"1  opened by clear, nothing recorded after it",
		"1 tool call, 1 turn with work",
		"A turn can span a context reset",
		"It is not a claim that the agent is still running.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	// Two identities appearing in sequence say nothing about continuity, and
	// the report must not suggest it.
	for _, reject := range []string{"continued", "resumed from", "same sitting", "rediscover"} {
		if strings.Contains(out, reject) {
			t.Errorf("output links two session identities with %q:\n%s", reject, out)
		}
	}
	rejectOverlongLines(t, out)
}

// The profiler compares repetition only within one context epoch. This is the
// seam: the boundary the report draws has to be the same boundary the findings
// were scoped by, or the report explains the wrong thing.
func TestContextResetEndsFindingsAndTheEpochTogether(t *testing.T) {
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
	// The same file was read twice, but the context was discarded in between.
	if !strings.Contains(out, "No high-confidence redundant work") {
		t.Errorf("a repeated read across a context reset was reported as a finding:\n%s", out)
	}
}

// An epoch with no work in it is still a reset that happened. This was observed
// in the wild: a session resumed, compacted before doing anything, then worked.
func TestProfileKeepsEmptyEpochsVisible(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		startEvent("session-1", "resume", at(0)),
		startEvent("session-1", "compact", at(time.Second)),
		readInTurn("session-1", "turn-1", "/repo/a.go", at(2*time.Second)),
	)

	out := profileOutput(t, dir)

	for _, want := range []string{
		"1  opened by resume, ended by a context reset",
		"no tool call recorded",
		"2  opened by compact, nothing recorded after it",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

// A log Axiom started reading part way through has work with no start before
// it, which is not the same as a session that reported no source.
func TestProfileDistinguishesUnknownOpenings(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		readInTurn("session-1", "turn-1", "/repo/a.go", at(0)),
		startEvent("session-2", "", at(time.Second)),
		readInTurn("session-2", "turn-2", "/repo/b.go", at(2*time.Second)),
		startEvent("session-3", "teleport", at(3*time.Second)),
		readInTurn("session-3", "turn-3", "/repo/c.go", at(4*time.Second)),
	)

	out := profileOutput(t, dir)

	for _, want := range []string{
		"1  no start recorded, nothing recorded after it",
		"1  opened by a start with no source, nothing recorded after it",
		// A source no version of Axiom knows is still reported as itself.
		"1  opened by teleport, nothing recorded after it",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

// A session end that closed nothing is the only trace of the context it
// belonged to, so it is reported rather than dropped.
func TestProfileReportsSessionEndsThatClosedNothing(t *testing.T) {
	t.Parallel()

	dir := seed(t, endEvent("session-1", "clear", at(0)))

	out := profileOutput(t, dir)

	for _, want := range []string{
		"session session-1, no context epoch recorded",
		"1 session end recorded with no context open",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

// Records that named no session have no context to belong to, and the report
// has to account for them rather than leaving them out of every total.
func TestProfileAccountsForRecordsWithoutASession(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		readEvent("", "/repo/a.go", at(0), 4),
		startEvent("session-1", "startup", at(time.Second)),
		readInTurn("session-1", "turn-1", "/repo/b.go", at(2*time.Second)),
	)

	out := profileOutput(t, dir)

	if !strings.Contains(flat(out), "1 record carried no session identity") {
		t.Errorf("output does not account for the record with no session:\n%s", out)
	}
}

// A log that placed nothing has no epochs to show, and saying so is not the
// same as showing an empty list.
func TestProfileWithoutAnyPlaceableRecord(t *testing.T) {
	t.Parallel()

	dir := seed(t, readEvent("", "/repo/a.go", at(0), 4))

	out := profileOutput(t, dir)

	for _, want := range []string{"No context epoch was recorded.", "1 record carried no session identity"} {
		if !strings.Contains(flat(out), want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

// An end the agent gave no reason for is reported as one, not as an end that
// happened for no reason.
func TestProfileReportsAnEndWithoutAReason(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		startEvent("session-1", "startup", at(0)),
		readInTurn("session-1", "turn-1", "/repo/a.go", at(time.Second)),
		endEvent("session-1", "", at(2*time.Second)),
	)

	out := profileOutput(t, dir)

	if !strings.Contains(out, "ended with the session, reason not recorded") {
		t.Errorf("output does not say the reason was missing:\n%s", out)
	}
}

// Long logs accumulate sessions. Everything omitted from the display is
// accounted for, so the limit hides no work.
func TestProfileAccountsForTheContextItDoesNotShow(t *testing.T) {
	t.Parallel()

	var events []event.Event
	for session := range 8 {
		id := fmt.Sprintf("session-%d", session)
		when := at(time.Duration(session) * time.Minute)
		events = append(events, startEvent(id, "startup", when))
		// One session compacts far more often than the display shows.
		for epoch := range 9 {
			events = append(events,
				readInTurn(id, "turn-1", "/repo/a.go", when.Add(time.Duration(epoch)*time.Second)),
				startEvent(id, "compact", when.Add(time.Duration(epoch)*time.Second+500*time.Millisecond)),
			)
		}
	}

	out := profileOutput(t, seed(t, events...))

	for _, want := range []string{
		"3 earlier sessions omitted (30 epochs, 27 tool calls)",
		"4 earlier epochs omitted (4 tool calls)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	rejectOverlongLines(t, out)
}

func TestProfileScopesToOneSession(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		startEvent("session-1", "startup", at(0)),
		readInTurn("session-1", "turn-1", "/repo/a.go", at(time.Second)),
		readInTurn("session-1", "turn-1", "/repo/a.go", at(2*time.Second)),
		startEvent("session-2", "startup", at(3*time.Second)),
		readInTurn("session-2", "turn-2", "/repo/b.go", at(4*time.Second)),
	)

	out := scopedProfileOutput(t, dir, profileOptions{session: "session-2"})

	for _, want := range []string{
		"Scope               session session-2",
		"Events              2",
		"Sessions analyzed   1",
		"Tool calls          1",
		"session session-2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "session-1") {
		t.Errorf("the report includes a session it was not scoped to:\n%s", out)
	}
	// The repetition belongs to the session that was left out, so scoping it
	// away has to leave the findings behind with it.
	if !strings.Contains(out, "No high-confidence redundant work") {
		t.Errorf("a finding from another session survived scoping:\n%s", out)
	}
}

// Scoping selects; it does not change what the analysis does. A report of a log
// holding one session is the same report either way, apart from saying what it
// was scoped to.
func TestScopingOneSessionMatchesTheWholeLog(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		startEvent("session-1", "startup", at(0)),
		readInTurn("session-1", "turn-1", "/repo/a.go", at(time.Second)),
		readInTurn("session-1", "turn-1", "/repo/a.go", at(2*time.Second)),
		endEvent("session-1", "other", at(3*time.Second)),
	)

	whole := profileOutput(t, dir)
	scoped := scopedProfileOutput(t, dir, profileOptions{session: "session-1"})

	if _, rest, found := strings.Cut(scoped, "Scope               session session-1\n"); !found {
		t.Fatalf("the scoped report does not say what it covered:\n%s", scoped)
	} else if rest != strings.TrimPrefix(whole, "Axiom Profile\n─────────────\n\n") {
		t.Errorf("scoping changed the report:\n--- scoped ---\n%s\n--- whole ---\n%s", rest, whole)
	}
}

// An identifier is the agent's, exactly. A prefix of one is a different string,
// and analyzing the session it happens to match would answer a question nobody
// asked.
func TestSessionScopeMatchesIdentifiersExactly(t *testing.T) {
	t.Parallel()

	dir := seed(t, readEvent("session-1", "/repo/a.go", at(0), 4))

	out := scopedProfileOutput(t, dir, profileOptions{session: "session"})

	for _, want := range []string{
		`No events recorded for session "session".`,
		"Run 'axiom profile' to see the sessions in the log.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Axiom Profile") {
		t.Errorf("an unmatched session produced a report:\n%s", out)
	}
}

// A record Axiom could not decode names no session, so "no events for that
// session" is only as strong as the log is complete. The report says so rather
// than reporting an absence it cannot establish.
func TestUnmatchedSessionAccountsForSkippedRecords(t *testing.T) {
	t.Parallel()

	dir := seed(t, readEvent("session-1", "/repo/a.go", at(0), 4))
	path := filepath.Join(dir, "events.jsonl")
	log, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if err := os.WriteFile(path, append(log, []byte("{not json}\n")...), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}

	out := scopedProfileOutput(t, dir, profileOptions{session: "session-2"})

	for _, want := range []string{
		`No events recorded for session "session-2".`,
		"1 record skipped and could not be attributed to any session.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

// An epoch and the span the profiler compares repetition in are the same thing
// at a recorded start, and only there. A session end closes an epoch without
// ending a run, so a record written after one — hooks are separate processes
// and can finish out of order — continues the run into the next epoch.
//
// This is pinned because the report must not claim otherwise: the scope
// footnote names the session and the reset, never the epoch.
func TestAFindingCanSpanAnEpochClosedByASessionEnd(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		startEvent("session-1", "startup", at(0)),
		readInTurn("session-1", "turn-1", "/repo/a.go", at(time.Second)),
		endEvent("session-1", "other", at(2*time.Second)),
		readInTurn("session-1", "turn-1", "/repo/a.go", at(3*time.Second)),
	)

	out := profileOutput(t, dir)

	for _, want := range []string{
		"1  opened by startup, ended with the session (other)",
		"2  no start recorded, nothing recorded after it",
		"Repeated file read",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

func TestParseProfileFlags(t *testing.T) {
	t.Parallel()

	opts, err := parseProfileFlags([]string{"--session", "abc"})
	if err != nil {
		t.Fatalf("parseProfileFlags: %v", err)
	}
	if opts.session != "abc" {
		t.Errorf("session = %q, want %q", opts.session, "abc")
	}

	if _, err := parseProfileFlags(nil); err != nil {
		t.Errorf("parseProfileFlags with no arguments: %v", err)
	}

	for _, args := range [][]string{
		{"yesterday"},
		{"--session"},
		{"--sessions", "abc"},
		{"--session", "abc", "extra"},
		// Asking for a session and naming none is a mistake worth reporting.
		// Reading it as "every session" would answer a different question
		// without saying so.
		{"--session", ""},
		{"--session="},
	} {
		if _, err := parseProfileFlags(args); !IsUsage(err) {
			t.Errorf("parseProfileFlags(%q) error = %v, want a usage error", args, err)
		}
	}
}
