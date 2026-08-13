package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/exequieldeferrari/axiom/internal/event"
)

// harnessSection is the provenance block of one comparison.
func harnessSection(t *testing.T, opts compareOptions) string {
	t.Helper()

	return compareSection(t, compareOutput(t, opts), "Observed harness provenance")
}

// capturedStart records one session start in root, through the hook path that
// observes provenance, into a data directory of its own.
//
// Nothing here is constructed: the components the comparison reads are the
// ones the collector wrote after looking at a real project.
func capturedStart(t *testing.T, session, root string) string {
	t.Helper()

	dir := t.TempDir()
	recordStart(t, dir, session, root)
	return dir
}

// blindCapture records a start Axiom could resolve no project for, which is
// one of the two ways a capture holds no provenance.
func blindCapture(t *testing.T, session string) string {
	t.Helper()

	dir := t.TempDir()
	payload := `{"hook_event_name":"SessionStart","session_id":"` + session + `","source":"startup"}`
	if err := runClaudeHook(strings.NewReader(payload), dir, hookNow); err != nil {
		t.Fatalf("runClaudeHook: %v", err)
	}
	return dir
}

// verdictFor reads back what the report said about one observed path, so
// that a test asserts on the verdict rather than on the padding around it.
func verdictFor(t *testing.T, block, path string) string {
	t.Helper()

	for line := range strings.SplitSeq(block, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, path) {
			continue
		}
		return strings.TrimSpace(strings.TrimPrefix(trimmed, path))
	}
	t.Fatalf("no component %q in:\n%s", path, block)
	return ""
}

// unwrapped joins a block's prose back into one line, so that an assertion
// about a sentence does not depend on where the report broke it.
func unwrapped(block string) string {
	return strings.Join(strings.Fields(block), " ")
}

func wantComponent(t *testing.T, block, path, want string) {
	t.Helper()

	if got := verdictFor(t, block, path); got != want {
		t.Errorf("%s = %q, want %q\n%s", path, got, want, block)
	}
}

// Two captures recorded under one unchanged project. Every component matched,
// which is a statement about bytes at two recorded moments and nothing more.
func TestCompareReportsMatchingProvenance(t *testing.T) {
	root := harnessProject(t, map[string]string{
		"CLAUDE.md":                   "# guidance\n",
		".claude/settings.local.json": `{"hooks":{}}`,
		".claude/agents/explore.md":   "explore\n",
	})

	block := harnessSection(t, sides(
		capturedStart(t, "before", root),
		capturedStart(t, "after", root),
	))

	wantComponent(t, block, "CLAUDE.md", "same bytes")
	wantComponent(t, block, ".claude/settings.local.json", "same bytes")
	wantComponent(t, block, ".claude/settings.json", "nothing found on either side")
	wantComponent(t, block, ".claude/agents", "enumerated on both sides")
	wantComponent(t, block, "explore.md", "same bytes")
	// Matching components are never reported as a matching harness, and
	// never as evidence that the two captures are comparable.
	for _, forbidden := range []string{"same harness", "identical", "equivalent", "unchanged harness"} {
		if strings.Contains(strings.ToLower(block), forbidden) {
			t.Errorf("the block claims %q:\n%s", forbidden, block)
		}
	}
}

// One byte of difference makes a different digest, and that is the whole of
// what the report says about it: no magnitude, and no content.
func TestCompareReportsOneChangedInstructionFile(t *testing.T) {
	root := harnessProject(t, map[string]string{
		"CLAUDE.md":                 "# guidance\n",
		".claude/agents/explore.md": "explore\n",
	})
	baseline := capturedStart(t, "before", root)
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("# guidance!\n"), 0o600); err != nil {
		t.Fatalf("rewrite instructions: %v", err)
	}
	candidate := capturedStart(t, "after", root)

	block := harnessSection(t, sides(baseline, candidate))

	wantComponent(t, block, "CLAUDE.md", "different bytes")
	// Everything the change did not touch still reports as itself, so that
	// one difference cannot be read as a different project.
	wantComponent(t, block, "explore.md", "same bytes")
	// The contents of neither version reach the page, and neither does a
	// count of how much of the file moved.
	for _, forbidden := range []string{"guidance", "1 line", "bytes changed", "larger", "smaller"} {
		if strings.Contains(block, forbidden) {
			t.Errorf("the block carries %q:\n%s", forbidden, block)
		}
	}
}

