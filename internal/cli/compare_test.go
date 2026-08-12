package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/store"
)

const (
	sessionA = "5d1a2b3c-4e5f-4a6b-8c9d-0e1f2a3b4c5d"
	sessionB = "7f2e1a3b-6c5d-4e3f-9a8b-1c2d3e4f5a6b"
	// A digest is recorded in place of a command, and must not reach a
	// comparison either: it names one exact string in one capture.
	digestA = "9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a0b9c8d7e6f5a4b3c2d1e0f9a8b"
)

// compareOutput reports what a comparison of two captures says.
func compareOutput(t *testing.T, opts compareOptions) string {
	t.Helper()

	var out bytes.Buffer
	if err := compareCaptures(opts, &out); err != nil {
		t.Fatalf("compareCaptures: %v", err)
	}
	return out.String()
}

// compareRefusal reports why a comparison refused.
func compareRefusal(t *testing.T, opts compareOptions) string {
	t.Helper()

	var out bytes.Buffer
	err := compareCaptures(opts, &out)
	if err == nil {
		t.Fatalf("comparison succeeded, want a refusal:\n%s", out.String())
	}
	if !IsUsage(err) {
		t.Fatalf("error = %v, want a usage error the caller can act on", err)
	}
	if out.Len() != 0 {
		t.Errorf("a refused comparison wrote a report:\n%s", out.String())
	}
	return err.Error()
}

func callEvent(session, turn, subagent string, when time.Time, tool event.ToolCall) event.Event {
	return event.Event{
		SchemaVersion: event.SchemaVersion,
		Agent:         "claude-code",
		Type:          event.TypeToolCall,
		Timestamp:     when,
		SessionID:     session,
		TurnID:        turn,
		SubagentID:    subagent,
		Tool:          &tool,
	}
}

func wholeRead(path string) event.ToolCall {
	return event.ToolCall{
		Name: "Read", Outcome: event.OutcomeSuccess, DurationMS: ms(5),
		Metadata: &event.ToolMetadata{
			File: &event.FileOp{Path: path, Access: event.AccessRead},
		},
	}
}

func launchCall(invocation, agent string, outcome event.Outcome) event.ToolCall {
	t := event.ToolCall{
		Name: "Task", InvocationID: invocation, Outcome: outcome, DurationMS: ms(800),
		Metadata: &event.ToolMetadata{Subagent: &event.SubagentOp{Type: "explore"}},
	}
	if agent != "" {
		t.Result = &event.ToolResult{Subagent: &event.SubagentResult{AgentID: agent}}
	}
	return t
}

// delegationCapture is the shape both sides of most tests are built from: one
// session that launches two agents and reads the files they read.
func delegationCapture(t *testing.T, session string, shell int) string {
	t.Helper()

	events := []event.Event{
		startEvent(session, "startup", at(0)),
		callEvent(session, "turn-1", "", at(time.Second), launchCall("inv-1", "agent-a", event.OutcomeSuccess)),
		callEvent(session, "turn-1", "", at(2*time.Second), launchCall("inv-2", "agent-b", event.OutcomeSuccess)),
		callEvent(session, "turn-1", "agent-a", at(3*time.Second), wholeRead("/repo/calc/calc.go")),
		callEvent(session, "turn-1", "agent-b", at(4*time.Second), wholeRead("/repo/strutil/strutil.go")),
		callEvent(session, "turn-1", "", at(5*time.Second), wholeRead("/repo/calc/calc.go")),
		callEvent(session, "turn-1", "", at(6*time.Second), wholeRead("/repo/strutil/strutil.go")),
	}
	for i := 0; i < shell; i++ {
		events = append(events, callEvent(session, "turn-1", "",
			at(time.Duration(7+i)*time.Second), event.ToolCall{
				Name: "Bash", Outcome: event.OutcomeSuccess, DurationMS: ms(90),
				Metadata: &event.ToolMetadata{Shell: &event.ShellOp{CommandDigest: digestA}},
			}))
	}
	return seed(t, events...)
}

func sides(baseline, candidate string) compareOptions {
	return compareOptions{
		baseline:  captureOptions{dir: baseline},
		candidate: captureOptions{dir: candidate},
	}
}

