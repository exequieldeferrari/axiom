package cli

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/event"
)

// fileCall is one file operation the agent identified, so that a measurement
// can be attached to this exact occurrence.
func fileCall(session, turn, invocation, path, access string, when time.Time, duration int64) event.Event {
	name := map[string]string{
		event.AccessRead:  "Read",
		event.AccessWrite: "Write",
		event.AccessEdit:  "Edit",
	}[access]

	return event.Event{
		SchemaVersion: event.SchemaVersion,
		Agent:         "claude-code",
		Type:          event.TypeToolCall,
		Timestamp:     when,
		SessionID:     session,
		TurnID:        turn,
		Tool: &event.ToolCall{
			Name:         name,
			InvocationID: invocation,
			Outcome:      event.OutcomeSuccess,
			DurationMS:   ms(duration),
			Metadata: &event.ToolMetadata{
				File: &event.FileOp{Path: path, Access: access},
			},
		},
	}
}

func rangedCall(session, turn, invocation, path string, offset int, when time.Time) event.Event {
	ev := fileCall(session, turn, invocation, path, event.AccessRead, when, 3)
	ev.Tool.Metadata.File.Offset = &offset
	return ev
}

func failedCall(session, turn, invocation, path, access string, when time.Time) event.Event {
	ev := fileCall(session, turn, invocation, path, access, when, 2)
	ev.Tool.Outcome = event.OutcomeFailure
	return ev
}

func searchCall(session, turn string, when time.Time) event.Event {
	ev := shellEvent(session, "digest", when, 4)
	ev.TurnID = turn
	ev.Tool.Name = "Grep"
	ev.Tool.Metadata = &event.ToolMetadata{
		Search: &event.SearchOp{Kind: event.SearchContent, PatternDigest: "pattern", Root: "/repo/internal"},
	}
	return ev
}

func subagentCall(session, turn string, when time.Time) event.Event {
	ev := shellEvent(session, "digest", when, 4)
	ev.TurnID = turn
	ev.Tool.Name = "Task"
	ev.Tool.Metadata = &event.ToolMetadata{Subagent: &event.SubagentOp{Type: "Explore"}}
	return ev
}

// opaqueCall is a tool the metadata allowlist does not cover, so Axiom cannot
// say what it did.
func opaqueCall(session, turn, name string, when time.Time) event.Event {
	ev := shellEvent(session, "digest", when, 4)
	ev.TurnID = turn
	ev.Tool.Name = name
	ev.Tool.Metadata = nil
	return ev
}

// wellCovered is one turn of ordinary work: a file read and edited, another
// read, a search, a shell command, and a call Axiom cannot interpret.
func wellCovered(t *testing.T, usage ...event.Usage) string {
	t.Helper()

	dir := seed(t,
		fileCall("session-1", "turn-1", "call-1", "/repo/internal/auth/validate.go", event.AccessRead, at(0), 12),
		fileCall("session-1", "turn-1", "call-2", "/repo/internal/auth/validate.go", event.AccessEdit, at(time.Second), 30),
		fileCall("session-1", "turn-1", "call-3", "/repo/internal/http/middleware.go", event.AccessRead, at(2*time.Second), 8),
		searchCall("session-1", "turn-1", at(3*time.Second)),
		shellEvent("session-1", "digest-1", at(4*time.Second), 900),
		opaqueCall("session-1", "turn-1", "mcp__docs__lookup", at(5*time.Second)),
	)
	if len(usage) > 0 {
		seedUsage(t, dir, usage...)
	}
	return dir
}

// section returns the report between one heading and the next, so a test can
// assert what a section says without matching text from another.
func section(t *testing.T, out, heading string) string {
	t.Helper()

	_, rest, ok := strings.Cut(out, "\n"+heading+"\n")
	if !ok {
		t.Fatalf("no %q section:\n%s", heading, out)
	}
	for _, next := range []string{"\nObserved operations\n", "\nWork by path", "\nFindings\n"} {
		if before, _, found := strings.Cut(rest, next); found {
			rest = before
		}
	}
	return rest
}

