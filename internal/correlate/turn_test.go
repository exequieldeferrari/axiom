package correlate_test

import (
	"testing"

	"github.com/exequieldeferrari/axiom/internal/correlate"
	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/turns"
)

func recordedTurn(session, turn string) turns.Turn {
	return turns.Turn{Ref: turns.Ref{SessionID: session, TurnID: turn}, Ordinal: 1, ToolCalls: 1}
}

func measureTurns(recorded []turns.Turn, usage ...event.Usage) ([]correlate.MeasuredTurn, correlate.Outside) {
	ix := correlate.NewIndex()
	for _, u := range usage {
		ix.Add(u)
	}
	return ix.MeasureTurns(recorded)
}

// Consumption is joined on the exact identity the turn was recorded under.
// Nothing is matched by time, by proximity or by the turn identifier alone.
func TestTurnConsumptionJoinsOnExactIdentity(t *testing.T) {
	t.Parallel()

	cases := map[string]event.Usage{
		"another turn":    modelRequest("session-1", "turn-9", tokens(8, 3, 100, 20), micros(213915)),
		"another session": modelRequest("session-9", "turn-1", tokens(8, 3, 100, 20), micros(213915)),
		"no turn at all":  modelRequest("session-1", "", tokens(8, 3, 100, 20), micros(213915)),
	}
	for name, usage := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			measured, _ := measureTurns([]turns.Turn{recordedTurn("session-1", "turn-1")}, usage)
			if measured[0].Observed != nil {
				t.Errorf("Observed = %+v, want nothing joined to a different identity", *measured[0].Observed)
			}
		})
	}

	t.Run("the same identity", func(t *testing.T) {
		t.Parallel()

		measured, _ := measureTurns([]turns.Turn{recordedTurn("session-1", "turn-1")},
			modelRequest("session-1", "turn-1", tokens(8, 3, 100, 20), micros(213915)),
			modelRequest("session-1", "turn-1", tokens(4, 1, 50, 10), micros(74085)))

		got := measured[0].Observed
		if got == nil {
			t.Fatal("Observed = nil, want the requests recorded under the turn")
		}
		if got.Requests != 2 {
			t.Errorf("Requests = %d, want both", got.Requests)
		}
		if want := *tokens(12, 4, 150, 30); *got.Tokens != want {
			t.Errorf("Tokens = %+v, want %+v", *got.Tokens, want)
		}
		if *got.CostMicros != 288000 {
			t.Errorf("CostMicros = %d, want the sum of both estimates", *got.CostMicros)
		}
	})
}

// Telemetry exists only while a receiver is running. A turn with nothing
// recorded consumed an unknown amount, which is not zero.
func TestUnobservedTurnConsumptionIsNotZero(t *testing.T) {
	t.Parallel()

	measured, _ := measureTurns([]turns.Turn{recordedTurn("session-1", "turn-1")})

	if len(measured) != 1 {
		t.Fatalf("got %d turns, want the recorded one whether or not it was measured", len(measured))
	}
	if measured[0].Observed != nil {
		t.Errorf("Observed = %+v, want nil for a turn nothing was recorded for", *measured[0].Observed)
	}
	if measured[0].ToolCalls != 1 {
		t.Error("the turn's own evidence was changed by the absence of telemetry")
	}
}

// A sum missing part of itself is never reported as though it were whole, and
// tokens and cost are withheld independently because they are recorded
// independently.
func TestTurnConsumptionWithholdsPartialTotals(t *testing.T) {
	t.Parallel()

	t.Run("a request reporting no counts withholds the tokens", func(t *testing.T) {
		t.Parallel()

		measured, _ := measureTurns([]turns.Turn{recordedTurn("session-1", "turn-1")},
			modelRequest("session-1", "turn-1", tokens(8, 3, 100, 20), micros(213915)),
			modelRequest("session-1", "turn-1", nil, micros(74085)))

		got := measured[0].Observed
		if got.Tokens != nil {
			t.Errorf("Tokens = %+v, want them withheld", *got.Tokens)
		}
		if got.CostMicros == nil || *got.CostMicros != 288000 {
			t.Error("the cost was withheld with the tokens, though both requests reported one")
		}
		if got.Requests != 2 {
			t.Errorf("Requests = %d, want both: the request was observed", got.Requests)
		}
	})

	t.Run("a request reporting no estimate withholds the cost", func(t *testing.T) {
		t.Parallel()

		measured, _ := measureTurns([]turns.Turn{recordedTurn("session-1", "turn-1")},
			modelRequest("session-1", "turn-1", tokens(8, 3, 100, 20), micros(213915)),
			modelRequest("session-1", "turn-1", tokens(4, 1, 50, 10), nil))

		got := measured[0].Observed
		if got.CostMicros != nil {
			t.Errorf("CostMicros = %d, want it withheld", *got.CostMicros)
		}
		if got.Tokens == nil {
			t.Error("the tokens were withheld with the cost, though both requests reported them")
		}
	})
}

// Requests are recorded under turn identifiers no tool call ever named. They
// belong to no recorded turn and are counted rather than dropped or attributed
// to a neighbour.
func TestConsumptionOutsideRecordedTurns(t *testing.T) {
	t.Parallel()

	measured, outside := measureTurns([]turns.Turn{recordedTurn("session-1", "turn-1")},
		modelRequest("session-1", "turn-1", tokens(8, 3, 100, 20), micros(213915)),
		modelRequest("session-1", "turn-2", tokens(4, 1, 50, 10), micros(74085)),
		modelRequest("session-1", "turn-2", tokens(4, 1, 50, 10), micros(74085)),
		modelRequest("session-2", "turn-1", tokens(4, 1, 50, 10), micros(74085)))

	if outside.Turns != 2 || outside.Requests != 3 {
		t.Errorf("Outside = %+v, want 3 requests under 2 identities", outside)
	}
	if got := measured[0].Observed.Requests; got != 1 {
		t.Errorf("Requests = %d, want only the one recorded under the turn", got)
	}
}

// A tool result names an invocation, not a turn's consumption, so it never
// makes a turn look measured.
func TestToolResultsAreNotTurnConsumption(t *testing.T) {
	t.Parallel()

	bytes := int64(4096)
	measured, outside := measureTurns([]turns.Turn{recordedTurn("session-1", "turn-1")},
		event.Usage{
			SchemaVersion: event.SchemaVersion,
			Agent:         "claude-code",
			Kind:          event.UsageToolResult,
			SessionID:     "session-1",
			TurnID:        "turn-1",
			InvocationID:  "call-1",
			ResultBytes:   &bytes,
		})

	if measured[0].Observed != nil {
		t.Error("a tool result was reported as model consumption")
	}
	if outside != (correlate.Outside{}) {
		t.Errorf("Outside = %+v, want a tool result to count as no model request", outside)
	}
}

// Turns are returned in the order they were derived, so the report's ordering
// is the analysis's and not the index's.
func TestMeasuredTurnsPreserveOrder(t *testing.T) {
	t.Parallel()

	recorded := []turns.Turn{
		recordedTurn("session-1", "turn-a"),
		recordedTurn("session-1", "turn-b"),
		recordedTurn("session-2", "turn-a"),
	}
	measured, _ := measureTurns(recorded, modelRequest("session-2", "turn-a", nil, nil))

	for i, m := range measured {
		if m.Ref != recorded[i].Ref {
			t.Fatalf("turn %d is %+v, want %+v", i, m.Ref, recorded[i].Ref)
		}
	}
}