// 1. A capture compared with itself differs in nothing. Any dimension that
// moves here is not a property of the capture at all.
func TestCompareOfOneCaptureWithItselfShowsNoDifference(t *testing.T) {
	t.Parallel()

	dir := delegationCapture(t, sessionA, 4)

	out := compareOutput(t, sides(dir, dir))

	for _, unwanted := range []string{"+", "−", " -1", " -2"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("a capture differed from itself (%q):\n%s", unwanted, out)
		}
	}
	if got := strings.Count(out, "same"); got < 10 {
		t.Errorf("only %d dimensions reported as unchanged:\n%s", got, out)
	}
}

// 2. Two captures differing in known ways report exactly those differences,
// mechanically and in both directions.
func TestCompareReportsExactMechanicalDifferences(t *testing.T) {
	t.Parallel()

	baseline := delegationCapture(t, sessionA, 4)
	candidate := delegationCapture(t, sessionB, 9)

	out := compareOutput(t, sides(baseline, candidate))

	shape := compareSection(t, out, "Recorded work by shape")
	rowOf(t, shape, "Shell").wants(t, "Shell", "4", "9", "+5")
	rowOf(t, shape, "Whole-file reads").wants(t, "Whole-file reads", "4", "4", "same")
	rowOf(t, shape, "Subagent launches").wants(t, "Subagent launches", "2", "2", "same")
	rowOf(t, shape, "Uninterpreted").wants(t, "Uninterpreted", "0", "0", "same")

	delegated := compareSection(t, out, "Delegation")
	rowOf(t, delegated, "Relations established").wants(t, "Relations established", "2", "2", "same")

	scopes := compareSection(t, out, "Read across related agent scopes")
	rowOf(t, scopes, "Paths read in more than one related scope").
		wants(t, "Paths read in more than one related scope", "2", "2", "same")

	// The same pair the other way round reports the same difference with the
	// other sign, and never a magnitude alone.
	reversed := compareSection(t, compareOutput(t, sides(candidate, baseline)), "Recorded work by shape")
	rowOf(t, reversed, "Shell").wants(t, "Shell", "9", "4", "-5")
}

// 3. A directory holding more than one session identity is refused. Axiom
// cannot tell which of them is the capture, and picking one would answer a
// question the operator never asked.
func TestCompareRefusesACaptureHoldingMoreThanOneSession(t *testing.T) {
	t.Parallel()

	multi := seed(t,
		startEvent(sessionA, "startup", at(0)),
		callEvent(sessionA, "turn-1", "", at(time.Second), wholeRead("/repo/a.go")),
		startEvent(sessionB, "startup", at(2*time.Second)),
		callEvent(sessionB, "turn-1", "", at(3*time.Second), wholeRead("/repo/a.go")),
	)
	single := delegationCapture(t, sessionA, 1)

	msg := compareRefusal(t, sides(multi, single))

	for _, want := range []string{
		"more than one session",
		"cannot",
		sessionA,
		sessionB,
		"--baseline-session",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal is missing %q:\n%s", want, msg)
		}
	}

	// The same refusal names the other side's flag when the other side is the
	// one holding several sessions.
	other := compareRefusal(t, sides(single, multi))
	if !strings.Contains(other, "--candidate-session") {
		t.Errorf("refusal does not name the candidate's selector:\n%s", other)
	}
}

// 4. Selecting a session explicitly resolves the capture and the comparison
// proceeds over that session alone.
func TestCompareAcceptsAnExplicitSessionSelection(t *testing.T) {
	t.Parallel()

	multi := seed(t,
		startEvent(sessionA, "startup", at(0)),
		callEvent(sessionA, "turn-1", "", at(time.Second), wholeRead("/repo/a.go")),
		callEvent(sessionA, "turn-1", "", at(2*time.Second), wholeRead("/repo/b.go")),
		startEvent(sessionB, "startup", at(3*time.Second)),
		callEvent(sessionB, "turn-1", "", at(4*time.Second), wholeRead("/repo/a.go")),
	)
	single := seed(t,
		startEvent(sessionA, "startup", at(0)),
		callEvent(sessionA, "turn-1", "", at(time.Second), wholeRead("/repo/a.go")),
		callEvent(sessionA, "turn-1", "", at(2*time.Second), wholeRead("/repo/b.go")),
	)

	opts := sides(multi, single)
	opts.baseline.session = sessionB
	out := compareOutput(t, opts)

	// One read in the selected session, two in the other: the selection, and
	// not the directory, decided what was compared.
	if !strings.Contains(out, sessionB) {
		t.Errorf("the selected session is not named:\n%s", out)
	}
	// The unselected session of the baseline directory read two files. One
	// read on the baseline side is the selected session's alone.
	rowOf(t, compareSection(t, out, "Recorded work by shape"), "Whole-file reads").
		wants(t, "Whole-file reads", "1", "2", "+1")
}