func TestProfileReportsWhatTheExecutionConsistedOf(t *testing.T) {
	t.Parallel()

	out := profileOutput(t, wellCovered(t))
	operations := section(t, out, "Observed operations")

	for _, want := range []string{
		"File              3   read, written or edited; attributed by path below",
		"Search            1   pattern recorded, no path named",
		"Shell             1   effects not observable; never attributed",
		"Unrecognized      1   Axiom cannot say what these did",
	} {
		if !strings.Contains(operations, want) {
			t.Errorf("the composition is missing %q:\n%s", want, operations)
		}
	}
	if strings.Contains(operations, "Subagent") {
		t.Errorf("a shape with nothing observed was printed:\n%s", operations)
	}
	rejectForbidden(t, out)
}

// The buckets have to add up to the calls above them, or a reader cannot tell
// what the profile left out.
func TestProfileAccountsForEveryObservedCall(t *testing.T) {
	t.Parallel()

	out := profileOutput(t, wellCovered(t))

	if !strings.Contains(out, "Tool calls          6") {
		t.Fatalf("the tool call count is missing:\n%s", out)
	}
	if !strings.Contains(flat(out), "The lines above describe the 3 of 6 observed tool calls that named a path.") {
		t.Errorf("the path work is not reconciled with the observed calls:\n%s", out)
	}
}

func TestProfileAttributesWorkToPaths(t *testing.T) {
	t.Parallel()

	out := profileOutput(t, wellCovered(t))
	work := section(t, out, "Work by path, under /repo/internal")

	for _, want := range []string{
		"auth/validate.go",
		"1 read, 1 modification, 1 turn, 42ms",
		"http/middleware.go",
		"1 read, 1 turn, 8ms",
	} {
		if !strings.Contains(work, want) {
			t.Errorf("the path work is missing %q:\n%s", want, work)
		}
	}
	if strings.Contains(work, "/repo/internal/auth/validate.go") {
		t.Errorf("the shared directory was repeated on every line:\n%s", work)
	}
}

// A ranged read acquires part of a file. Folding it into reads would say the
// file was read whole; dropping it would say the path was never read at all.
func TestProfileKeepsRangedReadsVisible(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		rangedCall("session-1", "turn-1", "call-1", "/repo/big.go", 0, at(0)),
		rangedCall("session-1", "turn-1", "call-2", "/repo/big.go", 400, at(time.Second)),
	)

	out := profileOutput(t, dir)

	if !strings.Contains(out, "2 ranged, 1 turn") {
		t.Errorf("ranged reads are not reported as their own work:\n%s", out)
	}
	if strings.Contains(out, "0 reads") {
		t.Errorf("a path read only in ranges was rendered as unread:\n%s", out)
	}
}

func TestProfileKeepsFailedOperationsVisible(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		fileCall("session-1", "turn-1", "call-1", "/repo/a.go", event.AccessRead, at(0), 5),
		failedCall("session-1", "turn-1", "call-2", "/repo/a.go", event.AccessEdit, at(time.Second)),
		failedCall("session-1", "turn-1", "call-3", "/repo/a.go", event.AccessRead, at(2*time.Second)),
	)

	out := profileOutput(t, dir)

	if !strings.Contains(out, "1 read, 2 failed, 1 turn") {
		t.Errorf("failed operations are not visible at the path:\n%s", out)
	}
	if !strings.Contains(out, "File              3") {
		t.Errorf("a failed operation went missing from the composition:\n%s", out)
	}
	// Every call named a path, so there is nothing left over to reconcile.
	if strings.Contains(flat(out), "observed tool calls that named a path") {
		t.Errorf("work was reported as unattributed where there is none:\n%s", out)
	}
}

