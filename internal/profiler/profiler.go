package profiler

import (
	"cmp"
	"slices"
	"time"

	"github.com/exequieldeferrari/axiom/internal/event"
)

// Profiler accumulates events and reports repeated work.
//
// Events are fed in the order they were recorded. State is kept per context
// scope rather than per log, so a log holding many interleaved sessions costs
// no more than the sessions currently open in it.
type Profiler struct {
	events    int
	toolCalls int
	sessions  map[string]struct{}
	scopes    map[scopeKey]*scope
	closed    []Finding
}

// New returns a profiler with no observations.
func New() *Profiler {
	return &Profiler{
		sessions: make(map[string]struct{}),
		scopes:   make(map[scopeKey]*scope),
	}
}

// scopeKey identifies one context window.
//
// Repetition is only meaningful inside a single context. A later session may
// legitimately redo work because the agent no longer remembers it, and a
// subagent reasons in a context of its own, so both are analyzed separately.
type scopeKey struct {
	session  string
	subagent string
}

type scope struct {
	key   scopeKey
	reads runs
	shell runs
	// failures holds the open sequence of failed attempts of each command.
	failures map[string]*failureRun
	// reported records where in the closed findings each command's failure
	// sequences ended up, so that a command observed succeeding later can be
	// noted on the findings that were already produced for it.
	reported map[string][]int
}

// run is an uninterrupted sequence of the same operation. A run ends as soon
// as anything happens that could make repeating the operation worthwhile.
type run struct {
	subject string
	// calls holds one entry per occurrence, in the order observed, and so is
	// also the occurrence count.
	calls []Call
	first time.Time
	last  time.Time
	total time.Duration
	timed bool
}

// failureRun is an uninterrupted sequence of attempts of the same command that
// all failed, within one turn.
//
// The sequence is confined to a turn because the claim it supports is about
// the agent repeating itself with nothing to go on. A turn boundary is where
// input Axiom cannot see may have arrived, and an attempt made because someone
// asked for it again is not the same behavior at all. The redundancy runs
// carry no such risk and are deliberately left free to cross turns: that work
// was already done and unchanged whoever asked for it.
type failureRun struct {
	run
	turn string
	// digest is the failure every attempt so far reported, and is empty as
	// soon as one of them reports a different failure or none. An empty
	// digest is never a valid failure, so agreement can never be re-formed
	// once it is lost.
	digest string
	// exitCode is the status every attempt so far exited with, on the same
	// terms. It is tracked separately because an agent may report one without
	// the other.
	exitCode *int
}

// Add records one event.
func (p *Profiler) Add(ev event.Event) {
	p.events++
	if ev.SessionID != "" {
		p.sessions[ev.SessionID] = struct{}{}
	}

	switch ev.Type {
	case event.TypeSessionStart:
		// Claude Code starts a session on compaction and on /clear. One
		// arriving for a session already under way means the agent's context
		// was reset, so earlier operations are no longer part of it.
		p.reset(ev.SessionID)
	case event.TypeToolCall:
		if ev.Tool != nil {
			p.toolCalls++
			p.observe(ev)
		}
	}
}

// Report summarizes everything added so far. It does not consume the
// profiler: adding more events and reporting again is valid.
func (p *Profiler) Report() Report {
	findings := slices.Clone(p.closed)
	for _, sc := range p.scopes {
		findings = append(findings, sc.pending()...)
	}
	slices.SortFunc(findings, compare)

	return Report{
		Events:    p.events,
		Sessions:  len(p.sessions),
		ToolCalls: p.toolCalls,
		Findings:  findings,
	}
}

// observedSuccess and observedFailure establish an outcome positively, and one
// is never the absence of the other.
//
// Nothing validates this field on the way into or out of the log, so a record
// can arrive carrying no outcome, and a later model may add a state this version
// does not know. A test of "not success" would read both of those as failures,
// which is the worst inference available from missing evidence. An outcome that
// was not established is evidence of nothing.
func observedSuccess(t *event.ToolCall) bool { return t.Outcome == event.OutcomeSuccess }
func observedFailure(t *event.ToolCall) bool { return t.Outcome == event.OutcomeFailure }

