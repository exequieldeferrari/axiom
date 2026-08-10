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
}

// run is an uninterrupted sequence of the same operation. A run ends as soon
// as anything happens that could make repeating the operation worthwhile.
type run struct {
	subject string
	count   int
	first   time.Time
	last    time.Time
	total   time.Duration
	timed   bool
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

func (p *Profiler) observe(ev event.Event) {
	sc := p.scope(ev)
	o := classify(ev.Tool)
	succeeded := ev.Tool.Outcome == event.OutcomeSuccess

	switch o.kind {
	case opRead:
		// A failed read returns nothing, so it neither repeats earlier work
		// nor makes later work redundant.
		if succeeded {
			sc.reads.extend(o.subject, ev)
		}

	case opModify:
		// A failed edit may still have applied in part, so both outcomes end
		// the runs they could have invalidated. Only this path's reads are
		// affected; writing one file says nothing about another.
		p.end(sc, sc.reads, KindRepeatedRead, o.subject)
		p.endAll(sc, sc.shell, KindRepeatedShell)

	case opShell:
		// Shell effects are unobservable: the command text is never recorded,
		// so anything could have changed underneath.
		p.endAll(sc, sc.reads, KindRepeatedRead)
		for other := range sc.shell {
			if other != o.subject {
				p.end(sc, sc.shell, KindRepeatedShell, other)
			}
		}
		if !succeeded {
			// Running a command again after it failed is a retry.
			p.end(sc, sc.shell, KindRepeatedShell, o.subject)
			return
		}
		sc.shell.extend(o.subject, ev)

	case opObserve:
		// Searches and partial reads look at state without changing it.

	case opOpaque:
		p.endAll(sc, sc.reads, KindRepeatedRead)
		p.endAll(sc, sc.shell, KindRepeatedShell)
	}
}

func (p *Profiler) scope(ev event.Event) *scope {
	key := scopeKey{session: ev.SessionID, subagent: ev.SubagentID}
	sc, ok := p.scopes[key]
	if !ok {
		sc = &scope{key: key, reads: make(runs), shell: make(runs)}
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
	return out
}

type runs map[string]*run

func (rs runs) extend(subject string, ev event.Event) {
	r, ok := rs[subject]
	if !ok {
		rs[subject] = &run{
			subject: subject,
			count:   1,
			first:   ev.Timestamp,
			last:    ev.Timestamp,
			timed:   true,
		}
		return
	}

	r.count++
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

func (r *run) finding(kind Kind, key scopeKey) (Finding, bool) {
	if r.count < 2 {
		return Finding{}, false
	}

	f := Finding{
		Kind:        kind,
		Confidence:  ConfidenceHigh,
		SessionID:   key.session,
		SubagentID:  key.subagent,
		Occurrences: r.count,
		Redundant:   r.count - 1,
		First:       r.first,
		Last:        r.last,
	}
	if r.timed {
		total := r.total
		f.ObservedTotal = &total
	}
	switch kind {
	case KindRepeatedRead:
		f.Path = r.subject
	case KindRepeatedShell:
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
