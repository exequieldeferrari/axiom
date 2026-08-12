package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/store"
)

// An epoch that recorded nothing is still a boundary a later read could have
// crossed. Reporting epochs alone would overstate what the capture did, and
// reporting only the epochs with work would hide a boundary the agent reported.
func TestCompareCountsAnEmptyEpochAsAnEpoch(t *testing.T) {
	t.Parallel()

	// Two starts and one call: the first epoch holds the reset and nothing
	// else, and the second holds the work.
	empty := seed(t,
		startEvent(sessionA, "startup", at(0)),
		startEvent(sessionA, "compact", at(time.Second)),
		callEvent(sessionA, "turn-1", "", at(2*time.Second), wholeRead("/repo/a.go")),
	)
	single := seed(t,
		startEvent(sessionB, "startup", at(0)),
		callEvent(sessionB, "turn-1", "", at(time.Second), wholeRead("/repo/a.go")),
	)

	out := compareOutput(t, sides(empty, single))

	shape := compareSection(t, out, "Capture shape")
	rowOf(t, shape, "Context epochs").wants(t, "Context epochs", "2", "1", "")
	rowOf(t, shape, "Epochs with recorded work").wants(t, "Epochs with recorded work", "1", "1", "")
}

// A call that named no turn is still a recorded call. The partition covers
// every one of them, so work outside a turn cannot go missing from it.
func TestCompareCountsCallsThatNamedNoTurn(t *testing.T) {
	t.Parallel()

	unturned := seed(t,
		startEvent(sessionA, "startup", at(0)),
		callEvent(sessionA, "", "", at(time.Second), wholeRead("/repo/a.go")),
		callEvent(sessionA, "", "", at(2*time.Second), wholeRead("/repo/b.go")),
	)
	single := seed(t,
		startEvent(sessionB, "startup", at(0)),
		callEvent(sessionB, "turn-1", "", at(time.Second), wholeRead("/repo/a.go")),
	)

	out := compareOutput(t, sides(unturned, single))

	rowOf(t, compareSection(t, out, "Capture shape"), "Recorded tool calls").
		wants(t, "Recorded tool calls", "2", "1", "")
	rowOf(t, compareSection(t, out, "Recorded work by shape"), "Whole-file reads").
		wants(t, "Whole-file reads", "2", "1", "-1")
}

// A synchronous launch reaches the log after the work it created, because a
// hook sees a call only once it has returned. Both orders were captured, and
// neither may change what a comparison reports.
func TestCompareIsUnchangedByLaunchAndNestedCallOrder(t *testing.T) {
	t.Parallel()

	launch := callEvent(sessionA, "turn-1", "", at(3*time.Second),
		launchCall("inv-1", "agent-a", event.OutcomeSuccess))
	nested := callEvent(sessionA, "turn-1", "agent-a", at(time.Second), wholeRead("/repo/a.go"))
	root := callEvent(sessionA, "turn-1", "", at(2*time.Second), wholeRead("/repo/a.go"))

	launchFirst := seed(t, startEvent(sessionA, "startup", at(0)), launch, nested, root)
	nestedFirst := seed(t, startEvent(sessionA, "startup", at(0)), nested, root, launch)

	out := compareOutput(t, sides(launchFirst, nestedFirst))

	for _, block := range []string{"Delegation", "Read across related agent scopes"} {
		body := compareSection(t, out, block)
		if strings.Contains(body, "+") || strings.Contains(body, "-") {
			t.Errorf("record order changed %s:\n%s", block, body)
		}
	}
}

// Two relations established in a different order are the same two relations.
// Nothing in a comparison may depend on which was recorded first.
func TestCompareIsUnchangedByRelationOrder(t *testing.T) {
	t.Parallel()

	first := seed(t,
		startEvent(sessionA, "startup", at(0)),
		callEvent(sessionA, "turn-1", "", at(time.Second), launchCall("inv-1", "agent-a", event.OutcomeSuccess)),
		callEvent(sessionA, "turn-1", "", at(2*time.Second), launchCall("inv-2", "agent-b", event.OutcomeSuccess)),
	)
	reversed := seed(t,
		startEvent(sessionB, "startup", at(0)),
		callEvent(sessionB, "turn-1", "", at(time.Second), launchCall("inv-2", "agent-b", event.OutcomeSuccess)),
		callEvent(sessionB, "turn-1", "", at(2*time.Second), launchCall("inv-1", "agent-a", event.OutcomeSuccess)),
	)

	body := compareSection(t, compareOutput(t, sides(first, reversed)), "Delegation")

	if strings.Contains(body, "+") || strings.Contains(body, "-") {
		t.Errorf("the order relations were established in changed the comparison:\n%s", body)
	}
}

