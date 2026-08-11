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

// Index holds what an agent measured for each tool invocation.
//
// Records may be added in any order: the two streams are written by
// independent processes, so neither the order within a log nor the order
// between logs carries meaning.
type Index struct {
	results map[Key]measurement
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
	return &Index{results: make(map[Key]measurement)}
}

// Add records one usage record.
//
// Anything that cannot identify a single tool call is ignored. Model requests
// are measured for a whole turn, so charging one to an individual tool call
// would assert a cause the recording does not show.
func (ix *Index) Add(u event.Usage) {
	if u.Kind != event.UsageToolResult || u.InvocationID == "" {
		return
	}

	key := Key{SessionID: u.SessionID, TurnID: u.TurnID, InvocationID: u.InvocationID}
	if _, seen := ix.results[key]; seen {
		ix.results[key] = measurement{ambiguous: true}
		return
	}
	ix.results[key] = measurement{bytes: u.ResultBytes}
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
}

// Measure attaches a measurement to each finding, preserving their order.
//
// Every finding is returned whether or not anything was measured for it, so
// analysis that has no telemetry behaves exactly as it did before there was
// any.
func (ix *Index) Measure(findings []profiler.Finding) []Measured {
	out := make([]Measured, 0, len(findings))
	for _, f := range findings {
		out = append(out, Measured{Finding: f, RedundantBytes: ix.redundantBytes(f)})
	}
	return out
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