// A selection matching nothing is a mistake worth naming, not an empty capture.
func TestCompareRefusesASelectionThatMatchesNothing(t *testing.T) {
	t.Parallel()

	dir := delegationCapture(t, sessionA, 1)

	opts := sides(dir, dir)
	opts.candidate.session = "no-such-session"
	msg := compareRefusal(t, opts)

	if !strings.Contains(msg, "no-such-session") {
		t.Errorf("refusal does not name the session that matched nothing:\n%s", msg)
	}
}

// 5. Records Axiom could not decode belong in the shape of a capture: they are
// the part of it that is missing, and a comparison read without them is read
// against an incomplete side without knowing it.
func TestCompareShowsSkippedRecordsInTheCaptureShape(t *testing.T) {
	t.Parallel()

	baseline := delegationCapture(t, sessionA, 1)
	candidate := delegationCapture(t, sessionB, 1)
	path := filepath.Join(candidate, store.EventsFile)
	log, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	damaged := append(log, []byte("{not json}\n{\"schema_version\":9}\n")...)
	if err := os.WriteFile(path, damaged, 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}

	out := compareOutput(t, sides(baseline, candidate))

	if !strings.Contains(out, "Records skipped") {
		t.Errorf("the capture shape does not report skipped records:\n%s", out)
	}
	if !strings.Contains(out, "2") {
		t.Errorf("the skipped records were not counted:\n%s", out)
	}
}

// 6. Telemetry presence is a fact about the directory. It is shown so that a
// reader knows what each side holds, and nothing measured is ever compared.
func TestCompareShowsUsagePresenceAndComparesNoConsumption(t *testing.T) {
	t.Parallel()

	baseline := delegationCapture(t, sessionA, 1)
	candidate := delegationCapture(t, sessionB, 1)
	cost := int64(4200)
	usage := event.Usage{
		SchemaVersion: event.SchemaVersion,
		Agent:         "claude-code",
		Kind:          event.UsageModelRequest,
		SessionID:     sessionA,
		TurnID:        "turn-1",
		Model:         "claude-opus-4",
		Tokens:        &event.Tokens{Input: 1200, Output: 300},
		CostMicros:    &cost,
	}
	s, err := store.OpenUsage(baseline)
	if err != nil {
		t.Fatalf("OpenUsage: %v", err)
	}
	if err := s.Append(usage); err != nil {
		t.Fatalf("Append: %v", err)
	}

	out := compareOutput(t, sides(baseline, candidate))

	if !strings.Contains(out, "Usage log") {
		t.Errorf("usage presence is not stated:\n%s", out)
	}
	if !strings.Contains(out, "present") || !strings.Contains(out, "absent") {
		t.Errorf("the two sides' telemetry states are not distinguished:\n%s", out)
	}
	for _, forbidden := range []string{
		"token", "Token", "cost", "Cost", "$", "model request", "Model request",
		"1,200", "1200", "4200", "requests",
	} {
		if strings.Contains(out, forbidden) {
			t.Errorf("consumption reached the comparison (%q):\n%s", forbidden, out)
		}
	}
}

// 7. A count of paths read across scopes means nothing without the delegation
// it was counted against: no launch at all and launches with nothing read
// across them are different observations that both report zero.
func TestCompareRendersCrossScopePathsWithTheirDenominators(t *testing.T) {
	t.Parallel()

	baseline := delegationCapture(t, sessionA, 1)
	// A capture that delegated nothing: zero paths, for a different reason.
	candidate := seed(t,
		startEvent(sessionB, "startup", at(0)),
		callEvent(sessionB, "turn-1", "", at(time.Second), wholeRead("/repo/calc/calc.go")),
	)

	out := compareOutput(t, sides(baseline, candidate))

	block := compareSection(t, out, "Read across related agent scopes")
	for _, want := range []string{
		"Paths read in more than one related scope",
		"Launches recorded",
		"Relations established",
		"Launching scopes",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the cross-scope block is missing %q:\n%s", want, block)
		}
	}
}