// Zero paths is not one observation. A capture that delegated nothing and a
// capture whose related scopes shared no reading both report zero, and the
// denominators beside the zero are what keeps them apart.
func TestCompareDistinguishesTheReasonsForZero(t *testing.T) {
	t.Parallel()

	// Delegated, related, and read nothing in common.
	shared := seed(t,
		startEvent(sessionA, "startup", at(0)),
		callEvent(sessionA, "turn-1", "", at(time.Second), launchCall("inv-1", "agent-a", event.OutcomeSuccess)),
		callEvent(sessionA, "turn-1", "agent-a", at(2*time.Second), wholeRead("/repo/a.go")),
		callEvent(sessionA, "turn-1", "", at(3*time.Second), wholeRead("/repo/b.go")),
	)
	// Delegated nothing at all.
	alone := seed(t,
		startEvent(sessionB, "startup", at(0)),
		callEvent(sessionB, "turn-1", "", at(time.Second), wholeRead("/repo/a.go")),
	)

	body := compareSection(t, compareOutput(t, sides(shared, alone)),
		"Read across related agent scopes")

	rowOf(t, body, "Paths read in more than one related scope").
		wants(t, "Paths read in more than one related scope", "0", "0", "same")
	rowOf(t, body, "Relations established").wants(t, "Relations established", "1", "0", "-1")
}

// The same rule where nothing was read across an epoch: a capture with one
// epoch had no boundary, which is not the same as having one and reading
// nothing across it.
func TestCompareDistinguishesTheReasonsForNoReacquisition(t *testing.T) {
	t.Parallel()

	// A boundary, with nothing read across it.
	boundary := seed(t,
		startEvent(sessionA, "startup", at(0)),
		callEvent(sessionA, "turn-1", "", at(time.Second), wholeRead("/repo/a.go")),
		startEvent(sessionA, "compact", at(2*time.Second)),
		callEvent(sessionA, "turn-2", "", at(3*time.Second), wholeRead("/repo/b.go")),
	)
	// No boundary at all.
	flat := seed(t,
		startEvent(sessionB, "startup", at(0)),
		callEvent(sessionB, "turn-1", "", at(time.Second), wholeRead("/repo/a.go")),
	)

	body := compareSection(t, compareOutput(t, sides(boundary, flat)),
		"Read again in a later context epoch")

	rowOf(t, body, "Paths read in more than one epoch").
		wants(t, "Paths read in more than one epoch", "0", "0", "same")
	rowOf(t, body, "Sessions with more than one epoch").
		wants(t, "Sessions with more than one epoch", "1", "0", "-1")
}

// A record from a newer schema, and one that cannot be decoded at all, are
// counted and shown. They are the part of a capture that is missing, and a
// difference read without them is read against an incomplete side.
func TestCompareShowsRecordsItCouldNotRead(t *testing.T) {
	t.Parallel()

	baseline := seed(t,
		startEvent(sessionA, "startup", at(0)),
		callEvent(sessionA, "turn-1", "", at(time.Second), wholeRead("/repo/a.go")),
	)
	candidate := seed(t,
		startEvent(sessionB, "startup", at(0)),
		callEvent(sessionB, "turn-1", "", at(time.Second), wholeRead("/repo/a.go")),
	)
	path := filepath.Join(candidate, store.EventsFile)
	log, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	// One malformed, one from a schema this version does not know, and one
	// truncated line.
	damaged := append(log, []byte("{not json}\n{\"schema_version\":9}\ntruncated")...)
	if err := os.WriteFile(path, damaged, 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}

	shape := compareSection(t, compareOutput(t, sides(baseline, candidate)), "Capture shape")

	rowOf(t, shape, "Records skipped").wants(t, "Records skipped", "0", "3", "")
}

// A capture whose records carry no session identity is not a capture. Nothing
// in it can be related, because every identity Axiom relates on is scoped to a
// session the records do not name.
func TestCompareRefusesACaptureWithNoSessionIdentity(t *testing.T) {
	t.Parallel()

	anonymous := seed(t, event.Event{
		SchemaVersion: event.SchemaVersion,
		Agent:         "claude-code",
		Type:          event.TypeToolCall,
		Timestamp:     at(0),
		Tool:          &event.ToolCall{Name: "Read", Outcome: event.OutcomeSuccess},
	})

	msg := compareRefusal(t, sides(anonymous, delegationCapture(t, sessionB, 1)))

	if !strings.Contains(msg, "no session identity") {
		t.Errorf("refusal does not explain what is missing:\n%s", msg)
	}
}

