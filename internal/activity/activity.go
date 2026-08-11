// Package activity attributes observed agent work to the paths it happened at.
//
// This is measurement, not a finding. The profiler asks what repeated without a
// good reason and stays silent unless the evidence rules the good reasons out;
// this package asks what was observed and rules nothing out. Every tool call
// that reached the log is counted exactly once, and a total is reported only
// when every operation behind it reported what the total needs.
//
// The two analyses are deliberately separate. A profile counts the operations
// the profiler treats as barriers, ignores context scopes and resets, and never
// withholds a count for lack of one.
package activity

import (
	"cmp"
	"slices"
	"time"

	"github.com/exequieldeferrari/axiom/internal/event"
)

// ByteLookup reports what telemetry measured one tool invocation as returning,
// and whether that measurement exists at all.
//
// It is a function rather than a dependency so that this package needs to know
// nothing about how the two recorded streams are joined. The caller that has
// both of them supplies it.
type ByteLookup func(session, turn, invocation string) (int64, bool)

// Composition partitions the observed tool calls by the shape of the operation
// they carried.
//
// The buckets are mutually exclusive and sum to Total, and they hold two
// distinctions that are easy to conflate. A call is *recognized* when the
// adapter's allowlist produced a typed operation for it, which is everything
// but Unrecognized. A call is *attributable* only when it named a path, which
// is File alone: a shell command is recognized and still unattributable,
// because the command text is deliberately never recorded.
type Composition struct {
	// Total is every tool call observed, and is a lower bound on the calls the
	// agent made: a call rejected before it ran is never recorded.
	Total int

	// File counts operations that named a path, and equals the sum of the
	// operations reported across Profile.Paths.
	File int

	// Search, Shell and Subagent count recognized operations that name no
	// path. Search records a digested pattern and a root, which is a place to
	// look and not a resource that was read; Shell records only a digest of
	// the command; a subagent's own calls are recorded against the subagent.
	Search   int
	Shell    int
	Subagent int

	// Unrecognized counts calls Axiom cannot interpret: a tool outside the
	// metadata allowlist, an MCP tool, input it could not parse, an operation
	// whose shape this analysis does not know, and a file operation whose
	// outcome was never established. They are counted and never guessed about.
	Unrecognized int
}

// Path is the work observed at one path.
//
// Identity is the exact string the agent named, compared byte for byte. No
// normalization happens anywhere: resolving a relative path would mean
// trusting a working directory at a moment Axiom did not observe, and inventing
// identity is worse than reporting two rows.
//
// Counts are of operations, never of repetition. Work by two agents at one path
// adds up here because it happened there, which says nothing about whether any
// of it repeated anything.
type Path struct {
	Path string

	// Reads counts successful reads of a whole file and RangedReads successful
	// reads of part of one. They are apart because they acquire different
	// things: a later read of a different range is not the same operation.
	Reads       int
	RangedReads int

	// Writes and Edits count successful modifications. They stay apart here
	// even where a report shows their sum, because the record distinguishes
	// them and a model that folded them could not be unfolded later.
	Writes int
	Edits  int

	// Failed counts file operations at this path the agent reported failing.
	// A failed read is not established to have delivered the file's contents
	// and a failed edit may have applied in part, so neither is counted as a
	// successful read or a modification — but both were observed, and dropping
	// them would leave the path's operations irreconcilable with
	// Composition.File.
	//
	// Keeping them out of Reads, RangedReads and ReadBytes is a boundary on
	// what those describe, and not a claim that a failed read returned
	// nothing. The record says what became of a call and never what it
	// returned, so a failed read's output is unknown rather than known to be
	// empty.
	//
	// This is an observed failure and not the absence of an observed success.
	// An operation whose outcome was never established is not counted here, or
	// anywhere at this path: it is Unrecognized work, because Axiom cannot say
	// whether it did anything.
	Failed int

	// Turns is how many distinct turns the operations happened in, or nil when
	// any of them carried no turn identifier. A turn is identified together
	// with its session, because turn identifiers are the agent's own and are
	// not known to be unique beyond the session that issued them.
	Turns *int

	// ObservedTime sums the durations the agent reported for the operations
	// here, or is nil when any of them reported none.
	//
	// It is tool execution time and not elapsed time. Nested agents run in
	// parallel, so these sums can exceed the wall clock they happened in, and
	// nothing here measures the model.
	ObservedTime *time.Duration

	// ReadBytes is what the agent reported returning to the successful reads
	// of this path, whole-file and ranged together, or nil unless there was at
	// least one of them and every one was measured exactly once.
	//
	// A partial sum would understate the total while looking exactly like a
	// complete one, so nil means unknown and never zero. It counts bytes an
	// agent reported, not tokens, not cost, and not context.
	//
	// A failed read is outside this total and neither adds to it nor withholds
	// it, which describes the total's subject and not the failed read's
	// output.
	ReadBytes *int64
}

