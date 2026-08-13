package harness_test

import (
	"testing"

	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/harness"
)

func start(session string, components ...event.HarnessComponent) event.Event {
	ev := event.Event{
		Type:      event.TypeSessionStart,
		SessionID: session,
		Session:   &event.Session{Source: "startup"},
	}
	if components != nil {
		ev.Session.Harness = &event.Harness{Components: components}
	}
	return ev
}

func instructions(digest string) event.HarnessComponent {
	return event.HarnessComponent{
		Kind:   event.HarnessProjectInstructions,
		Path:   "CLAUDE.md",
		Status: event.HarnessObserved,
		Digest: digest,
	}
}

func report(events ...event.Event) harness.Report {
	a := harness.New()
	for _, ev := range events {
		a.Add(ev)
	}
	return a.Report()
}

// A session identity can record several starts, and the files can change
// between two of them. Merging the observations would invent one harness for a
// session that was observed under two.
func TestDifferingObservationsInOneSessionStayApart(t *testing.T) {
	r := report(
		start("s1", instructions("aaaa")),
		start("s1", instructions("bbbb")),
	)

	if len(r.Sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(r.Sessions))
	}
	s := r.Sessions[0]
	if len(s.Observations) != 2 {
		t.Fatalf("got %d observations, want 2: %+v", len(s.Observations), s.Observations)
	}
	if s.Observations[0].Components[0].Digest == s.Observations[1].Components[0].Digest {
		t.Error("two different observations were reported as one")
	}
	if got := s.Observations[1].Starts; len(got) != 1 || got[0] != 2 {
		t.Errorf("second observation covers starts %v, want the second start", got)
	}
}

// Two sessions are never merged into one another, whatever they observed.
func TestSessionsAreReportedSeparately(t *testing.T) {
	r := report(
		start("s1", instructions("aaaa")),
		start("s2", instructions("bbbb")),
	)

	if len(r.Sessions) != 2 {
		t.Fatalf("got %d sessions, want 2", len(r.Sessions))
	}
	if r.Sessions[0].ID != "s1" || r.Sessions[1].ID != "s2" {
		t.Errorf("sessions = %s and %s, want them in the order the log records them",
			r.Sessions[0].ID, r.Sessions[1].ID)
	}
}

// Consecutive starts that observed the same components are reported together.
// It says the observations matched, and the ordinals keep saying how many
// starts there were.
func TestIdenticalConsecutiveObservationsAreReportedTogether(t *testing.T) {
	r := report(
		start("s1", instructions("aaaa")),
		start("s1", instructions("aaaa")),
		start("s1", instructions("aaaa")),
	)

	s := r.Sessions[0]
	if len(s.Observations) != 1 {
		t.Fatalf("got %d observations, want 1: %+v", len(s.Observations), s.Observations)
	}
	if got := s.Observations[0].Starts; len(got) != 3 {
		t.Errorf("observation covers %v, want all three starts", got)
	}
	if s.Starts != 3 {
		t.Errorf("counted %d starts, want 3", s.Starts)
	}
}

// A start that recorded nothing sits between two matching observations.
// Joining them across it would close a gap the log actually has.
func TestAGapIsNotClosedByAMatch(t *testing.T) {
	r := report(
		start("s1", instructions("aaaa")),
		start("s1"),
		start("s1", instructions("aaaa")),
	)

	s := r.Sessions[0]
	if len(s.Observations) != 2 {
		t.Fatalf("got %d observations, want 2: %+v", len(s.Observations), s.Observations)
	}
	if got := s.Observations[1].Starts; len(got) != 1 || got[0] != 3 {
		t.Errorf("second observation covers %v, want the third start", got)
	}
	if s.StartsWithoutProvenance != 1 {
		t.Errorf("counted %d starts without provenance, want 1", s.StartsWithoutProvenance)
	}
}

// A session recorded before Axiom observed any of this holds no provenance.
// That is what the report says: not an empty harness, and not the same as
// another session that recorded none either.
func TestASessionWithNoProvenanceReportsNone(t *testing.T) {
	r := report(start("historical"), start("also-historical"))

	if r.Recorded() {
		t.Error("a log with no recorded provenance reported some")
	}
	for _, s := range r.Sessions {
		if len(s.Observations) != 0 {
			t.Errorf("session %s reported %d observations", s.ID, len(s.Observations))
		}
		if s.StartsWithoutProvenance != 1 {
			t.Errorf("session %s counted %d starts without provenance, want 1",
				s.ID, s.StartsWithoutProvenance)
		}
	}
	// Two sessions that each recorded nothing are two sessions that recorded
	// nothing, and never two sessions established to match.
	if len(r.Sessions) != 2 {
		t.Fatalf("got %d sessions, want 2", len(r.Sessions))
	}
}

// Only a session start establishes anything, and only one naming a session can
// be attributed.
func TestOtherRecordsContributeNothing(t *testing.T) {
	toolCall := event.Event{
		Type:      event.TypeToolCall,
		SessionID: "s1",
		Tool:      &event.ToolCall{Name: "Read", Outcome: event.OutcomeSuccess},
	}
	anonymous := start("", instructions("aaaa"))

	if r := report(toolCall, anonymous); len(r.Sessions) != 0 {
		t.Errorf("got %d sessions, want none: %+v", len(r.Sessions), r.Sessions)
	}
}
