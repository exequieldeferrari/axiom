package correlate_test

import (
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/correlate"
	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/profiler"
)

// modelRequest is one request an agent reported making, labelled with the turn
// it belongs to. Several of them may carry the same turn.
func modelRequest(session, turn string, tokens *event.Tokens, cost *int64) event.Usage {
	return event.Usage{
		SchemaVersion: event.SchemaVersion,
		Agent:         "claude-code",
		Kind:          event.UsageModelRequest,
		Timestamp:     time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC),
		SessionID:     session,
		TurnID:        turn,
		Model:         "claude-sonnet-5",
		Tokens:        tokens,
		CostMicros:    cost,
	}
}

// micros is a cost estimate in millionths of a US dollar.
func micros(n int64) *int64 { return &n }

func tokens(input, output, cacheRead, cacheCreation int64) *event.Tokens {
	return &event.Tokens{
		Input:         input,
		Output:        output,
		CacheRead:     cacheRead,
		CacheCreation: cacheCreation,
	}
}

func associateOne(t *testing.T, f profiler.Finding, usage ...event.Usage) *correlate.Consumption {
	t.Helper()

	ix := correlate.NewIndex()
	for _, u := range usage {
		ix.Add(u)
	}
	measured := ix.Measure([]profiler.Finding{f})
	if len(measured) != 1 {
		t.Fatalf("got %d measured findings, want 1", len(measured))
	}
	return measured[0].Associated
}

func wantConsumption(t *testing.T, got *correlate.Consumption) correlate.Consumption {
	t.Helper()

	if got == nil {
		t.Fatal("Associated = nil, want consumption")
	}
	return *got
}

// wantCoverage states both turn counts on every assertion, so that a change
// conflating where a finding happened with what was recorded of it cannot pass
// unnoticed.
func wantCoverage(t *testing.T, c correlate.Consumption, affected, observed, requests int) {
	t.Helper()

	if c.AffectedTurns != affected || c.ObservedTurns != observed || c.Requests != requests {
		t.Errorf("AffectedTurns = %d, ObservedTurns = %d, Requests = %d; want %d, %d and %d",
			c.AffectedTurns, c.ObservedTurns, c.Requests, affected, observed, requests)
	}
}

func wantTokens(t *testing.T, got *event.Tokens, want event.Tokens) {
	t.Helper()

	if got == nil {
		t.Fatalf("Tokens = nil, want %+v", want)
	}
	if *got != want {
		t.Errorf("Tokens = %+v, want %+v", *got, want)
	}
}

func wantCost(t *testing.T, got *int64, want int64) {
	t.Helper()

	if got == nil {
		t.Fatalf("CostMicros = nil, want %d", want)
	}
	if *got != want {
		t.Errorf("CostMicros = %d, want %d", *got, want)
	}
}

// Several model requests may carry the same turn identifier, so a turn's
// consumption is the sum of all of them.
func TestEveryRequestInTheTurnIsCounted(t *testing.T) {
	t.Parallel()

	f := repeatedRead("s1",
		profiler.Call{TurnID: "t1", InvocationID: "call-1"},
		profiler.Call{TurnID: "t1", InvocationID: "call-2"},
	)

	c := wantConsumption(t, associateOne(t, f,
		modelRequest("s1", "t1", tokens(2, 93, 0, 35419), micros(213915)),
		modelRequest("s1", "t1", tokens(6, 308, 117147, 5722), micros(74085)),
	))

	wantCoverage(t, c, 1, 1, 2)
	wantTokens(t, c.Tokens, event.Tokens{Input: 8, Output: 401, CacheRead: 117147, CacheCreation: 41141})
	wantCost(t, c.CostMicros, 288000)
}

// A run that repeats within one turn happened in one turn. Counting the turn
// once per call would multiply consumption by the repetition being reported.
func TestATurnIsCountedOncePerFinding(t *testing.T) {
	t.Parallel()

	f := repeatedRead("s1",
		profiler.Call{TurnID: "t1", InvocationID: "call-1"},
		profiler.Call{TurnID: "t1", InvocationID: "call-2"},
		profiler.Call{TurnID: "t1", InvocationID: "call-3"},
	)

	c := wantConsumption(t, associateOne(t, f,
		modelRequest("s1", "t1", tokens(10, 20, 30, 40), micros(500)),
	))

	wantCoverage(t, c, 1, 1, 1)
	wantTokens(t, c.Tokens, event.Tokens{Input: 10, Output: 20, CacheRead: 30, CacheCreation: 40})
	wantCost(t, c.CostMicros, 500)
}