// A definition present on one side and enumerated away on the other is said of
// the two sides, not of two moments: which capture is the baseline is the
// operator's choice.
func TestCompareReportsAComponentObservedOnOneSideOnly(t *testing.T) {
	root := harnessProject(t, map[string]string{
		"CLAUDE.md":                 "# guidance\n",
		".claude/agents/explore.md": "explore\n",
	})
	baseline := capturedStart(t, "before", root)
	if err := os.WriteFile(filepath.Join(root, ".claude", "agents", "review.md"),
		[]byte("review\n"), 0o600); err != nil {
		t.Fatalf("write definition: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude", "settings.json"),
		[]byte(`{"model":"opus"}`), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	candidate := capturedStart(t, "after", root)

	block := harnessSection(t, sides(baseline, candidate))
	wantComponent(t, block, "review.md", "observed in the candidate only")
	wantComponent(t, block, ".claude/settings.json", "observed in the candidate only")

	// Reversed, the same evidence reads from the other side. Nothing in the
	// wording carries an order between two captures.
	reversed := harnessSection(t, sides(candidate, baseline))
	wantComponent(t, reversed, "review.md", "observed in the baseline only")
	for _, temporal := range []string{"after", "before", "later", "earlier", "no longer", "was added", "removed"} {
		if strings.Contains(strings.ToLower(reversed), temporal) {
			t.Errorf("the block reads an order into two captures (%q):\n%s", temporal, reversed)
		}
	}
}

// The observer's limit is never a change in a project. A link Axiom declined
// to read through leaves the comparison unestablished on both sides.
func TestCompareHoldsAnUnfollowedLinkApartFromAChange(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege on windows")
	}

	root := harnessProject(t, map[string]string{"CLAUDE.md": "# guidance\n"})
	baseline := capturedStart(t, "before", root)

	outside := t.TempDir()
	target := filepath.Join(outside, "private.md")
	if err := os.WriteFile(target, []byte("secret\n"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "CLAUDE.md")); err != nil {
		t.Fatalf("remove instructions: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(root, "CLAUDE.md")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	candidate := capturedStart(t, "after", root)

	block := harnessSection(t, sides(baseline, candidate))

	wantComponent(t, block, "CLAUDE.md", "not established")
	// Neither the target nor anything about it may reach a comparison.
	for _, leaked := range []string{target, outside, "private", "secret", "link"} {
		if strings.Contains(block, leaked) {
			t.Errorf("the block carries %q:\n%s", leaked, block)
		}
	}
	// A path Axiom did not establish is not a path that disappeared.
	for _, forbidden := range []string{"only", "different bytes", "nothing found"} {
		if strings.Contains(verdictFor(t, block, "CLAUDE.md"), forbidden) {
			t.Errorf("an unestablished component reads as a change:\n%s", block)
		}
	}
}

// A definition cannot be said to have appeared unless both sides established
// which definitions there were. A directory Axiom declined to enumerate is a
// directory whose contents are unknown, not an empty one.
func TestCompareInfersNoDefinitionFromAnUnenumeratedDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege on windows")
	}

	root := harnessProject(t, map[string]string{"CLAUDE.md": "# guidance\n"})
	// A definitions directory that leads out of the project is not followed,
	// so the baseline establishes nothing about what it held.
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "explore.md"), []byte("explore\n"), 0o600); err != nil {
		t.Fatalf("write definition: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatalf("create .claude: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".claude", "agents")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	baseline := capturedStart(t, "before", root)

	if err := os.Remove(filepath.Join(root, ".claude", "agents")); err != nil {
		t.Fatalf("remove link: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".claude", "agents"), 0o755); err != nil {
		t.Fatalf("create agents: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude", "agents", "explore.md"),
		[]byte("explore\n"), 0o600); err != nil {
		t.Fatalf("write definition: %v", err)
	}
	candidate := capturedStart(t, "after", root)

	block := harnessSection(t, sides(baseline, candidate))

	wantComponent(t, block, ".claude/agents", "not established")
	wantComponent(t, block, "explore.md", "not established")
}

// Three silences, each reported as itself, and none of them as a harness that
// matched. A reader who skims the block must not come away thinking the two
// captures were established to have been recorded under the same thing.
func TestCompareStatesWhyProvenanceWasNotCompared(t *testing.T) {
	root := harnessProject(t, map[string]string{"CLAUDE.md": "# guidance\n"})
	observed := capturedStart(t, "observed", root)
	blind := blindCapture(t, "blind")

	cases := []struct {
		name  string
		opts  compareOptions
		wants []string
	}{
		{"baseline only", sides(blind, observed),
			[]string{"the baseline recorded no harness provenance"}},
		{"candidate only", sides(observed, blind),
			[]string{"the candidate recorded no harness provenance"}},
		{"neither", sides(blind, blind),
			[]string{"neither capture recorded harness provenance"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			block := harnessSection(t, c.opts)

			for _, want := range append(c.wants, "Components are not compared",
				"is not established", "not a statement that they differed") {
				if !strings.Contains(unwrapped(block), want) {
					t.Errorf("the block omits %q:\n%s", want, block)
				}
			}
			// The silence is never rendered as configuration the agent
			// did without.
			for _, forbidden := range []string{
				"no configuration", "default harness", "unconfigured", "same bytes",
			} {
				if strings.Contains(strings.ToLower(block), forbidden) {
					t.Errorf("the empty state claims %q:\n%s", forbidden, block)
				}
			}
		})
	}
}

// A session observed under two different sets of components was recorded under
// two, and neither of them describes the capture. ADR 0018 keeps them apart,
// and a comparison that picked one would undo that.
func TestCompareChoosesNoObservationForACaptureObservedUnderTwo(t *testing.T) {
	root := harnessProject(t, map[string]string{"CLAUDE.md": "# before\n"})
	observed := capturedStart(t, "observed", root)

	// One session identity, two starts, with the file rewritten between
	// them: exactly what a compaction after an edit records.
	twice := t.TempDir()
	recordStart(t, twice, "twice", root)
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("# after\n"), 0o600); err != nil {
		t.Fatalf("rewrite instructions: %v", err)
	}
	recordStart(t, twice, "twice", root)

	block := harnessSection(t, sides(twice, observed))

	for _, want := range []string{
		"2 distinct observations recorded",
		"more than one distinct observation",
		"does not choose one of them",
	} {
		if !strings.Contains(unwrapped(block), want) {
			t.Errorf("the block omits %q:\n%s", want, block)
		}
	}
	if strings.Contains(block, "same bytes") || strings.Contains(block, "different bytes") {
		t.Errorf("one of two observations was made to stand for the capture:\n%s", block)
	}
}