func (p *Profiler) observe(ev event.Event) {
	sc := p.scope(ev)
	o := classify(ev.Tool)

	switch o.kind {
	case opRead:
		// A read repeats earlier work, or makes later work redundant, only
		// if the agent reported it succeeding. A read reported as failed is
		// not established to have delivered the file's contents, and neither
		// is one whose outcome was never established: the record says what
		// became of a call, never what it returned.
		if observedSuccess(ev.Tool) {
			sc.reads.extend(o.subject, ev)
		}

	case opModify:
		// A failed edit may still have applied in part, so both outcomes end
		// the runs they could have invalidated. Only this path's reads are
		// affected; writing one file says nothing about another.
		p.end(sc, sc.reads, KindRepeatedRead, o.subject)
		p.endAll(sc, sc.shell, KindRepeatedShell)
		// Any change to the tree is a reason for the next attempt to behave
		// differently, whichever file it touched.
		p.endFailures(sc)

	case opShell:
		// Shell effects are unobservable: the command text is never recorded,
		// so anything could have changed underneath.
		p.endAll(sc, sc.reads, KindRepeatedRead)
		for other := range sc.shell {
			if other != o.subject {
				p.end(sc, sc.shell, KindRepeatedShell, other)
			}
		}
		for other := range sc.failures {
			if other != o.subject {
				p.endFailure(sc, other, false)
			}
		}
		switch {
		case observedFailure(ev.Tool):
			// Running a command again after it failed is a retry, which is
			// not redundancy but is a finding of its own.
			p.end(sc, sc.shell, KindRepeatedShell, o.subject)
			p.attempt(sc, ev, o.subject)

		case observedSuccess(ev.Tool):
			p.succeeded(sc, o.subject)
			sc.shell.extend(o.subject, ev)

		default:
			// A record that does not say what became of the call is evidence
			// of neither outcome. It can no more show a command failing again
			// than show it getting past a failure, so it ends both sequences
			// it could have belonged to and starts neither: the run of
			// executions, because this one cannot be shown to have done what
			// the others did, and the sequence of failed attempts, without the
			// success that would have been noted on it.
			p.end(sc, sc.shell, KindRepeatedShell, o.subject)
			p.endFailure(sc, o.subject, false)
		}

	case opObserve:
		// Searches and partial reads look at state without changing it.

	case opOpaque:
		p.endAll(sc, sc.reads, KindRepeatedRead)
		p.endAll(sc, sc.shell, KindRepeatedShell)
		p.endFailures(sc)
	}
}

// attempt records one failed run of a shell command.
func (p *Profiler) attempt(sc *scope, ev event.Event, subject string) {
	f := ev.Tool.Failure

	// An interrupted call was stopped by a person. What the agent does next
	// answers that interruption, so the sequence ends and the attempt starts
	// no new one.
	if f != nil && f.Kind == event.FailureKindInterrupt {
		p.endFailure(sc, subject, false)
		return
	}

	// Without a turn identifier there is no boundary to respect, and an agent
	// that reports none leaves Axiom unable to tell a repetition from an
	// instruction it never saw. The finding is withheld rather than guessed.
	if ev.TurnID == "" {
		p.endFailure(sc, subject, false)
		return
	}

	fr, ok := sc.failures[subject]
	if ok && fr.turn != ev.TurnID {
		p.endFailure(sc, subject, false)
		ok = false
	}
	if !ok {
		sc.failures[subject] = newFailureRun(subject, ev)
		return
	}
	fr.extend(ev)
	fr.agree(f)
}

// succeeded records that a command ran without failing, which ends its
// sequence of failed attempts and is worth noting on the sequences already
// reported for it.
func (p *Profiler) succeeded(sc *scope, subject string) {
	p.endFailure(sc, subject, true)
	for _, i := range sc.reported[subject] {
		p.closed[i].LaterSuccess = true
	}
}

// endFailure closes the open sequence of failed attempts of one command.
func (p *Profiler) endFailure(sc *scope, subject string, succeeded bool) {
	fr, ok := sc.failures[subject]
	if !ok {
		return
	}
	delete(sc.failures, subject)

	f, ok := fr.finding(sc.key)
	if !ok {
		return
	}
	f.LaterSuccess = succeeded
	sc.reported[subject] = append(sc.reported[subject], len(p.closed))
	p.closed = append(p.closed, f)
}