// A run spanning turns happened in all of them, and each contributes what it
// consumed.
func TestEveryAffectedTurnContributes(t *testing.T) {
	t.Parallel()

	f := repeatedRead("s1",
		profiler.Call{TurnID: "t1", InvocationID: "call-1"},
		profiler.Call{TurnID: "t2", InvocationID: "call-2"},
		profiler.Call{TurnID: "t2", InvocationID: "call-3"},
	)

	c := wantConsumption(t, associateOne(t, f,
		modelRequest("s1", "t1", tokens(1, 2, 3, 4), micros(100)),
		modelRequest("s1", "t2", tokens(10, 20, 30, 40), micros(200)),
		modelRequest("s1", "t3", tokens(99, 99, 99, 99), micros(900)),
	))

	wantCoverage(t, c, 2, 2, 2)
	wantTokens(t, c.Tokens, event.Tokens{Input: 11, Output: 22, CacheRead: 33, CacheCreation: 44})
	wantCost(t, c.CostMicros, 300)
}

// The first call is not redundant, but the turn it happened in is still part
// of where the finding took place.
func TestTheFirstCallsTurnIsIncluded(t *testing.T) {
	t.Parallel()

	f := repeatedRead("s1",
		profiler.Call{TurnID: "t1", InvocationID: "call-1"},
		profiler.Call{TurnID: "t2", InvocationID: "call-2"},
	)

	c := wantConsumption(t, associateOne(t, f,
		modelRequest("s1", "t1", tokens(1, 2, 3, 4), micros(100)),
	))

	// The second turn is where the finding happened too, and nothing was
	// recorded there.
	wantCoverage(t, c, 2, 1, 1)
	wantTokens(t, c.Tokens, event.Tokens{Input: 1, Output: 2, CacheRead: 3, CacheCreation: 4})
}

// Two findings in one turn are two views of the same consumption. Each is
// true on its own, which is exactly why they cannot be added together.
func TestFindingsSharingATurnEachReportAllOfIt(t *testing.T) {
	t.Parallel()

	findings := []profiler.Finding{
		repeatedRead("s1",
			profiler.Call{TurnID: "t1", InvocationID: "call-1"},
			profiler.Call{TurnID: "t1", InvocationID: "call-2"},
		),
		repeatedRead("s1",
			profiler.Call{TurnID: "t1", InvocationID: "call-3"},
			profiler.Call{TurnID: "t1", InvocationID: "call-4"},
		),
	}

	ix := correlate.NewIndex()
	ix.Add(modelRequest("s1", "t1", tokens(1, 2, 3, 4), micros(100)))
	measured := ix.Measure(findings)

	if len(measured) != 2 {
		t.Fatalf("got %d measured findings, want 2", len(measured))
	}
	for _, m := range measured {
		c := wantConsumption(t, m.Associated)
		wantCoverage(t, c, 1, 1, 1)
		wantCost(t, c.CostMicros, 100)
	}
}

// Telemetry is recorded only while a receiver runs, so a turn with nothing
// recorded is unknown rather than free.
func TestNoRequestsMeansNoConsumption(t *testing.T) {
	t.Parallel()

	f := repeatedRead("s1",
		profiler.Call{TurnID: "t1", InvocationID: "call-1"},
		profiler.Call{TurnID: "t1", InvocationID: "call-2"},
	)

	cases := map[string][]event.Usage{
		"nothing recorded":     nil,
		"only tool results":    {toolResult("s1", "t1", "call-2", bytesOf(48))},
		"another session":      {modelRequest("s2", "t1", tokens(1, 2, 3, 4), micros(100))},
		"another turn":         {modelRequest("s1", "t9", tokens(1, 2, 3, 4), micros(100))},
		"request with no turn": {modelRequest("s1", "", tokens(1, 2, 3, 4), micros(100))},
	}
	for name, usage := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := associateOne(t, f, usage...); got != nil {
				t.Errorf("Associated = %+v, want nil", *got)
			}
		})
	}
}

// A call with no turn leaves part of the finding unplaced. Reporting the rest
// would describe a narrower context than the one the finding spans.
func TestAnUnplacedCallWithholdsConsumption(t *testing.T) {
	t.Parallel()

	f := repeatedRead("s1",
		profiler.Call{TurnID: "t1", InvocationID: "call-1"},
		profiler.Call{InvocationID: "call-2"},
	)

	if got := associateOne(t, f, modelRequest("s1", "t1", tokens(1, 2, 3, 4), micros(100))); got != nil {
		t.Errorf("Associated = %+v, want nil", *got)
	}
}

// A finding whose occurrences were never identified has no turns to look up,
// which is not the same as its turns having consumed nothing.
func TestAFindingWithoutCallsHasNoConsumption(t *testing.T) {
	t.Parallel()

	f := profiler.Finding{
		Kind:      profiler.KindRepeatedRead,
		SessionID: "s1",
		Path:      "/src/main.go",
	}

	if got := associateOne(t, f, modelRequest("s1", "t1", tokens(1, 2, 3, 4), micros(100))); got != nil {
		t.Errorf("Associated = %+v, want nil", *got)
	}
}