// A second identity can be carried by a record the timeline cannot place: a
// tool call with no call in it names a session and opens no epoch. The capture
// still holds two identities, and comparing it would quietly cover one of them.
func TestCompareRefusesASecondIdentityItCouldNotPlace(t *testing.T) {
	t.Parallel()

	twoIdentities := seed(t,
		startEvent(sessionA, "startup", at(0)),
		callEvent(sessionA, "turn-1", "", at(time.Second), wholeRead("/repo/a.go")),
		event.Event{
			SchemaVersion: event.SchemaVersion,
			Agent:         "claude-code",
			Type:          event.TypeToolCall,
			Timestamp:     at(2 * time.Second),
			SessionID:     sessionB,
		},
	)

	msg := compareRefusal(t, sides(twoIdentities, delegationCapture(t, sessionA, 1)))

	if !strings.Contains(msg, "more than one session") {
		t.Errorf("an unplaceable second identity was not refused:\n%s", msg)
	}
}

// A directory with no log at all is named as such, rather than compared as a
// capture that did nothing.
func TestCompareRefusesADirectoryWithNoLog(t *testing.T) {
	t.Parallel()

	msg := compareRefusal(t, sides(t.TempDir(), delegationCapture(t, sessionB, 1)))

	if !strings.Contains(msg, "no events are recorded") {
		t.Errorf("an empty directory was not explained:\n%s", msg)
	}
}

// A subagent's reads are the subagent's. They are set aside from the session's
// epochs by internal/reacquire, and a comparison must not quietly gain them.
func TestCompareDoesNotCountSubagentReadsAcrossEpochs(t *testing.T) {
	t.Parallel()

	nested := seed(t,
		startEvent(sessionA, "startup", at(0)),
		callEvent(sessionA, "turn-1", "agent-a", at(time.Second), wholeRead("/repo/a.go")),
		startEvent(sessionA, "compact", at(2*time.Second)),
		callEvent(sessionA, "turn-2", "agent-a", at(3*time.Second), wholeRead("/repo/a.go")),
	)
	flat := seed(t,
		startEvent(sessionB, "startup", at(0)),
		callEvent(sessionB, "turn-1", "", at(time.Second), wholeRead("/repo/a.go")),
	)

	body := compareSection(t, compareOutput(t, sides(nested, flat)),
		"Read again in a later context epoch")

	rowOf(t, body, "Paths read in more than one epoch").
		wants(t, "Paths read in more than one epoch", "0", "0", "same")
}