// Operations is every file operation observed at the path, successful or not.
// It is what the profile ranks by, and what makes the paths reconcile with
// Composition.File.
func (p Path) Operations() int {
	return p.Reads + p.RangedReads + p.Writes + p.Edits + p.Failed
}

// Profile is one pass of observed work.
type Profile struct {
	Operations Composition

	// Paths holds every path with work observed at it, ordered by operations
	// descending and then by path. It is complete: presenting only part of it
	// is a decision for a report, not for the analysis.
	Paths []Path

	// Reads counts the successful reads across Paths, whole-file and ranged,
	// and ReadsMeasured how many of them telemetry measured exactly once. The
	// pair is the denominator and numerator of what the byte totals cover.
	Reads         int
	ReadsMeasured int
}

// Accumulator builds a profile from events as they are read.
type Accumulator struct {
	lookup ByteLookup
	comp   Composition
	paths  map[string]*pathWork
}

// New returns an accumulator that measures reads with lookup, or leaves every
// byte total unknown when lookup is nil.
//
// The lookup must already hold every measurement before events are added:
// measurement is resolved as each read arrives, so that the accumulator keeps
// one entry per path instead of one per read.
func New(lookup ByteLookup) *Accumulator {
	return &Accumulator{lookup: lookup, paths: make(map[string]*pathWork)}
}

// turnKey identifies one turn. The session is part of it because a turn
// identifier is the agent's own and means nothing outside the session that
// issued it.
type turnKey struct {
	session string
	turn    string
}

type pathWork struct {
	reads, ranged, writes, edits, failed int

	turns      map[turnKey]struct{}
	turnsKnown bool

	total time.Duration
	timed bool

	bytes    int64
	measured int
}

// Add records one event. Anything that is not a completed tool call describes
// no work and contributes nothing.
func (a *Accumulator) Add(ev event.Event) {
	if ev.Type != event.TypeToolCall || ev.Tool == nil {
		return
	}
	a.comp.Total++

	m := ev.Tool.Metadata
	switch {
	case m == nil:
		a.comp.Unrecognized++
	case m.File != nil:
		// A file operation is attributable only if it named a path, an access
		// this analysis knows, and an outcome that was established. Anything
		// else is counted as work Axiom could not interpret rather than
		// attributed to an empty path, to a path whose operation has no
		// category, or to a path as work that may never have happened.
		if m.File.Path == "" || !known(m.File.Access) || !established(ev.Tool.Outcome) {
			a.comp.Unrecognized++
			return
		}
		a.comp.File++
		a.file(ev, m.File)
	case m.Shell != nil:
		a.comp.Shell++
	case m.Search != nil:
		a.comp.Search++
	case m.Subagent != nil:
		a.comp.Subagent++
	default:
		a.comp.Unrecognized++
	}
}

