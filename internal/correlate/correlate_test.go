package correlate_test

import (
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/correlate"
	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/profiler"
)

func bytesOf(n int64) *int64 { return &n }

// toolResult is a measurement of one tool call, as a receiver would have
// recorded it.
func toolResult(session, turn, invocation string, size *int64) event.Usage {
	return event.Usage{
		SchemaVersion: event.SchemaVersion,
		Agent:         "claude-code",
		Kind:          event.UsageToolResult,
		Timestamp:     time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC),
		SessionID:     session,
		TurnID:        turn,
		InvocationID:  invocation,
		ToolName:      "Read",
		ResultBytes:   size,
	}
}

// repeatedRead is a finding about one file read several times, with the
// identifiers the hook stream recorded for each occurrence.
func repeatedRead(session string, calls ...profiler.Call) profiler.Finding {
	return profiler.Finding{
		Kind:        profiler.KindRepeatedRead,
		Confidence:  profiler.ConfidenceHigh,
		SessionID:   session,
		Occurrences: len(calls),
		Redundant:   len(calls) - 1,
		Calls:       calls,
		Path:        "/src/main.go",
	}
}

func measureOne(t *testing.T, f profiler.Finding, usage ...event.Usage) *int64 {
	t.Helper()

	ix := correlate.NewIndex()
	for _, u := range usage {
		ix.Add(u)
	}
	measured := ix.Measure([]profiler.Finding{f})
	if len(measured) != 1 {
		t.Fatalf("got %d measured findings, want 1", len(measured))
	}
	return measured[0].RedundantBytes
}

func wantBytes(t *testing.T, got *int64, want int64) {
	t.Helper()

	if got == nil {
		t.Fatalf("RedundantBytes = nil, want %d", want)
	}
	if *got != want {
		t.Errorf("RedundantBytes = %d, want %d", *got, want)
	}
}

func wantUnmeasured(t *testing.T, got *int64) {
	t.Helper()

	if got != nil {
		t.Errorf("RedundantBytes = %d, want nil", *got)
	}
}

// The first call did the work. Only what the run repeated is attributable to
// the redundancy.
func TestOnlyTheRepeatedCallsAreCounted(t *testing.T) {
	t.Parallel()

	f := repeatedRead("s1",
		profiler.Call{TurnID: "t1", InvocationID: "call-1"},
		profiler.Call{TurnID: "t1", InvocationID: "call-2"},
		profiler.Call{TurnID: "t1", InvocationID: "call-3"},
	)

	got := measureOne(t, f,
		toolResult("s1", "t1", "call-1", bytesOf(7000)),
		toolResult("s1", "t1", "call-2", bytesOf(48)),
		toolResult("s1", "t1", "call-3", bytesOf(51)),
	)

	wantBytes(t, got, 99)
}

// The streams are written by independent processes, so the order they are
// read in cannot change what they mean.
func TestOrderOfMeasurementsDoesNotMatter(t *testing.T) {
	t.Parallel()

	f := repeatedRead("s1",
		profiler.Call{TurnID: "t1", InvocationID: "call-1"},
		profiler.Call{TurnID: "t2", InvocationID: "call-2"},
	)

	forward := measureOne(t, f,
		toolResult("s1", "t1", "call-1", bytesOf(7000)),
		toolResult("s1", "t2", "call-2", bytesOf(120)),
	)
	reverse := measureOne(t, f,
		toolResult("s1", "t2", "call-2", bytesOf(120)),
		toolResult("s1", "t1", "call-1", bytesOf(7000)),
	)

	wantBytes(t, forward, 120)
	wantBytes(t, reverse, 120)
}

// Two records for one invocation may describe the same call twice or two
// different calls. Choosing between them would be a guess.
func TestDuplicateMeasurementIsNotAttributed(t *testing.T) {
	t.Parallel()

	f := repeatedRead("s1",
		profiler.Call{TurnID: "t1", InvocationID: "call-1"},
		profiler.Call{TurnID: "t1", InvocationID: "call-2"},
	)

	got := measureOne(t, f,
		toolResult("s1", "t1", "call-1", bytesOf(7000)),
		toolResult("s1", "t1", "call-2", bytesOf(48)),
		toolResult("s1", "t1", "call-2", bytesOf(48)),
	)

	wantUnmeasured(t, got)
}

// An unmeasured repeat makes the total unknown. A partial sum would look
// exactly like a complete one.
func TestOneUnmeasuredRepeatWithholdsTheWholeTotal(t *testing.T) {
	t.Parallel()

	f := repeatedRead("s1",
		profiler.Call{TurnID: "t1", InvocationID: "call-1"},
		profiler.Call{TurnID: "t1", InvocationID: "call-2"},
		profiler.Call{TurnID: "t1", InvocationID: "call-3"},
	)

	got := measureOne(t, f,
		toolResult("s1", "t1", "call-1", bytesOf(7000)),
		toolResult("s1", "t1", "call-2", bytesOf(48)),
	)

	wantUnmeasured(t, got)
}

func TestMeasurementWithoutResultBytesIsNotAttributed(t *testing.T) {
	t.Parallel()

	f := repeatedRead("s1",
		profiler.Call{TurnID: "t1", InvocationID: "call-1"},
		profiler.Call{TurnID: "t1", InvocationID: "call-2"},
	)

	got := measureOne(t, f, toolResult("s1", "t1", "call-2", nil))

	wantUnmeasured(t, got)
}