// The dispatcher reaches the command, and a comparison of two real directories
// runs end to end.
func TestRunDispatchesCompare(t *testing.T) {
	baseline := delegationCapture(t, sessionA, 1)
	candidate := delegationCapture(t, sessionB, 2)

	var stdout, stderr strings.Builder
	err := Run([]string{"axiom", "compare", baseline, candidate}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
	if !strings.Contains(stdout.String(), "Axiom Compare") {
		t.Errorf("the comparison did not reach stdout:\n%s", stdout.String())
	}
}

// A comparison reads two logs and writes to neither. Axiom is a passive
// observer, and a command that touched a capture would change the evidence it
// was asked to describe.
func TestCompareModifiesNeitherCapture(t *testing.T) {
	t.Parallel()

	baseline := delegationCapture(t, sessionA, 2)
	candidate := delegationCapture(t, sessionB, 3)

	before := map[string][]byte{}
	stamps := map[string]time.Time{}
	for _, dir := range []string{baseline, candidate} {
		path := filepath.Join(dir, store.EventsFile)
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read log: %v", err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		before[path], stamps[path] = content, info.ModTime()
	}

	compareOutput(t, sides(baseline, candidate))

	for path, content := range before {
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read log: %v", err)
		}
		if string(after) != string(content) {
			t.Errorf("comparing changed %s", path)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if !info.ModTime().Equal(stamps[path]) {
			t.Errorf("comparing touched %s", path)
		}
	}
	// Nor may it create the usage log it checked for the existence of.
	for _, dir := range []string{baseline, candidate} {
		if _, err := os.Stat(filepath.Join(dir, store.UsageFile)); err == nil {
			t.Errorf("comparing created a usage log in %s", dir)
		}
	}
}

// A nested agent that launches another agent made a call and created an agent
// on one record. It is one launch in the partition, one launch in the
// delegation block, and one call the nested agent made: the three describe the
// same record from different sides and none of them may double it.
func TestCompareCountsANestedLaunchOnce(t *testing.T) {
	t.Parallel()

	// agent-a is launched by the session scope and launches agent-b itself.
	nested := callEvent(sessionA, "turn-1", "agent-a", at(2*time.Second),
		launchCall("inv-2", "agent-b", event.OutcomeSuccess))
	deep := seed(t,
		startEvent(sessionA, "startup", at(0)),
		callEvent(sessionA, "turn-1", "", at(time.Second), launchCall("inv-1", "agent-a", event.OutcomeSuccess)),
		nested,
		callEvent(sessionA, "turn-1", "agent-b", at(3*time.Second), wholeRead("/repo/a.go")),
	)
	flat := seed(t,
		startEvent(sessionB, "startup", at(0)),
		callEvent(sessionB, "turn-1", "", at(time.Second), wholeRead("/repo/a.go")),
	)

	out := compareOutput(t, sides(deep, flat))

	shape := rowOf(t, compareSection(t, out, "Recorded work by shape"), "Subagent launches")
	shape.wants(t, "Subagent launches", "2", "0", "-2")
	delegated := rowOf(t, compareSection(t, out, "Delegation"), "Launches recorded")
	delegated.wants(t, "Launches recorded", "2", "0", "-2")
	// Two scopes launched, so the relation has two launchers rather than one.
	rowOf(t, compareSection(t, out, "Delegation"), "Launching scopes").
		wants(t, "Launching scopes", "2", "0", "-2")
}

// Telemetry that exists and cannot be read is neither present nor absent, and
// it is certainly not zero. The three states are held apart because a capture
// whose measurements were lost is not a capture that consumed nothing.
func TestCompareHoldsUnreadableTelemetryApartFromAbsent(t *testing.T) {
	t.Parallel()

	baseline := delegationCapture(t, sessionA, 1)
	candidate := delegationCapture(t, sessionB, 1)
	// A directory in the log's place opens and then fails to read, which
	// needs no permission change.
	if err := os.Mkdir(filepath.Join(baseline, store.UsageFile), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	shape := compareSection(t, compareOutput(t, sides(baseline, candidate)), "Capture shape")

	rowOf(t, shape, "Usage log").wants(t, "Usage log", "unreadable", "absent", "")
	if strings.Contains(shape, "Usage log                                          0") {
		t.Errorf("missing telemetry was reported as a measurement of zero:\n%s", shape)
	}
}

// The report is about captures. Naming a directory a run or an execution would
// claim a unit of work that nothing recorded establishes: one capture here held
// three CLI invocations under one session identity, and another held two
// sessions from one workload.
func TestCompareNamesNoExecution(t *testing.T) {
	t.Parallel()

	baseline := delegationCapture(t, sessionA, 1)
	candidate := delegationCapture(t, sessionB, 2)

	// The directories are the operator's own words, echoed back so that each
	// side can be told from the other. Only what Axiom wrote is checked here.
	out := compareOutput(t, sides(baseline, candidate))
	out = strings.ReplaceAll(out, baseline, "")
	out = strings.ReplaceAll(out, candidate, "")

	for _, word := range []string{"execution", "run of", "the run", "each run"} {
		if strings.Contains(strings.ToLower(out), word) {
			t.Errorf("the report calls a capture %q:\n%s", word, out)
		}
	}
	if !strings.Contains(out, "capture") {
		t.Errorf("the report never names what it compared:\n%s", out)
	}
}

// A selector is read wherever it was written. The standard parser stops at the
// first argument that is not a flag, which would ignore a selector written
// after the directories and compare a session nobody asked for.
func TestCompareReadsASelectorAfterTheDirectories(t *testing.T) {
	t.Parallel()

	multi := seed(t,
		startEvent(sessionA, "startup", at(0)),
		callEvent(sessionA, "turn-1", "", at(time.Second), wholeRead("/repo/a.go")),
		startEvent(sessionB, "startup", at(2*time.Second)),
		callEvent(sessionB, "turn-1", "", at(3*time.Second), wholeRead("/repo/a.go")),
	)
	single := delegationCapture(t, sessionA, 1)

	for _, args := range [][]string{
		{multi, single, "--baseline-session", sessionB},
		{"--baseline-session", sessionB, multi, single},
		{multi, "--baseline-session", sessionB, single},
	} {
		var out strings.Builder
		if err := runCompare(args, &out); err != nil {
			t.Errorf("runCompare(%q): %v", args, err)
			continue
		}
		if !strings.Contains(out.String(), sessionB) {
			t.Errorf("runCompare(%q) did not apply the selection:\n%s", args, out.String())
		}
	}
}

// Two directories are required, and a selector needs a value: both are
// mistakes the command can name before it reads anything.
func TestCompareRejectsMalformedInvocations(t *testing.T) {
	t.Parallel()

	cases := [][]string{
		nil,
		{"only-one"},
		{"a", "b", "c"},
		{"--baseline-session", "", "a", "b"},
	}
	for _, args := range cases {
		var out strings.Builder
		err := runCompare(args, &out)
		if !IsUsage(err) {
			t.Errorf("runCompare(%q) error = %v, want a usage error", args, err)
		}
		if out.Len() != 0 {
			t.Errorf("runCompare(%q) wrote a report:\n%s", args, out.String())
		}
	}
}