func known(access string) bool {
	switch access {
	case event.AccessRead, event.AccessWrite, event.AccessEdit:
		return true
	default:
		return false
	}
}

// established reports whether the record says what became of a call.
//
// Nothing validates this field on the way in or out of the log, so an outcome
// can arrive empty from a record written without one, or hold a value a later
// model added. Neither is a failure: an outcome that was not established says
// nothing at all, and reading it as failure would infer the worst from missing
// evidence.
func established(outcome event.Outcome) bool {
	switch outcome {
	case event.OutcomeSuccess, event.OutcomeFailure:
		return true
	default:
		return false
	}
}

func (a *Accumulator) file(ev event.Event, f *event.FileOp) {
	w, ok := a.paths[f.Path]
	if !ok {
		w = &pathWork{turns: make(map[turnKey]struct{}), turnsKnown: true, timed: true}
		a.paths[f.Path] = w
	}

	// Where and how long are properties of the call, so every operation
	// contributes to them whether or not it succeeded: a failed read still
	// happened in a turn and still took time.
	if ev.TurnID == "" {
		w.turnsKnown = false
	} else {
		w.turns[turnKey{session: ev.SessionID, turn: ev.TurnID}] = struct{}{}
	}
	if d := ev.Tool.DurationMS; d != nil {
		w.total += time.Duration(*d) * time.Millisecond
	} else {
		// A partial sum would understate the time without saying so.
		w.timed = false
	}

	// Both outcomes are named rather than one being the absence of the other.
	// Add has already established that the record says which of them happened.
	switch ev.Tool.Outcome {
	case event.OutcomeFailure:
		w.failed++
	case event.OutcomeSuccess:
		switch f.Access {
		case event.AccessRead:
			if f.Offset != nil || f.Limit != nil {
				w.ranged++
			} else {
				w.reads++
			}
			a.measure(ev, w)
		case event.AccessWrite:
			w.writes++
		case event.AccessEdit:
			w.edits++
		}
	}
}

// measure attaches what telemetry reported for one read.
//
// Only reads are measured. What an edit returns is the agent's confirmation of
// its own change, not repository content, and adding it to a total labelled as
// what reads returned would make the label false.
func (a *Accumulator) measure(ev event.Event, w *pathWork) {
	if a.lookup == nil || ev.Tool.InvocationID == "" {
		return
	}
	b, ok := a.lookup(ev.SessionID, ev.TurnID, ev.Tool.InvocationID)
	if !ok {
		return
	}
	w.bytes += b
	w.measured++
}

// Profile summarizes everything added so far. It does not consume the
// accumulator: adding more events and profiling again is valid.
func (a *Accumulator) Profile() Profile {
	out := Profile{Operations: a.comp, Paths: make([]Path, 0, len(a.paths))}

	for path, w := range a.paths {
		p := Path{
			Path:        path,
			Reads:       w.reads,
			RangedReads: w.ranged,
			Writes:      w.writes,
			Edits:       w.edits,
			Failed:      w.failed,
		}
		if w.turnsKnown {
			turns := len(w.turns)
			p.Turns = &turns
		}
		if w.timed {
			total := w.total
			p.ObservedTime = &total
		}

		reads := w.reads + w.ranged
		if reads > 0 && w.measured == reads {
			bytes := w.bytes
			p.ReadBytes = &bytes
		}
		out.Reads += reads
		out.ReadsMeasured += w.measured

		out.Paths = append(out.Paths, p)
	}

	slices.SortFunc(out.Paths, compare)
	return out
}

// compare orders the busiest path first, and settles ties by path so that two
// runs over one log cannot disagree.
//
// Ranking is by operations and never by bytes or time, which would turn a
// measurement into a judgement about which work mattered.
func compare(a, b Path) int {
	if c := cmp.Compare(b.Operations(), a.Operations()); c != 0 {
		return c
	}
	return cmp.Compare(a.Path, b.Path)
}