// An invocation identifier is only known to be unique within its turn, so the
// session and turn are part of the identity.
func TestMeasurementsFromAnotherContextDoNotMatch(t *testing.T) {
	t.Parallel()

	f := repeatedRead("s1",
		profiler.Call{TurnID: "t1", InvocationID: "call-1"},
		profiler.Call{TurnID: "t1", InvocationID: "call-2"},
	)

	cases := map[string]event.Usage{
		"another session": toolResult("s2", "t1", "call-2", bytesOf(48)),
		"another turn":    toolResult("s1", "t9", "call-2", bytesOf(48)),
	}
	for name, u := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			wantUnmeasured(t, measureOne(t, f, u))
		})
	}
}

// Token and cost measurements are reported against a turn. Nothing identifies
// the tool call they would have to be charged to, even when the record carries
// an invocation identifier of its own.
func TestModelRequestsAreNotMeasuredAsToolOutput(t *testing.T) {
	t.Parallel()

	f := repeatedRead("s1",
		profiler.Call{TurnID: "t1", InvocationID: "call-1"},
		profiler.Call{TurnID: "t1", InvocationID: "call-2"},
	)

	request := toolResult("s1", "t1", "call-2", bytesOf(48))
	request.Kind = event.UsageModelRequest
	request.Tokens = &event.Tokens{Input: 1200, Output: 40}

	wantUnmeasured(t, measureOne(t, f, request))
}

// A record that names no invocation cannot identify one, and indexing it
// would let two of them collide.
func TestMeasurementsWithoutAnInvocationAreNotIndexed(t *testing.T) {
	t.Parallel()

	f := repeatedRead("s1",
		profiler.Call{TurnID: "t1", InvocationID: "call-1"},
		profiler.Call{TurnID: "t1", InvocationID: ""},
	)

	wantUnmeasured(t, measureOne(t, f,
		toolResult("s1", "t1", "", bytesOf(48)),
	))
}

// Agents redact some tool names in telemetry. The hook stream says what the
// tool was; telemetry only measures it.
func TestToolNameDisagreementDoesNotAffectTheMatch(t *testing.T) {
	t.Parallel()

	f := repeatedRead("s1",
		profiler.Call{TurnID: "t1", InvocationID: "call-1"},
		profiler.Call{TurnID: "t1", InvocationID: "call-2"},
	)

	redacted := toolResult("s1", "t1", "call-2", bytesOf(48))
	redacted.ToolName = "mcp_tool"

	wantBytes(t, measureOne(t, f, redacted), 48)
}

// Without telemetry the analysis is exactly what it was before there was any.
func TestFindingsSurviveAnEmptyIndex(t *testing.T) {
	t.Parallel()

	findings := []profiler.Finding{
		repeatedRead("s1",
			profiler.Call{TurnID: "t1", InvocationID: "call-1"},
			profiler.Call{TurnID: "t1", InvocationID: "call-2"},
		),
		repeatedRead("s2",
			profiler.Call{TurnID: "t1", InvocationID: "call-3"},
			profiler.Call{TurnID: "t1", InvocationID: "call-4"},
		),
	}

	measured := correlate.NewIndex().Measure(findings)

	if len(measured) != len(findings) {
		t.Fatalf("got %d measured findings, want %d", len(measured), len(findings))
	}
	for i, m := range measured {
		if m.Finding.SessionID != findings[i].SessionID {
			t.Errorf("finding %d is %q, want %q", i, m.Finding.SessionID, findings[i].SessionID)
		}
		wantUnmeasured(t, m.RedundantBytes)
	}
}

// A finding from an agent that reports no identifiers is still a finding.
func TestFindingWithoutIdentityIsNotMeasured(t *testing.T) {
	t.Parallel()

	f := repeatedRead("s1", profiler.Call{}, profiler.Call{})

	wantUnmeasured(t, measureOne(t, f, toolResult("s1", "", "", bytesOf(48))))
}

// A finding that identifies no repeat has nothing to attribute, whatever the
// index happens to hold.
func TestFindingWithoutRepeatsIsNotMeasured(t *testing.T) {
	t.Parallel()

	f := repeatedRead("s1", profiler.Call{TurnID: "t1", InvocationID: "call-1"})

	wantUnmeasured(t, measureOne(t, f,
		toolResult("s1", "t1", "call-1", bytesOf(7000)),
	))
}

// Measurements that belong to no finding are not an error: most tool calls
// are not redundant.
func TestUnmatchedMeasurementsAreIgnored(t *testing.T) {
	t.Parallel()

	f := repeatedRead("s1",
		profiler.Call{TurnID: "t1", InvocationID: "call-1"},
		profiler.Call{TurnID: "t1", InvocationID: "call-2"},
	)

	got := measureOne(t, f,
		toolResult("s1", "t1", "call-2", bytesOf(48)),
		toolResult("s1", "t1", "call-99", bytesOf(9000)),
		toolResult("s7", "t3", "call-77", bytesOf(4000)),
	)

	wantBytes(t, got, 48)
}