func (p *Profiler) endFailures(sc *scope) {
	for subject := range sc.failures {
		p.endFailure(sc, subject, false)
	}
}

func (p *Profiler) scope(ev event.Event) *scope {
	key := scopeKey{session: ev.SessionID, subagent: ev.SubagentID}
	sc, ok := p.scopes[key]
	if !ok {
		sc = &scope{
			key:      key,
			reads:    make(runs),
			shell:    make(runs),
			failures: make(map[string]*failureRun),
			reported: make(map[string][]int),
		}
		p.scopes[key] = sc
	}
	return sc
}

// reset ends every run in a session whose context was discarded.
func (p *Profiler) reset(session string) {
	for key, sc := range p.scopes {
		if key.session != session {
			continue
		}
		p.endAll(sc, sc.reads, KindRepeatedRead)
		p.endAll(sc, sc.shell, KindRepeatedShell)
		p.endFailures(sc)
		delete(p.scopes, key)
	}
}

func (p *Profiler) end(sc *scope, rs runs, kind Kind, subject string) {
	r, ok := rs[subject]
	if !ok {
		return
	}
	delete(rs, subject)
	if f, ok := r.finding(kind, sc.key); ok {
		p.closed = append(p.closed, f)
	}
}

func (p *Profiler) endAll(sc *scope, rs runs, kind Kind) {
	for subject := range rs {
		p.end(sc, rs, kind, subject)
	}
}

// pending reports the findings of runs that were still open at the end of the
// log, without ending them.
func (sc *scope) pending() []Finding {
	var out []Finding
	for _, r := range sc.reads {
		if f, ok := r.finding(KindRepeatedRead, sc.key); ok {
			out = append(out, f)
		}
	}
	for _, r := range sc.shell {
		if f, ok := r.finding(KindRepeatedShell, sc.key); ok {
			out = append(out, f)
		}
	}
	// A sequence of failed attempts still open at the end of the log has, by
	// definition, not been observed succeeding.
	for _, fr := range sc.failures {
		if f, ok := fr.finding(sc.key); ok {
			out = append(out, f)
		}
	}
	return out
}

type runs map[string]*run

func (rs runs) extend(subject string, ev event.Event) {
	r, ok := rs[subject]
	if !ok {
		started := newRun(subject, ev)
		rs[subject] = &started
		return
	}
	r.extend(ev)
}

func newRun(subject string, ev event.Event) run {
	return run{
		subject: subject,
		calls:   []Call{call(ev)},
		first:   ev.Timestamp,
		last:    ev.Timestamp,
		timed:   true,
	}
}

// extend adds one more occurrence to a run already under way. The first
// occurrence's duration is deliberately not part of the total: it did the work
// the rest repeated.
func (r *run) extend(ev event.Event) {
	r.calls = append(r.calls, call(ev))
	// Hooks run as parallel processes, so a later event can carry an earlier
	// timestamp. Widening the window keeps it from reading backwards.
	if ev.Timestamp.Before(r.first) {
		r.first = ev.Timestamp
	}
	if ev.Timestamp.After(r.last) {
		r.last = ev.Timestamp
	}
	if d := ev.Tool.DurationMS; d != nil {
		r.total += time.Duration(*d) * time.Millisecond
	} else {
		// A partial sum would understate the time without saying so.
		r.timed = false
	}
}

func call(ev event.Event) Call {
	return Call{TurnID: ev.TurnID, InvocationID: ev.Tool.InvocationID}
}

func newFailureRun(subject string, ev event.Event) *failureRun {
	fr := &failureRun{run: newRun(subject, ev), turn: ev.TurnID}
	if f := ev.Tool.Failure; f != nil {
		fr.digest = f.Digest
		if f.ExitCode != nil {
			code := *f.ExitCode
			fr.exitCode = &code
		}
	}
	return fr
}