// A record that does not say what became of a call must not be reported as a
// failure at the path. Nothing validates that field, so this is a state a log
// can hold.
func TestProfileDoesNotReportAnUnestablishedOutcomeAsAFailure(t *testing.T) {
	t.Parallel()

	unestablished := fileCall("session-1", "turn-1", "call-2", "/repo/a.go", event.AccessRead, at(time.Second), 4)
	unestablished.Tool.Outcome = ""
	dir := seed(t,
		fileCall("session-1", "turn-1", "call-1", "/repo/a.go", event.AccessRead, at(0), 4),
		unestablished,
	)

	out := profileOutput(t, dir)

	if strings.Contains(out, "1 failed") {
		t.Errorf("an outcome that was never established was reported as a failure:\n%s", out)
	}
	if !strings.Contains(out, "1 read, 1 turn") {
		t.Errorf("the read Axiom did observe is missing:\n%s", out)
	}
	if !strings.Contains(out, "Unrecognized      1   Axiom cannot say what these did") {
		t.Errorf("the uninterpretable call is not accounted for:\n%s", out)
	}
}

func TestProfileMeasuresWhatTheReadsOfAPathReturned(t *testing.T) {
	t.Parallel()

	out := profileOutput(t, wellCovered(t,
		measurement("session-1", "turn-1", "call-1", bytesOf(firstReadBytes)),
		measurement("session-1", "turn-1", "call-3", bytesOf(2048)),
	))
	work := section(t, out, "Work by path, under /repo/internal")

	if !strings.Contains(work, "7.5 KB read") {
		t.Errorf("the measured read is missing:\n%s", work)
	}
	if !strings.Contains(work, "2.0 KB read") {
		t.Errorf("the second path's measurement is missing:\n%s", work)
	}
	if !strings.Contains(flat(out), "Read bytes were measured for 2 of 2 reads.") {
		t.Errorf("the measurement coverage is not stated:\n%s", out)
	}
	rejectOverlongLines(t, out)
}

// An edit's result is the agent confirming its own change, so it is not part of
// what the reads of that path returned.
func TestProfileMeasuresOnlyReads(t *testing.T) {
	t.Parallel()

	out := profileOutput(t, wellCovered(t,
		measurement("session-1", "turn-1", "call-1", bytesOf(1024)),
		measurement("session-1", "turn-1", "call-2", bytesOf(500000)),
		measurement("session-1", "turn-1", "call-3", bytesOf(1024)),
	))

	if strings.Contains(out, "488.3 KB") {
		t.Errorf("an edit's result was counted as bytes a read returned:\n%s", out)
	}
	if !strings.Contains(flat(out), "measured for 2 of 2 reads") {
		t.Errorf("the reads were not the denominator:\n%s", out)
	}
}

// A path nothing read has no byte total to be missing, so it is not asked for
// one. Only a total Axiom could have had and does not is reported as unknown.
func TestProfileDoesNotAskAWrittenPathForReadBytes(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		fileCall("session-1", "turn-1", "call-1", "/repo/a.go", event.AccessRead, at(0), 4),
		fileCall("session-1", "turn-1", "call-2", "/repo/new.go", event.AccessWrite, at(time.Second), 6),
	)
	seedUsage(t, dir, measurement("session-1", "turn-1", "call-1", bytesOf(2048)))

	out := profileOutput(t, dir)

	for line := range strings.Lines(out) {
		if strings.Contains(line, "1 modification") && strings.Contains(line, "read bytes") {
			t.Errorf("a path nothing read was asked for read bytes: %q", strings.TrimSpace(line))
		}
	}
	if !strings.Contains(out, "2.0 KB read") {
		t.Errorf("the measured read is missing:\n%s", out)
	}
}

func TestProfileNamesTheOnlyReadItMeasured(t *testing.T) {
	t.Parallel()

	dir := seed(t, fileCall("session-1", "turn-1", "call-1", "/repo/a.go", event.AccessRead, at(0), 4))
	seedUsage(t, dir, measurement("session-1", "turn-1", "call-1", bytesOf(1024)))

	out := profileOutput(t, dir)

	if !strings.Contains(out, "1.0 KB read") {
		t.Errorf("the measured read is missing:\n%s", out)
	}
	if !strings.Contains(flat(out), "Read bytes were measured for the only read.") {
		t.Errorf("the coverage of one read is not stated in words that fit it:\n%s", out)
	}
}