// A capture whose records begin after its session did recorded no start, which
// is a different silence from a start that observed nothing.
func TestCompareHoldsAMissingStartApartFromAStartThatObservedNothing(t *testing.T) {
	root := harnessProject(t, map[string]string{"CLAUDE.md": "# guidance\n"})
	observed := capturedStart(t, "observed", root)
	// A log holding work and no start at all.
	headless := seed(t, callEvent("headless", "turn-1", "", at(0), wholeRead("/repo/a.go")))

	block := harnessSection(t, sides(headless, observed))

	if !strings.Contains(block, "no session start was recorded") {
		t.Errorf("a capture with no recorded start is not distinguished:\n%s", block)
	}
	if strings.Contains(block, "no harness provenance recorded at") {
		t.Errorf("a capture that never started is reported as one that observed nothing:\n%s", block)
	}
}

// Several starts that observed the same components are one observation, and a
// capture holding one compares like any other. The starts are still named, so
// the report never claims that one harness spanned them.
func TestCompareComparesACaptureThatStartedSeveralTimes(t *testing.T) {
	root := harnessProject(t, map[string]string{"CLAUDE.md": "# guidance\n"})

	resumed := t.TempDir()
	recordStart(t, resumed, "resumed", root)
	recordStart(t, resumed, "resumed", root)

	block := harnessSection(t, sides(resumed, capturedStart(t, "single", root)))

	if !strings.Contains(block, "session starts 1, 2, same observed components") {
		t.Errorf("the starts one observation covers are not named:\n%s", block)
	}
	wantComponent(t, block, "CLAUDE.md", "same bytes")
}