// 8. The same rule for reading across epochs: the crossing count is only
// interpretable beside the boundaries it was counted against, and an epoch
// that recorded no work is one of those boundaries.
func TestCompareRendersReacquisitionWithItsEpochContext(t *testing.T) {
	t.Parallel()

	baseline := seed(t,
		startEvent(sessionA, "startup", at(0)),
		callEvent(sessionA, "turn-1", "", at(time.Second), wholeRead("/repo/calc/calc.go")),
		startEvent(sessionA, "compact", at(2*time.Second)),
		callEvent(sessionA, "turn-2", "", at(3*time.Second), wholeRead("/repo/calc/calc.go")),
	)
	candidate := delegationCapture(t, sessionB, 1)

	out := compareOutput(t, sides(baseline, candidate))

	block := compareSection(t, out, "Read again in a later context epoch")
	for _, want := range []string{
		"Paths read in more than one epoch",
		"Sessions with more than one epoch",
		"Context epochs",
		"Epochs with recorded work",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the reacquisition block is missing %q:\n%s", want, block)
		}
	}
}

// 9. Shell and uninterpreted calls stay in the partition. They vary between
// recordings of one workload, and leaving them out to make the rest look
// steady would describe the remaining categories as the whole of the work.
func TestCompareKeepsShellAndUninterpretedInThePartition(t *testing.T) {
	t.Parallel()

	baseline := seed(t,
		startEvent(sessionA, "startup", at(0)),
		callEvent(sessionA, "turn-1", "", at(time.Second), wholeRead("/repo/a.go")),
		callEvent(sessionA, "turn-1", "", at(2*time.Second), event.ToolCall{
			Name: "Bash", Outcome: event.OutcomeSuccess,
			Metadata: &event.ToolMetadata{Shell: &event.ShellOp{CommandDigest: digestA}},
		}),
		callEvent(sessionA, "turn-1", "", at(3*time.Second), event.ToolCall{
			Name: "ScheduleWakeup", Outcome: event.OutcomeSuccess,
		}),
	)
	candidate := seed(t,
		startEvent(sessionB, "startup", at(0)),
		callEvent(sessionB, "turn-1", "", at(time.Second), wholeRead("/repo/a.go")),
	)

	out := compareOutput(t, sides(baseline, candidate))

	block := compareSection(t, out, "Recorded work by shape")
	for _, want := range []string{"Shell", "Uninterpreted"} {
		if !strings.Contains(block, want) {
			t.Errorf("the partition is missing %q:\n%s", want, block)
		}
	}
	// The partition has to account for every recorded call on both sides, or
	// the categories shown are not the whole of what was recorded.
	if !strings.Contains(out, "Recorded tool calls") {
		t.Errorf("the shape does not state how many calls the partition covers:\n%s", out)
	}
}

// 10. Writes, edits and launches carry outcomes. A launch reported failing
// started no nested agent, and folding it into a total would report a
// delegation that the record says did not happen.
func TestCompareDoesNotFlattenOutcomeStates(t *testing.T) {
	t.Parallel()

	baseline := seed(t,
		startEvent(sessionA, "startup", at(0)),
		callEvent(sessionA, "turn-1", "", at(time.Second), launchCall("inv-1", "agent-a", event.OutcomeSuccess)),
		callEvent(sessionA, "turn-1", "", at(2*time.Second), launchCall("inv-2", "", event.OutcomeFailure)),
		callEvent(sessionA, "turn-1", "", at(3*time.Second), event.ToolCall{
			Name: "Edit", Outcome: event.OutcomeFailure,
			Metadata: &event.ToolMetadata{
				File: &event.FileOp{Path: "/repo/a.go", Access: event.AccessEdit},
			},
		}),
		callEvent(sessionA, "turn-1", "", at(4*time.Second), event.ToolCall{
			Name: "Write", Outcome: "",
			Metadata: &event.ToolMetadata{
				File: &event.FileOp{Path: "/repo/b.go", Access: event.AccessWrite},
			},
		}),
	)
	candidate := delegationCapture(t, sessionB, 1)

	out := compareOutput(t, sides(baseline, candidate))

	block := compareSection(t, out, "Recorded work by shape")
	for _, want := range []string{
		"succeeded",
		"failed",
		"outcome not established",
	} {
		if strings.Count(block, want) < 3 {
			t.Errorf("%q does not appear under every category that carries outcomes:\n%s", want, block)
		}
	}
}