// A path whose reads were not all measured says so, and never says zero.
func TestProfileWithholdsAnIncompleteByteTotal(t *testing.T) {
	t.Parallel()

	out := profileOutput(t, wellCovered(t,
		measurement("session-1", "turn-1", "call-3", bytesOf(2048)),
	))
	work := section(t, out, "Work by path, under /repo/internal")

	if !strings.Contains(work, "read bytes —") {
		t.Errorf("an unmeasured path did not say so:\n%s", work)
	}
	if strings.Contains(work, "0 B read") {
		t.Errorf("an unmeasured total was rendered as zero:\n%s", work)
	}
	if !strings.Contains(flat(out), "Read bytes were measured for 1 of 2 reads.") {
		t.Errorf("the partial coverage is not stated:\n%s", out)
	}
	if !strings.Contains(flat(out), "A dash means Axiom recorded nothing complete; it never means zero.") {
		t.Errorf("the dash is not explained:\n%s", out)
	}
}

// Telemetry exists only while a receiver is running, so its absence is the
// ordinary case and must not read as an absence of bytes.
func TestProfileExplainsUnmeasuredReadsOnce(t *testing.T) {
	t.Parallel()

	out := profileOutput(t, wellCovered(t))
	work := section(t, out, "Work by path, under /repo/internal")

	if strings.Contains(work, "read bytes —") {
		t.Errorf("every line carried a byte total that could never exist:\n%s", work)
	}
	if !strings.Contains(flat(out), "No read was measured, so no path reports read bytes.") {
		t.Errorf("the absence of measurement is not explained:\n%s", out)
	}
	if strings.Contains(out, "0 B") {
		t.Errorf("an absent measurement was rendered as zero:\n%s", out)
	}
}

func TestProfileWithholdsATimeItCannotTotal(t *testing.T) {
	t.Parallel()

	untimed := fileCall("session-1", "turn-1", "call-2", "/repo/a.go", event.AccessRead, at(time.Second), 0)
	untimed.Tool.DurationMS = nil
	dir := seed(t,
		fileCall("session-1", "turn-1", "call-1", "/repo/a.go", event.AccessRead, at(0), 9),
		untimed,
	)

	out := profileOutput(t, dir)

	if !strings.Contains(out, "time —") {
		t.Errorf("a partial time was reported as complete:\n%s", out)
	}
	if strings.Contains(out, "9ms") {
		t.Errorf("a partial sum was printed:\n%s", out)
	}
}

func TestProfileWithholdsTurnsItCannotCount(t *testing.T) {
	t.Parallel()

	anonymous := fileCall("session-1", "", "call-2", "/repo/a.go", event.AccessRead, at(time.Second), 4)
	dir := seed(t,
		fileCall("session-1", "turn-1", "call-1", "/repo/a.go", event.AccessRead, at(0), 4),
		anonymous,
	)

	out := profileOutput(t, dir)

	if !strings.Contains(out, "turns —") {
		t.Errorf("turns were counted from part of the work:\n%s", out)
	}
}

// An execution Axiom mostly cannot attribute has to say so, or the little it
// can attribute reads as the whole of it.
func TestProfileReportsAnExecutionItCannotMostlyAttribute(t *testing.T) {
	t.Parallel()

	events := []event.Event{
		fileCall("session-1", "turn-1", "call-1", "/repo/Makefile", event.AccessRead, at(0), 5),
	}
	for i := range 8 {
		events = append(events, shellEvent("session-1", fmt.Sprintf("digest-%d", i), at(time.Duration(i+1)*time.Second), 700))
	}
	for i := range 3 {
		events = append(events, opaqueCall("session-1", "turn-1", "mcp__deploy__run", at(time.Duration(i+10)*time.Second)))
	}

	out := profileOutput(t, seed(t, events...))
	flattened := flat(out)

	if !strings.Contains(flattened, "The lines above describe the 1 of 12 observed tool calls that named a path.") {
		t.Errorf("the profile does not say how little of the execution it describes:\n%s", out)
	}
	if !strings.Contains(out, "Shell             8   effects not observable; never attributed") {
		t.Errorf("unattributable shell work is not accounted for:\n%s", out)
	}
	if !strings.Contains(flattened, "Only a file operation names a path") {
		t.Errorf("the limits of attribution are not explained:\n%s", out)
	}
}