// A start that recorded no provenance beside one that did is a gap in the
// evidence, and it is reported rather than closed over.
func TestCompareAccountsForAStartThatRecordedNoProvenance(t *testing.T) {
	root := harnessProject(t, map[string]string{"CLAUDE.md": "# guidance\n"})

	// One session identity: a start that observed the project, and a later
	// start recorded by an Axiom that could resolve none.
	partial := t.TempDir()
	recordStart(t, partial, "partial", root)
	blind := `{"hook_event_name":"SessionStart","session_id":"partial","source":"resume"}`
	if err := runClaudeHook(strings.NewReader(blind), partial, hookNow); err != nil {
		t.Fatalf("runClaudeHook: %v", err)
	}

	block := harnessSection(t, sides(partial, capturedStart(t, "single", root)))

	if !strings.Contains(block, "1 session start recorded no harness provenance") {
		t.Errorf("the start that recorded nothing is not accounted for:\n%s", block)
	}
	// The observation that does exist is still compared, and the gap sits
	// beside it rather than in place of it.
	wantComponent(t, block, "CLAUDE.md", "same bytes")
}

// The property the whole feature rests on. Provenance was recorded when each
// session started, so a project rewritten or deleted afterwards cannot change
// what a comparison of those captures says.
func TestComparedProvenanceSurvivesTheProjectItDescribed(t *testing.T) {
	root := harnessProject(t, map[string]string{
		"CLAUDE.md":                 "# guidance\n",
		".claude/agents/explore.md": "explore\n",
	})
	baseline := capturedStart(t, "before", root)
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("# rewritten\n"), 0o600); err != nil {
		t.Fatalf("rewrite instructions: %v", err)
	}
	candidate := capturedStart(t, "after", root)

	before := harnessSection(t, sides(baseline, candidate))

	// The project is rewritten past recognition and then removed outright.
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("# a third thing\n"), 0o600); err != nil {
		t.Fatalf("rewrite instructions: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude", "agents", "explore.md"),
		[]byte("rewritten\n"), 0o600); err != nil {
		t.Fatalf("rewrite definition: %v", err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatalf("remove project: %v", err)
	}

	if after := harnessSection(t, sides(baseline, candidate)); after != before {
		t.Errorf("the comparison changed with the machine it was read on:\nbefore:\n%s\nafter:\n%s",
			before, after)
	}
	// And it still says what it said, rather than degrading to silence.
	wantComponent(t, before, "CLAUDE.md", "different bytes")
	wantComponent(t, before, "explore.md", "same bytes")
}