// agree narrows what every attempt in the sequence has reported in common.
//
// Agreement only ever weakens. An attempt that reports nothing is not
// agreement with an attempt that did: the two are not known to be alike, which
// is the same position as knowing they differ.
func (fr *failureRun) agree(f *event.Failure) {
	if f == nil || f.Digest == "" || f.Digest != fr.digest {
		fr.digest = ""
	}
	if f == nil || f.ExitCode == nil || fr.exitCode == nil || *f.ExitCode != *fr.exitCode {
		fr.exitCode = nil
	}
}

// finding reports the sequence, if there was anything repeated about it.
func (fr *failureRun) finding(key scopeKey) (Finding, bool) {
	f, ok := fr.run.finding(KindRepeatedFailure, key)
	if !ok {
		return Finding{}, false
	}

	// The repetition is established either way. What separates the levels is
	// whether the failures themselves are known to be alike: identical
	// reported failures leave nothing to the reader's imagination, while
	// failures Axiom cannot compare leave open that each attempt met something
	// different.
	f.Confidence = ConfidenceMedium
	if fr.digest != "" {
		f.Confidence = ConfidenceHigh
		f.FailureDigest = fr.digest
	}
	f.ExitCode = fr.exitCode
	return f, true
}

func (r *run) finding(kind Kind, key scopeKey) (Finding, bool) {
	if len(r.calls) < 2 {
		return Finding{}, false
	}

	f := Finding{
		Kind:       kind,
		Confidence: ConfidenceHigh,
		SessionID:  key.session,
		SubagentID: key.subagent,
		// Both counts come from the occurrence list so that they cannot
		// disagree with the identities reported alongside them.
		Occurrences: len(r.calls),
		Redundant:   len(r.calls) - 1,
		// A run may be reported while it is still open, so the finding gets
		// its own copy rather than a view that later occurrences could grow.
		Calls: slices.Clone(r.calls),
		First: r.first,
		Last:  r.last,
	}
	if r.timed {
		total := r.total
		f.ObservedTotal = &total
	}
	switch kind {
	case KindRepeatedRead:
		f.Path = r.subject
	case KindRepeatedShell, KindRepeatedFailure:
		f.CommandDigest = r.subject
	}
	return f, true
}

type opKind int

const (
	// opOpaque is an operation whose effects Axiom cannot observe.
	opOpaque opKind = iota
	// opObserve inspects state without changing it.
	opObserve
	opRead
	opModify
	opShell
)

type op struct {
	kind    opKind
	subject string
}

// classify reduces a tool call to what it means for repeated work.
//
// Anything Axiom does not recognise is opaque rather than harmless. Metadata
// extraction is an allowlist, so an unrecognised tool may well have edited a
// file, and treating it as harmless is how a legitimate re-read would be
// reported as waste.
func classify(t *event.ToolCall) op {
	m := t.Metadata
	switch {
	case m == nil:
		return op{kind: opOpaque}

	case m.File != nil:
		switch m.File.Access {
		case event.AccessRead:
			// A ranged read returns part of a file, so a later read of a
			// different range is not the same operation. Ranges are rare and
			// comparing them adds a class of mistake worth avoiding.
			if m.File.Offset != nil || m.File.Limit != nil {
				return op{kind: opObserve}
			}
			return op{kind: opRead, subject: m.File.Path}
		case event.AccessWrite, event.AccessEdit:
			return op{kind: opModify, subject: m.File.Path}
		default:
			return op{kind: opOpaque}
		}

	case m.Shell != nil:
		// A background command keeps running after the call returns, so what
		// happened between two of them cannot be bounded.
		if m.Shell.Background {
			return op{kind: opOpaque}
		}
		return op{kind: opShell, subject: m.Shell.CommandDigest}

	case m.Search != nil:
		return op{kind: opObserve}

	default:
		// Subagents included: a nested agent can do anything, and its own
		// tool calls are recorded against a different scope.
		return op{kind: opOpaque}
	}
}

func compare(a, b Finding) int {
	if c := a.First.Compare(b.First); c != 0 {
		return c
	}
	if c := a.Last.Compare(b.Last); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Kind, b.Kind); c != 0 {
		return c
	}
	if c := cmp.Compare(a.subject(), b.subject()); c != 0 {
		return c
	}
	return cmp.Compare(a.SessionID, b.SessionID)
}

func (f Finding) subject() string {
	if f.Path != "" {
		return f.Path
	}
	return f.CommandDigest
}