func TestProfileWithNothingToAttribute(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		shellEvent("session-1", "digest-1", at(0), 900),
		shellEvent("session-1", "digest-2", at(time.Second), 400),
		searchCall("session-1", "turn-1", at(2*time.Second)),
		subagentCall("session-1", "turn-1", at(3*time.Second)),
		opaqueCall("session-1", "turn-1", "NotebookEdit", at(4*time.Second)),
	)

	out := profileOutput(t, dir)
	flattened := flat(out)

	if !strings.Contains(flattened, "No operation named a path, so there is nothing to attribute.") {
		t.Errorf("an unattributable execution is not explained:\n%s", out)
	}
	if !strings.Contains(flattened, "That is not a claim that no file changed: 2 shell commands, 1 search, 1 subagent call and 1 unrecognized call were observed, and none of them names a path.") {
		t.Errorf("the unattributable work is not named:\n%s", out)
	}
	rejectForbidden(t, out)
}

// The work Axiom cannot place is named as a list, and a list of one or two
// shapes has to read as a sentence rather than as a rendered array.
func TestProfileNamesUnattributableWorkAsASentence(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		events []event.Event
		want   string
	}{
		"one shape": {
			events: []event.Event{
				shellEvent("session-1", "digest-1", at(0), 300),
				shellEvent("session-1", "digest-2", at(time.Second), 300),
			},
			want: "2 shell commands were observed",
		},
		"two shapes": {
			events: []event.Event{
				shellEvent("session-1", "digest-1", at(0), 300),
				searchCall("session-1", "turn-1", at(time.Second)),
			},
			want: "1 shell command and 1 search were observed",
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			out := profileOutput(t, seed(t, c.events...))

			if !strings.Contains(flat(out), c.want) {
				t.Errorf("output does not say %q:\n%s", c.want, out)
			}
		})
	}
}

// The sentence naming unattributable work is as long as the log makes it, so it
// is the one explanation in the report that has to be wrapped rather than
// written on its lines.
func TestProfileWrapsTheWorkItCannotAttribute(t *testing.T) {
	t.Parallel()

	var events []event.Event
	for i := range 40 {
		when := at(time.Duration(i) * time.Second)
		events = append(events,
			shellEvent("session-1", fmt.Sprintf("digest-%d", i), when, 300),
			searchCall("session-1", "turn-1", when.Add(time.Millisecond)),
			subagentCall("session-1", "turn-1", when.Add(2*time.Millisecond)),
			opaqueCall("session-1", "turn-1", "mcp__db__query", when.Add(3*time.Millisecond)),
		)
	}

	out := profileOutput(t, seed(t, events...))

	if !strings.Contains(flat(out), "40 shell commands, 40 searches, 40 subagent calls and 40 unrecognized calls were observed") {
		t.Errorf("the unattributable work is not named:\n%s", out)
	}
	rejectOverlongLines(t, out)
}

// rejectOverlongLines checks the profile's own lines against the width the
// report is written to. Paths are printed as the agent named them and can be
// longer than any width; everything Axiom writes itself cannot.
func rejectOverlongLines(t *testing.T, out string) {
	t.Helper()

	profile, _, _ := strings.Cut(out, "\nFindings\n")
	for line := range strings.Lines(profile) {
		text := strings.TrimRight(line, "\n")
		if strings.HasPrefix(text, "/") || strings.HasPrefix(text, "  /") {
			continue
		}
		if got := len(text); got > reportWidth {
			t.Errorf("a line runs to %d characters, past the report's %d:\n%s", got, reportWidth, text)
		}
	}
}

func TestProfileWithoutAnyToolCall(t *testing.T) {
	t.Parallel()

	dir := seed(t, event.Event{
		SchemaVersion: event.SchemaVersion,
		Agent:         "claude-code",
		Type:          event.TypeSessionStart,
		Timestamp:     at(0),
		SessionID:     "session-1",
		Session:       &event.Session{Source: "startup"},
	})

	out := profileOutput(t, dir)

	if !strings.Contains(out, "No tool call was recorded.") {
		t.Errorf("an empty execution is not explained:\n%s", out)
	}
	if strings.Contains(out, "Work by path") {
		t.Errorf("an empty path section was rendered:\n%s", out)
	}
}