// A capture recorded somewhere that has no project at all still reports what
// it recorded. Nothing is filled in from the machine reading the log, which
// would describe today and attach it to work recorded earlier.
func TestCompareReconstructsNoProvenanceFromTheReadingMachine(t *testing.T) {
	root := harnessProject(t, map[string]string{"CLAUDE.md": "# present day\n"})
	// A start exactly as an older Axiom wrote it: a real working directory,
	// with configuration sitting in it now, and no provenance on the record.
	historical := seed(t, event.Event{
		SchemaVersion: event.SchemaVersion,
		Agent:         "claude-code",
		Type:          event.TypeSessionStart,
		Timestamp:     hookNow,
		SessionID:     "historical",
		Cwd:           root,
		Session:       &event.Session{Source: "startup"},
	})

	block := harnessSection(t, sides(historical, capturedStart(t, "recent", root)))

	if !strings.Contains(unwrapped(block), "the baseline recorded no harness provenance") {
		t.Errorf("a historical capture was not reported as holding none:\n%s", block)
	}
	if strings.Contains(block, "same bytes") {
		t.Errorf("provenance was reconstructed from the filesystem:\n%s", block)
	}
	// The working directory the record carries is not project identity, and
	// a comparison does not present it as any.
	if strings.Contains(block, root) {
		t.Errorf("the recorded working directory reached the block:\n%s", block)
	}
}

// The section is written even where there is nothing to compare. A block that
// disappeared would leave the reader to supply the missing conclusion, and the
// one they would supply is that the two captures matched.
func TestTheProvenanceSectionIsAlwaysWritten(t *testing.T) {
	t.Parallel()

	// Two ordinary captures, neither of which recorded any provenance.
	out := compareOutput(t, sides(
		delegationCapture(t, sessionA, 2),
		delegationCapture(t, sessionB, 3),
	))

	if !strings.Contains(out, "\nObserved harness provenance\n") {
		t.Errorf("the section is missing where there was nothing to compare:\n%s", out)
	}
	if !strings.Contains(unwrapped(out), "neither capture recorded harness provenance") {
		t.Errorf("the empty state is not explained:\n%s", out)
	}
}

// The boundary this feature exists inside. Provenance sits above the recorded
// work, and nothing may pair the two.
func TestTheProvenanceSectionStatesItsBoundary(t *testing.T) {
	root := harnessProject(t, map[string]string{"CLAUDE.md": "# guidance\n"})
	baseline := capturedStart(t, "before", root)
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("# after\n"), 0o600); err != nil {
		t.Fatalf("rewrite instructions: %v", err)
	}
	out := compareOutput(t, sides(baseline, capturedStart(t, "after", root)))

	block := compareSection(t, out, "Observed harness provenance")
	if !strings.Contains(unwrapped(block), "Nothing compared below is attributed to anything here.") {
		t.Errorf("the block does not state its boundary:\n%s", block)
	}
	// Two captures need not be two recordings of one project, and matching
	// components would look exactly like a project that did not change.
	// Axiom records no project identity, and the report says so rather than
	// leaving the reader to assume the more useful reading.
	limits := unwrapped(compareSection(t, out, "What this compares"))
	if !strings.Contains(limits, "Axiom records no project identity") {
		t.Errorf("the report does not say that project identity is unrecorded:\n%s", limits)
	}
	if !strings.Contains(limits, "not evidence that either agent loaded them") {
		t.Errorf("the report does not bound what a matching component establishes:\n%s", limits)
	}

	// It comes before the work, because a reader has to meet the limits of
	// an observation before meeting anything derived from the capture.
	provenance := strings.Index(out, "\nObserved harness provenance\n")
	work := strings.Index(out, "\nRecorded work by shape\n")
	if provenance < 0 || work < 0 || provenance > work {
		t.Errorf("provenance is not placed above the recorded work:\n%s", out)
	}
	// No count of any kind sits beside a component. A number here would be
	// read against the numbers below it.
	for line := range strings.SplitSeq(block, "\n") {
		if !strings.HasPrefix(line, changeIndent) {
			continue
		}
		if strings.ContainsAny(strings.TrimSpace(line), "0123456789") {
			t.Errorf("a component line carries a number:\n%s", line)
		}
	}
}

