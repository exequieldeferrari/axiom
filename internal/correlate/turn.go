package correlate

import (
	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/turns"
)

// MeasuredTurn pairs a turn that recorded work with what the agent reported
// consuming under the same identity. The turn itself is unchanged: telemetry
// adds a measurement, never evidence.
type MeasuredTurn struct {
	turns.Turn

	// Observed is what was recorded for the turn's identity, and is nil when
	// nothing was.
	//
	// Nil is not zero. Telemetry exists only for the time a receiver was
	// running, so a turn with none recorded is a turn Axiom did not see
	// consuming anything, not a turn that consumed nothing.
	Observed *TurnConsumption
}

// TurnConsumption is the model consumption observed under one turn identity.
//
// It is what the agent reported for requests it labelled with this turn. It is
// not the cost of the turn's tool calls: nothing recorded says which request
// served which call, and requests are recorded that no call caused. It is not a
// billing figure either, and it is not complete — a receiver started midway
// through a session records part of a turn without anything saying so, which is
// why everything here is what was observed rather than what happened.
//
// It is also not everything a nested agent spent. Nested tool calls were
// observed carrying the turn that launched them, and nested model requests were
// observed carrying turn identifiers of their own, so consumption produced
// inside a subagent may be counted somewhere else entirely.
type TurnConsumption struct {
	// Requests counts the model requests observed under the turn.
	Requests int

	// Tokens sums each dimension over those requests, or is nil when any of
	// them reported no counts at all. The dimensions are summed together or
	// not at all, for the reason Consumption.Tokens gives.
	Tokens *event.Tokens

	// CostMicros sums the agent's own cost estimates over those requests, in
	// millionths of a US dollar, or is nil when any of them reported none. It
	// is withheld independently of tokens because the two are recorded
	// independently.
	CostMicros *int64
}

// Outside counts model consumption observed under turn identities that no
// recorded tool call named.
//
// Captures produce these: the agent labels requests with turn identifiers that
// never reach a hook, because no tool ran under them. They are counted so that
// the observed consumption in a report adds up to something honest, and they
// are not attributed to any turn, because nothing says where they belong.
type Outside struct {
	// Turns counts those identities, and Requests the model requests recorded
	// across them. Only counts are reported: the tokens and cost would be a
	// second consumption total under a heading that cannot say what it covers.
	Turns    int
	Requests int
}

// MeasureTurns attaches observed consumption to each turn that recorded work,
// preserving their order, and counts what was observed outside all of them.
//
// Every turn is returned whether or not anything was observed for it, so
// analysis that has no telemetry behaves exactly as it did before there was
// any. The set of turns measured here is the set the outside count is taken
// against, so the two cannot drift apart.
func (ix *Index) MeasureTurns(recorded []turns.Turn) ([]MeasuredTurn, Outside) {
	out := make([]MeasuredTurn, 0, len(recorded))
	held := make(map[turnKey]struct{}, len(recorded))
	for _, t := range recorded {
		key := turnKey{SessionID: t.SessionID, TurnID: t.TurnID}
		held[key] = struct{}{}
		out = append(out, MeasuredTurn{Turn: t, Observed: ix.observed(key)})
	}

	var outside Outside
	for key, u := range ix.turns {
		if _, ok := held[key]; ok {
			continue
		}
		outside.Turns++
		outside.Requests += u.requests
	}
	return out, outside
}

// observed reports what was recorded under one turn identity, applying the
// same withholding rules the finding join applies: a sum missing part of itself
// is never reported as though it were whole.
func (ix *Index) observed(key turnKey) *TurnConsumption {
	t, ok := ix.turns[key]
	if !ok {
		return nil
	}

	c := TurnConsumption{Requests: t.requests}
	if t.allTokens {
		tokens := t.tokens
		c.Tokens = &tokens
	}
	if t.allCost {
		cost := t.cost
		c.CostMicros = &cost
	}
	return &c
}
