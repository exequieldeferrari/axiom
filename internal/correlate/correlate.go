// Package correlate attaches measurements an agent reported to the behavior
// Axiom observed.
//
// The two recorded streams are produced by different mechanisms with different
// lifetimes: hooks describe what an agent did, telemetry measures what it
// consumed. They are joined only on identifiers both streams carry, never on
// time and never on tool names, so a measurement is either provably about one
// occurrence or it is not used at all.
package correlate

import (
	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/profiler"
)

// Key identifies one tool invocation.
//
// The identity is composite because an invocation identifier is only known to
// be unique within the turn that produced it. Joining on it alone would mean
// trusting a property no recorded evidence establishes.
type Key struct {
	SessionID    string
	TurnID       string
	InvocationID string
}

// turnKey identifies one turn: the execution context an agent labels with its
// own turn or prompt identifier, and which several model requests and tool
// calls may share.
//
// The session is part of the identity for the same reason it is part of Key:
// turn identifiers are the agent's own and are not known to be unique beyond
// the session that issued them.
type turnKey struct {
	SessionID string
	TurnID    string
}

// Index holds what an agent measured for each tool invocation, and what it
// reported consuming in each turn.
//
// The two are kept apart because they support different claims. A tool result
// belongs to one call and can be attributed to it. A model request belongs to
// a turn, which several calls may share, and no recorded evidence says which
// of them a particular request served.
//
// Records may be added in any order: the two streams are written by
// independent processes, so neither the order within a log nor the order
// between logs carries meaning.
type Index struct {
	results map[Key]measurement
	turns   map[turnKey]*turnUsage
}

// turnUsage accumulates the model requests observed for one turn.
//
// Several requests may carry the same turn identifier, so this is a running
// total rather than a single record.
type turnUsage struct {
	requests int
	tokens   event.Tokens
	cost     int64
	// allTokens and allCost record whether every request contributed, so that
	// a sum missing part of itself is never reported as though it were whole.
	allTokens bool
	allCost   bool
}

// measurement is what telemetry reported for one invocation.
//
// An invocation measured more than once is ambiguous rather than resolved:
// duplicate records may describe the same call twice or two different calls,
// and picking one would be a guess presented as a measurement.
type measurement struct {
	bytes     *int64
	ambiguous bool
}

// NewIndex returns an index holding no measurements.
func NewIndex() *Index {
	return &Index{
		results: make(map[Key]measurement),
		turns:   make(map[turnKey]*turnUsage),
	}
}

// Add records one usage record, ignoring anything Axiom cannot place.
func (ix *Index) Add(u event.Usage) {
	switch u.Kind {
	case event.UsageToolResult:
		ix.addResult(u)
	case event.UsageModelRequest:
		ix.addRequest(u)
	}
}

// addResult records what one tool call returned. A record naming no
// invocation cannot identify a call, and indexing it would let two of them
// collide.
func (ix *Index) addResult(u event.Usage) {
	if u.InvocationID == "" {
		return
	}

	key := Key{SessionID: u.SessionID, TurnID: u.TurnID, InvocationID: u.InvocationID}
	if _, seen := ix.results[key]; seen {
		ix.results[key] = measurement{ambiguous: true}
		return
	}
	ix.results[key] = measurement{bytes: u.ResultBytes}
}

// addRequest accumulates one model request against the turn it served.
//
// A request naming no turn is dropped rather than pooled under an empty one,
// which would attach one session's consumption to another's work.
func (ix *Index) addRequest(u event.Usage) {
	if u.TurnID == "" {
		return
	}

	key := turnKey{SessionID: u.SessionID, TurnID: u.TurnID}
	t, ok := ix.turns[key]
	if !ok {
		t = &turnUsage{allTokens: true, allCost: true}
		ix.turns[key] = t
	}

	t.requests++
	if u.Tokens == nil {
		t.allTokens = false
	} else {
		t.tokens.Input += u.Tokens.Input
		t.tokens.Output += u.Tokens.Output
		t.tokens.CacheRead += u.Tokens.CacheRead
		t.tokens.CacheCreation += u.Tokens.CacheCreation
	}
	if u.CostMicros == nil {
		t.allCost = false
	} else {
		t.cost += *u.CostMicros
	}
}

// Measured pairs a finding with what was measured for it. The finding itself
// is unchanged: telemetry adds a measurement, never evidence.
type Measured struct {
	profiler.Finding

	// RedundantBytes is the total output of the repeated occurrences,
	// excluding the first, and is nil unless every one of them was measured
	// exactly once.
	//
	// It is a count of bytes an agent reported returning, not tokens and not
	// cost. Nil means unknown: telemetry is recorded only while a receiver is
	// running, so most logs have none at all.
	RedundantBytes *int64

	// Associated is what the agent reported consuming in the turns the
	// finding's calls happened in, and is nil when none of it was observed.
	//
	// It is context, not a measurement of the finding. Nothing here is
	// attributable to the repetition.
	Associated *Consumption
}

