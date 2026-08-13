package harness_test

import (
	"testing"

	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/harness"
)

func file(kind event.HarnessKind, path string, status event.HarnessStatus, digest string) event.HarnessComponent {
	return event.HarnessComponent{Kind: kind, Path: path, Status: status, Digest: digest}
}

func claude(status event.HarnessStatus, digest string) event.HarnessComponent {
	return file(event.HarnessProjectInstructions, "CLAUDE.md", status, digest)
}

func settings(status event.HarnessStatus, digest string) event.HarnessComponent {
	return file(event.HarnessProjectSettings, ".claude/settings.json", status, digest)
}

// agents is the definitions directory, which carries no digest: what it
// records is that enumeration was attempted and what came of it.
func agents(status event.HarnessStatus) event.HarnessComponent {
	return file(event.HarnessSubagentDirectory, ".claude/agents", status, "")
}

func definition(name string, status event.HarnessStatus, digest string) event.HarnessComponent {
	return file(event.HarnessSubagentDefinition, ".claude/agents/"+name, status, digest)
}

func observation(components ...event.HarnessComponent) harness.Observation {
	return harness.Observation{Starts: []int{1}, Components: components}
}

// verdictAt reports what a comparison established about one observed path.
func verdictAt(t *testing.T, changes []harness.Change, path string) harness.Verdict {
	t.Helper()

	for _, c := range changes {
		if c.Path == path {
			return c.Verdict
		}
	}
	t.Fatalf("no change for %s in %+v", path, changes)
	return ""
}

func wantVerdict(t *testing.T, changes []harness.Change, path string, want harness.Verdict) {
	t.Helper()

	if got := verdictAt(t, changes, path); got != want {
		t.Errorf("%s = %q, want %q", path, got, want)
	}
}

// The two comparisons the evidence supports outright: one file, read on both
// sides, whose bytes either matched or did not.
func TestObservedComponentsAreComparedByDigest(t *testing.T) {
	t.Parallel()

	same := harness.Compare(
		observation(claude(event.HarnessObserved, "aaaa")),
		observation(claude(event.HarnessObserved, "aaaa")),
	)
	wantVerdict(t, same, "CLAUDE.md", harness.VerdictSame)

	differed := harness.Compare(
		observation(claude(event.HarnessObserved, "aaaa")),
		observation(claude(event.HarnessObserved, "bbbb")),
	)
	wantVerdict(t, differed, "CLAUDE.md", harness.VerdictDiffered)
}

// Absence is a positive observation: Axiom looked at that path and found
// nothing there. Two of them are comparable, and so is one against a file.
func TestAbsenceIsComparedAsEvidence(t *testing.T) {
	t.Parallel()

	both := harness.Compare(
		observation(settings(event.HarnessAbsent, "")),
		observation(settings(event.HarnessAbsent, "")),
	)
	wantVerdict(t, both, ".claude/settings.json", harness.VerdictAbsent)

	appeared := harness.Compare(
		observation(claude(event.HarnessAbsent, "")),
		observation(claude(event.HarnessObserved, "bbbb")),
	)
	wantVerdict(t, appeared, "CLAUDE.md", harness.VerdictAppeared)

	disappeared := harness.Compare(
		observation(claude(event.HarnessObserved, "aaaa")),
		observation(claude(event.HarnessAbsent, "")),
	)
	wantVerdict(t, disappeared, "CLAUDE.md", harness.VerdictDisappeared)
}

// The load-bearing refusal. A component Axiom did not establish is a limit of
// the observation, and reporting it as a change would turn the observer's
// failure into a fact about a project.
func TestTheObserversLimitsAreNeverAChange(t *testing.T) {
	t.Parallel()

	for _, status := range []event.HarnessStatus{event.HarnessUnreadable, event.HarnessNotFollowed} {
		// On either side, and against every state the other side could
		// have been in.
		for _, other := range []event.HarnessComponent{
			claude(event.HarnessObserved, "aaaa"),
			claude(event.HarnessAbsent, ""),
			claude(event.HarnessUnreadable, ""),
			claude(event.HarnessNotFollowed, ""),
		} {
			stopped := claude(status, "")
			wantVerdict(t, harness.Compare(observation(stopped), observation(other)),
				"CLAUDE.md", harness.VerdictNotEstablished)
			wantVerdict(t, harness.Compare(observation(other), observation(stopped)),
				"CLAUDE.md", harness.VerdictNotEstablished)
		}
	}
}