// An unreported dimension is recorded as zero, so a sum missing one request's
// counts cannot be told apart from a complete one.
func TestARequestWithoutTokensWithholdsThemAll(t *testing.T) {
	t.Parallel()

	f := repeatedRead("s1",
		profiler.Call{TurnID: "t1", InvocationID: "call-1"},
		profiler.Call{TurnID: "t1", InvocationID: "call-2"},
	)
	counted := modelRequest("s1", "t1", tokens(1, 2, 3, 4), micros(100))
	silent := modelRequest("s1", "t1", nil, micros(200))

	cases := map[string][]event.Usage{
		"silent request last":  {counted, silent},
		"silent request first": {silent, counted},
	}
	for name, usage := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := wantConsumption(t, associateOne(t, f, usage...))
			if c.Requests != 2 {
				t.Errorf("Requests = %d, want 2", c.Requests)
			}
			if c.Tokens != nil {
				t.Errorf("Tokens = %+v, want nil", *c.Tokens)
			}
			// Counts and cost are reported separately, so one missing set of
			// counts says nothing about the cost.
			wantCost(t, c.CostMicros, 300)
		})
	}
}

// Cost is the agent's own estimate and may be absent on its own.
func TestARequestWithoutCostWithholdsOnlyCost(t *testing.T) {
	t.Parallel()

	f := repeatedRead("s1",
		profiler.Call{TurnID: "t1", InvocationID: "call-1"},
		profiler.Call{TurnID: "t1", InvocationID: "call-2"},
	)

	c := wantConsumption(t, associateOne(t, f,
		modelRequest("s1", "t1", tokens(1, 2, 3, 4), micros(100)),
		modelRequest("s1", "t1", tokens(10, 20, 30, 40), nil),
	))

	wantTokens(t, c.Tokens, event.Tokens{Input: 11, Output: 22, CacheRead: 33, CacheCreation: 44})
	if c.CostMicros != nil {
		t.Errorf("CostMicros = %d, want nil", *c.CostMicros)
	}
}

// A turn that consumed nothing recorded is not evidence about a turn that did,
// and it is still a turn the finding happened in. Telemetry decides what Axiom
// observed, never where the behavior took place.
func TestUnrecordedTurnsReduceCoverageAndNotTheFinding(t *testing.T) {
	t.Parallel()

	f := repeatedRead("s1",
		profiler.Call{TurnID: "t1", InvocationID: "call-1"},
		profiler.Call{TurnID: "t2", InvocationID: "call-2"},
		profiler.Call{TurnID: "t3", InvocationID: "call-3"},
	)

	c := wantConsumption(t, associateOne(t, f,
		modelRequest("s1", "t2", tokens(1, 2, 3, 4), micros(100)),
	))

	wantCoverage(t, c, 3, 1, 1)
	// The two turns with nothing recorded contribute nothing rather than
	// zero, so the totals are the observed turn's own.
	wantTokens(t, c.Tokens, event.Tokens{Input: 1, Output: 2, CacheRead: 3, CacheCreation: 4})
	wantCost(t, c.CostMicros, 100)
}

// The two streams have independent writers, so nothing about the reading
// order may change the totals.
func TestOrderOfRequestsDoesNotMatter(t *testing.T) {
	t.Parallel()

	f := repeatedRead("s1",
		profiler.Call{TurnID: "t1", InvocationID: "call-1"},
		profiler.Call{TurnID: "t2", InvocationID: "call-2"},
	)
	first := modelRequest("s1", "t1", tokens(1, 2, 3, 4), micros(100))
	second := modelRequest("s1", "t2", tokens(10, 20, 30, 40), micros(200))

	forward := wantConsumption(t, associateOne(t, f, first, second))
	reverse := wantConsumption(t, associateOne(t, f, second, first))

	wantTokens(t, forward.Tokens, event.Tokens{Input: 11, Output: 22, CacheRead: 33, CacheCreation: 44})
	wantTokens(t, reverse.Tokens, event.Tokens{Input: 11, Output: 22, CacheRead: 33, CacheCreation: 44})
	wantCoverage(t, forward, 2, 2, 2)
	wantCoverage(t, reverse, 2, 2, 2)
	wantCost(t, reverse.CostMicros, 300)
}

// Measured tool output and associated consumption answer different questions,
// and one being unavailable must not withhold the other.
func TestMeasuredOutputAndConsumptionAreIndependent(t *testing.T) {
	t.Parallel()

	f := repeatedRead("s1",
		profiler.Call{TurnID: "t1", InvocationID: "call-1"},
		profiler.Call{TurnID: "t1", InvocationID: "call-2"},
	)

	ix := correlate.NewIndex()
	ix.Add(modelRequest("s1", "t1", tokens(1, 2, 3, 4), micros(100)))
	measured := ix.Measure([]profiler.Finding{f})

	wantUnmeasured(t, measured[0].RedundantBytes)
	if measured[0].Associated == nil {
		t.Error("Associated = nil, want consumption despite unmeasured output")
	}
}