// Consumption is model consumption observed in a finding's turns.
//
// A turn is an execution context the agent identifies, and the tool calls and
// model requests sharing one are recorded without anything relating them.
// Nothing says which requests a particular call caused, so these totals
// describe the company the repetition kept and never its price.
//
// The totals are also not turn totals. Telemetry exists only while a receiver
// is running, and a receiver started midway through a session records part of
// a turn without anything saying so, which is why every total here is what was
// observed rather than what happened.
//
// Where the finding happened is not observed evidence, though. It comes from
// the behavior stream and is stated exactly, so that missing telemetry reduces
// how much of the finding Axiom can speak to instead of making the finding
// look smaller than it was.
type Consumption struct {
	// AffectedTurns is how many distinct turns the finding's calls happened
	// in, and ObservedTurns is how many of those had any model request
	// recorded. Requests counts the requests observed across them, and
	// several of them may share one turn.
	//
	// The two turn counts are separate because they answer separate
	// questions. Where the behavior happened is established by the finding
	// alone. What Axiom saw of it depends on whether a receiver was running,
	// and evidence that was never recorded must reduce the coverage of a
	// claim rather than shrink the thing being described.
	AffectedTurns int
	ObservedTurns int
	Requests      int

	// Tokens sums each dimension over the observed requests, or is nil when
	// any of them reported no counts at all.
	//
	// The dimensions are summed together or not at all. An unreported
	// dimension is recorded as zero, so Axiom cannot tell one from a measured
	// zero, and a rule that dropped dimensions individually would be
	// enforcing a distinction the record does not preserve.
	Tokens *event.Tokens

	// CostMicros sums the agent's own cost estimates over the observed
	// requests, in millionths of a US dollar, or is nil when any of them
	// reported none. It is the agent's estimate and not a billing figure.
	//
	// Cost is withheld independently of tokens because the two are recorded
	// independently: one missing estimate says nothing about the counts.
	CostMicros *int64
}

// Measure attaches a measurement to each finding, preserving their order.
//
// Every finding is returned whether or not anything was measured for it, so
// analysis that has no telemetry behaves exactly as it did before there was
// any.
func (ix *Index) Measure(findings []profiler.Finding) []Measured {
	out := make([]Measured, 0, len(findings))
	for _, f := range findings {
		out = append(out, Measured{
			Finding:        f,
			RedundantBytes: ix.redundantBytes(f),
			Associated:     ix.associated(f),
		})
	}
	return out
}

// associated totals what was observed in the turns a finding happened in.
//
// Findings that share a turn each report it in full, which is the honest
// answer for a finding read on its own. It is also why these totals must never
// be added up across findings: the shared turn would be counted twice.
func (ix *Index) associated(f profiler.Finding) *Consumption {
	turns, ok := affectedTurns(f)
	if !ok {
		return nil
	}

	var (
		c                  = Consumption{AffectedTurns: len(turns)}
		tokens             event.Tokens
		cost               int64
		allTokens, allCost = true, true
	)
	for _, key := range turns {
		t, ok := ix.turns[key]
		if !ok {
			// A turn with no model request recorded contributes nothing. It
			// does not mean the turn was free, and it does not mean the
			// finding happened in one turn fewer.
			continue
		}
		c.ObservedTurns++
		c.Requests += t.requests
		tokens.Input += t.tokens.Input
		tokens.Output += t.tokens.Output
		tokens.CacheRead += t.tokens.CacheRead
		tokens.CacheCreation += t.tokens.CacheCreation
		cost += t.cost
		allTokens = allTokens && t.allTokens
		allCost = allCost && t.allCost
	}

	if c.Requests == 0 {
		return nil
	}
	if allTokens {
		c.Tokens = &tokens
	}
	if allCost {
		c.CostMicros = &cost
	}
	return &c
}

// affectedTurns lists the distinct turns a finding's calls happened in,
// including the turn of the first call: a run is one piece of behavior, and a
// context that omitted where it began would describe something narrower than
// the finding.
//
// A call with no turn makes the set unknowable. Associating consumption with
// the turns that happen to be identified would quietly describe part of the
// finding as though it were all of it.
func affectedTurns(f profiler.Finding) ([]turnKey, bool) {
	if len(f.Calls) == 0 {
		return nil, false
	}

	out := make([]turnKey, 0, 1)
	seen := make(map[turnKey]struct{}, len(f.Calls))
	for _, c := range f.Calls {
		if c.TurnID == "" {
			return nil, false
		}
		key := turnKey{SessionID: f.SessionID, TurnID: c.TurnID}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	return out, true
}

// redundantBytes totals what the repeated occurrences returned.
//
// It reports nothing unless all of them were measured. A partial sum would
// understate the total while looking exactly like a complete one, and an
// understated measurement is worse than an absent one: it invites a conclusion
// the evidence does not support.
func (ix *Index) redundantBytes(f profiler.Finding) *int64 {
	if len(f.Calls) < 2 {
		return nil
	}

	var total int64
	for _, c := range f.Calls[1:] {
		if c.InvocationID == "" {
			return nil
		}
		m, ok := ix.results[Key{SessionID: f.SessionID, TurnID: c.TurnID, InvocationID: c.InvocationID}]
		if !ok || m.ambiguous || m.bytes == nil {
			return nil
		}
		total += *m.bytes
	}
	return &total
}