// A component one record holds and the other does not is two versions of Axiom
// having looked at different paths. The one that does not name it never looked,
// which is not the same as having looked and found nothing.
func TestAComponentOnlyOneSideLookedAtIsNotEstablished(t *testing.T) {
	t.Parallel()

	// An older Axiom that observed the instruction file alone, against one
	// that also observed the project's settings.
	changes := harness.Compare(
		observation(claude(event.HarnessObserved, "aaaa")),
		observation(claude(event.HarnessObserved, "aaaa"), settings(event.HarnessObserved, "cccc")),
	)

	wantVerdict(t, changes, "CLAUDE.md", harness.VerdictSame)
	wantVerdict(t, changes, ".claude/settings.json", harness.VerdictNotEstablished)

	// The other way round, the same: the component did not disappear.
	reversed := harness.Compare(
		observation(claude(event.HarnessObserved, "aaaa"), settings(event.HarnessObserved, "cccc")),
		observation(claude(event.HarnessObserved, "aaaa")),
	)
	wantVerdict(t, reversed, ".claude/settings.json", harness.VerdictNotEstablished)
}

// The directory carries no digest. Both sides reaching it establishes that
// enumeration happened, and what each one found is established by the
// definitions rather than by the directory.
func TestTheDefinitionsDirectoryComparesAsAnEnumeration(t *testing.T) {
	t.Parallel()

	changes := harness.Compare(
		observation(agents(event.HarnessObserved)),
		observation(agents(event.HarnessObserved)),
	)

	wantVerdict(t, changes, ".claude/agents", harness.VerdictEnumerated)
}

// Set membership is established by enumeration. Where both sides enumerated,
// a definition only one of them names is one the other looked for and did not
// find.
func TestADefinitionAppearsOnlyWhereBothSidesEnumerated(t *testing.T) {
	t.Parallel()

	changes := harness.Compare(
		observation(agents(event.HarnessObserved), definition("explore.md", event.HarnessObserved, "aaaa")),
		observation(agents(event.HarnessObserved),
			definition("explore.md", event.HarnessObserved, "aaaa"),
			definition("review.md", event.HarnessObserved, "bbbb")),
	)

	wantVerdict(t, changes, ".claude/agents/explore.md", harness.VerdictSame)
	wantVerdict(t, changes, ".claude/agents/review.md", harness.VerdictAppeared)

	reversed := harness.Compare(
		observation(agents(event.HarnessObserved),
			definition("explore.md", event.HarnessObserved, "aaaa"),
			definition("review.md", event.HarnessObserved, "bbbb")),
		observation(agents(event.HarnessObserved), definition("explore.md", event.HarnessObserved, "aaaa")),
	)
	wantVerdict(t, reversed, ".claude/agents/review.md", harness.VerdictDisappeared)
}

// A directory that was not there held no definitions, which is an enumeration
// too: the set is established as empty.
func TestAnAbsentDirectoryEstablishesAnEmptySet(t *testing.T) {
	t.Parallel()

	changes := harness.Compare(
		observation(agents(event.HarnessAbsent)),
		observation(agents(event.HarnessObserved), definition("explore.md", event.HarnessObserved, "aaaa")),
	)

	wantVerdict(t, changes, ".claude/agents", harness.VerdictAppeared)
	wantVerdict(t, changes, ".claude/agents/explore.md", harness.VerdictAppeared)
}