// The report shows the busiest paths and accounts for the rest, counting them
// exactly as the visible lines are counted.
func TestProfileAccountsForThePathsItDoesNotShow(t *testing.T) {
	t.Parallel()

	var events []event.Event
	for i := range 13 {
		path := fmt.Sprintf("/repo/pkg%02d/file.go", i)
		when := at(time.Duration(i) * time.Second)
		events = append(events, fileCall("session-1", "turn-1", fmt.Sprintf("call-%d", i), path, event.AccessRead, when, 3))
		if i < 10 {
			// The ten busiest paths have two operations each, so the three
			// omitted ones are the single-operation paths.
			events = append(events, failedCall("session-1", "turn-1", fmt.Sprintf("fail-%d", i), path, event.AccessEdit, when.Add(time.Millisecond)))
		}
	}

	out := profileOutput(t, seed(t, events...))

	rejectOverlongLines(t, out)
	if !strings.Contains(out, "and 3 more paths (3 operations)") {
		t.Errorf("the omitted paths are not accounted for:\n%s", out)
	}
	if strings.Contains(out, "pkg12") {
		t.Errorf("more paths were shown than the report limits itself to:\n%s", out)
	}
	if !strings.Contains(out, "pkg00/file.go") {
		t.Errorf("the busiest paths are missing:\n%s", out)
	}
}

// Trimming a shared directory is display only. With nothing in common there is
// nothing to trim, and the paths are printed as the agent named them.
func TestProfilePrintsWholePathsWhenTheyShareNoDirectory(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		fileCall("session-1", "turn-1", "call-1", "/repo/a.go", event.AccessRead, at(0), 3),
		fileCall("session-1", "turn-1", "call-2", "/etc/hosts", event.AccessRead, at(time.Second), 3),
	)

	out := profileOutput(t, dir)

	if strings.Contains(out, "Work by path, under") {
		t.Errorf("a shared directory was claimed where there is none:\n%s", out)
	}
	for _, want := range []string{"/repo/a.go", "/etc/hosts"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

// Work at one path by two agents happened at that path. Reporting it together
// is a sum of operations and not a claim that any of it repeated.
func TestProfileCountsNestedAgentWorkAtThePath(t *testing.T) {
	t.Parallel()

	nested := fileCall("session-1", "turn-1", "call-2", "/repo/a.go", event.AccessRead, at(time.Second), 4)
	nested.SubagentID = "agent-1"
	dir := seed(t,
		fileCall("session-1", "turn-1", "call-1", "/repo/a.go", event.AccessRead, at(0), 4),
		nested,
	)

	out := profileOutput(t, dir)

	if !strings.Contains(out, "2 reads, 1 turn") {
		t.Errorf("nested work was not counted at the path:\n%s", out)
	}
	if strings.Contains(out, "Repeated file read") {
		t.Errorf("work in two contexts was reported as repetition:\n%s", out)
	}
}

// The profile describes work; the findings judge it. Neither may be mistaken
// for the other.
func TestProfileAndFindingsStayApart(t *testing.T) {
	t.Parallel()

	dir := seed(t,
		readCall("session-1", "turn-1", "call-1", "/repo/notes.txt", at(0)),
		readCall("session-1", "turn-1", "call-2", "/repo/notes.txt", at(time.Second)),
	)

	out := profileOutput(t, dir)
	work := section(t, out, "Work by path")

	if !strings.Contains(work, "2 reads, 1 turn") {
		t.Errorf("the work is not described:\n%s", work)
	}
	if strings.Contains(work, "redundant") || strings.Contains(work, "Repeated") {
		t.Errorf("the profile judged the work it described:\n%s", work)
	}
	if !strings.Contains(out, "Repeated file read") {
		t.Errorf("the finding is missing:\n%s", out)
	}
}