// The vocabulary that would turn evidence about two observations into a claim
// about what one of them did to a capture.
func TestProvenanceUsesNoCausalOrStatisticalVocabulary(t *testing.T) {
	root := harnessProject(t, map[string]string{
		"CLAUDE.md":                 "# guidance\n",
		".claude/agents/explore.md": "explore\n",
	})
	baseline := capturedStart(t, "before", root)
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("# after\n"), 0o600); err != nil {
		t.Fatalf("rewrite instructions: %v", err)
	}
	if err := os.Remove(filepath.Join(root, ".claude", "agents", "explore.md")); err != nil {
		t.Fatalf("remove definition: %v", err)
	}
	candidate := capturedStart(t, "after", root)
	blind := blindCapture(t, "blind")

	// Every shape the section takes: compared, and each way it can refuse
	// to compare.
	//
	// The check is scoped to this section rather than to the whole report,
	// because it is a ban on substrings and the rest of the report uses two
	// of these words to deny exactly what they name — a capture does not
	// "explain" another, and nothing below is "attributed" to provenance. A
	// negated word is the point being made, and a report-wide ban would
	// forbid the sentence that makes it.
	blocks := []string{
		harnessSection(t, sides(baseline, candidate)),
		harnessSection(t, sides(blind, candidate)),
		harnessSection(t, sides(blind, blind)),
	}
	forbidden := []string{
		"caused", "because of", "resulted in", "responsible for", "led to", "due to",
		"improve", "degrad", "effect", "impact", "correlat", "associat", "explains",
	}
	for _, block := range blocks {
		lower := strings.ToLower(block)
		for _, word := range forbidden {
			if strings.Contains(lower, word) {
				t.Errorf("the provenance block contains %q:\n%s", word, block)
			}
		}
	}
}

// The provenance block is an addition and not a rewrite. Every number the
// comparison reported before it existed reports the same thing now.
func TestProvenanceLeavesTheRestOfTheComparisonAlone(t *testing.T) {
	t.Parallel()

	baseline := delegationCapture(t, sessionA, 4)
	candidate := delegationCapture(t, sessionB, 9)

	out := compareOutput(t, sides(baseline, candidate))

	shape := compareSection(t, out, "Recorded work by shape")
	rowOf(t, shape, "Shell").wants(t, "Shell", "4", "9", "+5")
	rowOf(t, shape, "Whole-file reads").wants(t, "Whole-file reads", "4", "4", "same")
	rowOf(t, shape, "Subagent launches").wants(t, "Subagent launches", "2", "2", "same")

	capture := compareSection(t, out, "Capture shape")
	rowOf(t, capture, "Context epochs").wants(t, "Context epochs", "1", "1", "")
	rowOf(t, capture, "Recorded tool calls").wants(t, "Recorded tool calls", "10", "15", "")

	rowOf(t, compareSection(t, out, "Delegation"), "Relations established").
		wants(t, "Relations established", "2", "2", "same")
	rowOf(t, compareSection(t, out, "Read again in a later context epoch"), "Context epochs").
		wants(t, "Context epochs", "1", "1", "same")
}

// Refusals are unchanged: a directory that is not one capture is refused
// before anything is read, and provenance does not make it comparable.
func TestProvenanceDoesNotSoftenARefusal(t *testing.T) {
	root := harnessProject(t, map[string]string{"CLAUDE.md": "# guidance\n"})

	multi := t.TempDir()
	recordStart(t, multi, "one", root)
	recordStart(t, multi, "two", root)

	msg := compareRefusal(t, sides(multi, capturedStart(t, "single", root)))

	if !strings.Contains(msg, "more than one session") {
		t.Errorf("a directory holding two identities was not refused:\n%s", msg)
	}
	// Selecting one of them resolves it, and the comparison then reports the
	// provenance of the session that was selected.
	opts := sides(multi, capturedStart(t, "single", root))
	opts.baseline.session = "two"
	wantComponent(t, harnessSection(t, opts), "CLAUDE.md", "same bytes")
}