// A directory Axiom could not read establishes no set, so a definition missing
// from that side is one Axiom did not look for. Inferring that it was not there
// would read a full directory Axiom declined to enumerate as an empty one.
func TestADirectoryThatEstablishedNoSetInfersNoDefinition(t *testing.T) {
	t.Parallel()

	for _, blocked := range []event.HarnessStatus{event.HarnessUnreadable, event.HarnessNotFollowed} {
		changes := harness.Compare(
			observation(agents(blocked)),
			observation(agents(event.HarnessObserved), definition("explore.md", event.HarnessObserved, "aaaa")),
		)

		wantVerdict(t, changes, ".claude/agents", harness.VerdictNotEstablished)
		wantVerdict(t, changes, ".claude/agents/explore.md", harness.VerdictNotEstablished)
	}

	// And where the directory is not in the record at all, which is what an
	// Axiom that did not enumerate definitions wrote.
	changes := harness.Compare(
		observation(claude(event.HarnessObserved, "aaaa")),
		observation(claude(event.HarnessObserved, "aaaa"),
			agents(event.HarnessObserved),
			definition("explore.md", event.HarnessObserved, "aaaa")),
	)
	wantVerdict(t, changes, ".claude/agents/explore.md", harness.VerdictNotEstablished)
}

// A definition the enumeration named and Axiom did not read establishes the
// name and not the file. It is the observer's limit again, and it is not an
// appearance.
func TestADefinitionAxiomDidNotReadIsNotAnAppearance(t *testing.T) {
	t.Parallel()

	changes := harness.Compare(
		observation(agents(event.HarnessObserved)),
		observation(agents(event.HarnessObserved), definition("explore.md", event.HarnessNotFollowed, "")),
	)

	wantVerdict(t, changes, ".claude/agents/explore.md", harness.VerdictNotEstablished)
}

// Nothing is summarized over the components. A comparison is the components
// and no value above them, because one number over a set of unlike paths is
// the harness identity ADR 0018 refused.
func TestAComparisonIsComponentsAndNothingElse(t *testing.T) {
	t.Parallel()

	changes := harness.Compare(
		observation(claude(event.HarnessObserved, "aaaa"), settings(event.HarnessAbsent, "")),
		observation(claude(event.HarnessObserved, "bbbb"), settings(event.HarnessAbsent, "")),
	)

	if len(changes) != 2 {
		t.Fatalf("got %d changes, want one per observed path: %+v", len(changes), changes)
	}
	// The order the collector wrote, so that a report never has to sort
	// paths it was handed.
	if changes[0].Path != "CLAUDE.md" || changes[1].Path != ".claude/settings.json" {
		t.Errorf("changes = %+v, want them in the recorded order", changes)
	}
	if changes[0].Kind != event.HarnessProjectInstructions {
		t.Errorf("kind = %q, want the recorded kind rather than one parsed from the path",
			changes[0].Kind)
	}
}

// A capture recorded under two different observations has no single one. ADR
// 0018 keeps them apart, and choosing one here would present the conditions
// part of a capture ran under as the conditions all of it ran under.
func TestOnlyASingleObservationIsComparable(t *testing.T) {
	t.Parallel()

	one := report(start("s1", instructions("aaaa")), start("s1", instructions("aaaa")))
	s, ok := one.Session("s1")
	if !ok {
		t.Fatal("the recorded session was not found")
	}
	if _, ok := s.Comparable(); !ok {
		t.Error("a session whose starts all observed the same components has no comparable observation")
	}

	two := report(start("s1", instructions("aaaa")), start("s1", instructions("bbbb")))
	changed, _ := two.Session("s1")
	if _, ok := changed.Comparable(); ok {
		t.Error("a session observed under two different sets of components offered one of them")
	}

	none := report(start("s1"))
	blind, _ := none.Session("s1")
	if _, ok := blind.Comparable(); ok {
		t.Error("a session that recorded no provenance offered an observation")
	}
}

// A session the log never recorded a start for is held apart from one that
// started and observed nothing. The first never looked; the second did.
func TestSessionLookupHoldsSilenceApartFromAbsence(t *testing.T) {
	t.Parallel()

	r := report(start("s1"))

	s, ok := r.Session("s1")
	if !ok {
		t.Fatal("a session that recorded a start was not found")
	}
	if s.StartsWithoutProvenance != 1 {
		t.Errorf("counted %d starts without provenance, want 1", s.StartsWithoutProvenance)
	}
	if _, ok := r.Session("never-started"); ok {
		t.Error("a session identity the log does not hold was found")
	}
}