// 11 and 12. A path and a command digest each name one exact string in one
// capture. Two captures record their own, so neither can be compared, and
// printing one invites a reader to compare it by eye.
func TestCompareNamesNoRecordedPathOrDigest(t *testing.T) {
	t.Parallel()

	baseline := delegationCapture(t, sessionA, 2)
	candidate := delegationCapture(t, sessionB, 3)

	out := compareOutput(t, sides(baseline, candidate))

	for _, forbidden := range []string{
		"/repo/calc/calc.go",
		"/repo/strutil/strutil.go",
		"calc.go",
		"strutil",
		digestA,
		digestA[:12],
	} {
		if strings.Contains(out, forbidden) {
			t.Errorf("a recorded string reached the comparison (%q):\n%s", forbidden, out)
		}
	}
}

// 14. A difference is a difference. The vocabulary below would turn one into a
// verdict Axiom has no evidence for.
func TestCompareUsesNoJudgementVocabulary(t *testing.T) {
	t.Parallel()

	baseline := delegationCapture(t, sessionA, 2)
	candidate := delegationCapture(t, sessionB, 7)
	multi := seed(t,
		startEvent(sessionA, "startup", at(0)),
		callEvent(sessionA, "turn-1", "", at(time.Second), wholeRead("/repo/a.go")),
		startEvent(sessionB, "startup", at(2*time.Second)),
	)

	opts := sides(baseline, candidate)
	texts := []string{compareOutput(t, opts), compareRefusal(t, sides(multi, baseline))}

	forbidden := []string{
		"efficien", "waste", "wasted", "saved", "saving",
		"better", "worse", "improve", "regress", "optimal", "optimi",
		"caused", "because of", "%", "$", "€", "£",
	}
	for _, text := range texts {
		lower := strings.ToLower(text)
		for _, word := range forbidden {
			if strings.Contains(lower, word) {
				t.Errorf("comparison wording contains %q:\n%s", word, text)
			}
		}
	}
}

// A comparison states what it is not, in the output itself. A reader who never
// opens an ADR still has to be told that comparability was asserted and not
// established.
func TestCompareStatesItsEvidenceLimits(t *testing.T) {
	t.Parallel()

	out := compareOutput(t, sides(
		delegationCapture(t, sessionA, 1),
		delegationCapture(t, sessionB, 1),
	))

	for _, want := range []string{
		"same task",
		"asserted",
		"capture",
	} {
		if !strings.Contains(strings.ToLower(out), want) {
			t.Errorf("the report does not state its limits (%q):\n%s", want, out)
		}
	}
}

// row is one line of a comparison table, read back as its columns so that a
// test asserts on what the row says rather than on how it is padded.
type row struct {
	baseline, candidate, difference string
}

// rowOf finds one labelled row in a block of the report.
func rowOf(t *testing.T, block, label string) row {
	t.Helper()

	for line := range strings.SplitSeq(block, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, label) {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(trimmed, label))
		if len(fields) < 2 {
			t.Fatalf("row %q has no values: %q", label, line)
		}
		out := row{baseline: fields[0], candidate: fields[1]}
		if len(fields) > 2 {
			out.difference = fields[2]
		}
		return out
	}
	t.Fatalf("no row %q in:\n%s", label, block)
	return row{}
}

// wants asserts what one row says, in the order the report prints it.
func (r row) wants(t *testing.T, label, baseline, candidate, difference string) {
	t.Helper()

	if r.baseline != baseline || r.candidate != candidate || r.difference != difference {
		t.Errorf("%s = (%s, %s, %s), want (%s, %s, %s)",
			label, r.baseline, r.candidate, r.difference, baseline, candidate, difference)
	}
}

// compareHeadings are the blocks a comparison is made of, in the order they
// are written.
var compareHeadings = []string{
	"Capture shape",
	"Recorded work by shape",
	"Delegation",
	"Read across related agent scopes",
	"Read again in a later context epoch",
	"What this compares",
}

// section returns one block of the report, so that an assertion about a block
// cannot be satisfied by a word somewhere else in the output.
func compareSection(t *testing.T, out, heading string) string {
	t.Helper()

	start := strings.Index(out, "\n"+heading+"\n")
	if start < 0 {
		t.Fatalf("output has no %q section:\n%s", heading, out)
	}
	rest := out[start+len(heading)+2:]

	end := len(rest)
	for _, next := range compareHeadings {
		if i := strings.Index(rest, "\n"+next+"\n"); i >= 0 && i < end {
			end = i
		}
	}
	return rest[:end]
}
