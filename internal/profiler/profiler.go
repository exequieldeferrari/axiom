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
	// open holds, per command, the intervals of the failure sequences already
	// reported for it that no success has frozen yet. It is how a command
	// observed succeeding later reaches the findings produced for it.
	open map[string][]*openInterval

	// now is everything the scope has recorded so far. An interval is the
	// difference between two of these, which is what keeps interval
	// accounting to a fixed cost per event.
	now observation
	// lastTurn is the turn the previous recorded call reported, which may be
	// none, and seenCall says whether there was a previous call at all. A
	// turn is never compared across scopes.
	lastTurn string
	seenCall bool
	// snapshotAfter names the command whose open sequence should take its
	// interval start once the call being observed has been counted. An
	// interval begins after the attempt, so the snapshot cannot be taken
	// while the attempt is still being recorded.
	snapshotAfter string
}

// observation is the running total of what one scope has recorded.
//
// Everything here is cumulative and monotonic, so subtracting an earlier copy
// from a later one describes exactly the calls recorded between them. That is
// the only mechanism available: the calls themselves are not retained.
type observation struct {
	operations                                                         int
	wholeReads, rangedReads, searches, shell, subagents, uninterpreted int
	writes, edits                                                      Outcomes
	// turnChanges counts transitions between consecutive recorded calls where
	// both reported a turn and the two differed.
	turnChanges int
	// unsettledTurns counts transitions where at least one of the two calls
	// reported no turn, which establishes neither a boundary nor its absence.
	//
	// Both are counted per transition rather than per call, which is what
	// makes the two ends of an interval behave alike. See scope.track.
	unsettledTurns int
}

// since describes the calls recorded between an earlier observation and this
// one. Paths are added by the caller, which is the only part of an interval
// that is not a difference of counters.
func (o observation) since(start observation) Interval {
	iv := Interval{
		Operations:    o.operations - start.operations,
		WholeReads:    o.wholeReads - start.wholeReads,
		RangedReads:   o.rangedReads - start.rangedReads,
		Searches:      o.searches - start.searches,
		Shell:         o.shell - start.shell,
		Subagents:     o.subagents - start.subagents,
		Uninterpreted: o.uninterpreted - start.uninterpreted,
		Writes:        o.writes.since(start.writes),
		Edits:         o.edits.since(start.edits),
	}

	// An unestablished boundary is reported ahead of an observed one: a call
	// with no turn leaves the whole question open, and answering it from the
	// calls that happened to carry one would state more than the record does.
	switch {
	case o.unsettledTurns > start.unsettledTurns:
		iv.TurnBoundary = TurnBoundaryUnknown
	case o.turnChanges > start.turnChanges:
		iv.TurnBoundary = TurnBoundaryRecorded
	default:
		iv.TurnBoundary = TurnBoundaryNone
	}
	return iv
}

func (o Outcomes) since(start Outcomes) Outcomes {
	return Outcomes{
		Succeeded:     o.Succeeded - start.Succeeded,
		Failed:        o.Failed - start.Failed,
		Unestablished: o.Unestablished - start.Unestablished,
	}
}

// maxIntervalPaths bounds the write and edit paths one interval retains. The
// counts above them stay complete, and the paths left out are counted, so the
// bound costs detail and never accuracy.
const maxIntervalPaths = 5

// openInterval accumulates the interval of one reported finding until a
// success freezes it.
type openInterval struct {
	// finding is the position of the finding in Profiler.closed, which only
	// grows, so the index stays valid.
	finding int
	start   observation
	// seen holds every distinct write or edit path recorded since start, and
	// order holds the bounded prefix of it that the finding will carry. Paths
	// are kept rather than the calls that named them, so what grows is the
	// number of distinct files written, not the length of the interval.
	seen  map[string]struct{}
	order []string
}

// note records a write or edit at a path.
func (iv *openInterval) note(path string) {
	if _, dup := iv.seen[path]; dup {
		return
	}
	iv.seen[path] = struct{}{}
	if len(iv.order) < maxIntervalPaths {
		iv.order = append(iv.order, path)
	}
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
	// start is the scope as it stood immediately after the last attempt
	// recorded in this run, which is where the run's interval begins.
	//
	// It is taken at the attempt rather than where the run closes, because a
	// run closes at the first barrier and operations that are not barriers
	// can be recorded before one arrives. Taking it at the close would drop
	// exactly those, and nothing recorded afterwards can recover them.
	start observation
	// reporting is what every attempt so far was observed reporting about its
	// failure, and reports is whether those reports came out the same. They
	// are separate questions asked of the same text, and an attempt can
	// settle one while leaving the other open.
	reporting FailureReporting
	reports   ReportIdentity
	// digest is the report every attempt so far produced, kept only to
	// compare the next one against.
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
		// A start arriving for a session already under way means the agent's
		// context was reset, so earlier operations are no longer part of it.
		// Compaction was observed doing that under the same session
		// identifier, on /compact and automatically. A /clear reports a new
		// identifier instead, which puts the work in a different scope
		// already.
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
	// The turn is tracked before anything can freeze an interval, because the
	// success that freezes one bounds it: a boundary between the last attempt
	// and that success falls inside the interval, even where nothing at all
	// was recorded between them.
	sc.track(ev)
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
				p.endFailure(sc, other)
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
			p.endFailure(sc, o.subject)
		}

	case opObserve:
		// Searches and partial reads look at state without changing it.

	case opOpaque:
		p.endAll(sc, sc.reads, KindRepeatedRead)
		p.endAll(sc, sc.shell, KindRepeatedShell)
		p.endFailures(sc)
	}

	// Counted last, so that a call is outside the intervals it bounds: the
	// attempt an interval starts after, and the success it ends before, are
	// neither of them work recorded between the two.
	sc.record(ev)
	if sc.snapshotAfter != "" {
		if fr, ok := sc.failures[sc.snapshotAfter]; ok {
			fr.start = sc.now
		}
		sc.snapshotAfter = ""
	}
}

// track follows the turn from one recorded call to the next.
//
// The question an interval answers is asked of the closed span running from
// the last attempt, through everything recorded after it, to the success that
// bounds it. Both ends of that span are part of the question: a boundary
// between the last attempt and the success falls inside the interval even
// where nothing at all was recorded between them.
//
// So it is counted per transition rather than per call, which is what makes
// the two ends behave alike. The transition out of the attempt is counted
// after the start snapshot is taken, and the transition into the success
// before the interval freezes, so exactly the transitions of the closed span
// fall in the difference. A turn identifier missing at either end leaves the
// same question open as one missing in between, and neither end can be
// compared against a call outside the span.
func (sc *scope) track(ev event.Event) {
	if !sc.seenCall {
		sc.seenCall = true
		sc.lastTurn = ev.TurnID
		return
	}

	switch {
	case sc.lastTurn == "" || ev.TurnID == "":
		// Neither a boundary nor the absence of one. Without an identifier on
		// both sides there is nothing to compare, and carrying the last turn
		// past the gap would compare two calls that were never adjacent.
		sc.now.unsettledTurns++
	case sc.lastTurn != ev.TurnID:
		sc.now.turnChanges++
	}
	sc.lastTurn = ev.TurnID
}

// record adds one call to what the scope has recorded, and to the paths of
// every interval still open.
func (sc *scope) record(ev event.Event) {
	sc.now.operations++

	switch sh := shapeOf(ev.Tool); sh {
	case shapeWholeRead:
		sc.now.wholeReads++
	case shapeRangedRead:
		sc.now.rangedReads++
	case shapeSearch:
		sc.now.searches++
	case shapeShell:
		sc.now.shell++
	case shapeSubagent:
		sc.now.subagents++
	case shapeWrite, shapeEdit:
		counts := &sc.now.writes
		if sh == shapeEdit {
			counts = &sc.now.edits
		}
		switch {
		case observedSuccess(ev.Tool):
			counts.Succeeded++
		case observedFailure(ev.Tool):
			counts.Failed++
		default:
			counts.Unestablished++
		}
		for _, intervals := range sc.open {
			for _, iv := range intervals {
				iv.note(ev.Tool.Metadata.File.Path)
			}
		}
	default:
		sc.now.uninterpreted++
	}
}

// attempt records one failed run of a shell command.
func (p *Profiler) attempt(sc *scope, ev event.Event, subject string) {
	f := ev.Tool.Failure

	// An interrupted call was stopped by a person. What the agent does next
	// answers that interruption, so the sequence ends and the attempt starts
	// no new one.
	if f != nil && f.Kind == event.FailureKindInterrupt {
		p.endFailure(sc, subject)
		return
	}

	// Without a turn identifier there is no boundary to respect, and an agent
	// that reports none leaves Axiom unable to tell a repetition from an
	// instruction it never saw. The finding is withheld rather than guessed.
	if ev.TurnID == "" {
		p.endFailure(sc, subject)
		return
	}

	fr, ok := sc.failures[subject]
	if ok && fr.turn != ev.TurnID {
		p.endFailure(sc, subject)
		ok = false
	}
	if ok {
		fr.extend(ev)
		fr.narrow(f)
	} else {
		sc.failures[subject] = newFailureRun(subject, ev)
	}
	// Whatever this run turns out to contain, its interval begins after the
	// attempt recorded now. Each further attempt moves the start, so the run
	// ends up holding the one the last attempt left.
	sc.snapshotAfter = subject
}

// succeeded records that a command ran without failing, which ends its
// sequence of failed attempts and is worth noting on the sequences already
// reported for it.
func (p *Profiler) succeeded(sc *scope, subject string) {
	p.endFailure(sc, subject)

	for _, iv := range sc.open[subject] {
		f := &p.closed[iv.finding]
		f.LaterSuccess = true

		interval := sc.now.since(iv.start)
		interval.Paths = iv.order
		interval.OmittedPaths = len(iv.seen) - len(iv.order)
		f.Interval = &interval
	}
	// The first success is what the interval is bounded by, so the findings
	// it froze are dropped here and no later success can reach them again.
	delete(sc.open, subject)
}

// endFailure closes the open sequence of failed attempts of one command.
func (p *Profiler) endFailure(sc *scope, subject string) {
	fr, ok := sc.failures[subject]
	if !ok {
		return
	}
	delete(sc.failures, subject)

	f, ok := fr.finding(sc.key)
	if !ok {
		return
	}
	sc.open[subject] = append(sc.open[subject], &openInterval{
		finding: len(p.closed),
		start:   fr.start,
		seen:    make(map[string]struct{}),
	})
	p.closed = append(p.closed, f)
}

func (p *Profiler) endFailures(sc *scope) {
	for subject := range sc.failures {
		p.endFailure(sc, subject)
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
			open:     make(map[string][]*openInterval),
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
	fr := &failureRun{
		run:       newRun(subject, ev),
		turn:      ev.TurnID,
		reporting: reportingOf(ev.Tool.Failure),
		reports:   ReportsUnestablished,
	}
	if f := ev.Tool.Failure; f != nil {
		if f.Digest != "" {
			fr.digest = f.Digest
			fr.reports = ReportsIdentical
		}
		if f.ExitCode != nil {
			code := *f.ExitCode
			fr.exitCode = &code
		}
	}
	return fr
}

// narrow folds one more attempt into what the sequence has established about
// its attempts.
//
// Every question here only ever weakens, and none of them can be re-formed
// once lost. An attempt that reports nothing is not agreement with an attempt
// that did.
func (fr *failureRun) narrow(f *event.Failure) {
	fr.reporting = narrowReporting(fr.reporting, reportingOf(f))
	fr.narrowIdentity(f)
	if f == nil || f.ExitCode == nil || fr.exitCode == nil || *f.ExitCode != *fr.exitCode {
		fr.exitCode = nil
	}
}

// narrowIdentity folds in whether this attempt reported what the ones before
// it did.
//
// A missing report is not a differing one. Without two reports there is
// nothing to compare, so an attempt that reported none leaves the question
// open however the others came out, and that is not the same observation as
// reports known to have differed.
func (fr *failureRun) narrowIdentity(f *event.Failure) {
	if fr.reports == ReportsUnestablished {
		return
	}
	if f == nil || f.Digest == "" {
		fr.reports, fr.digest = ReportsUnestablished, ""
		return
	}
	if f.Digest != fr.digest {
		fr.reports = ReportsDiffered
	}
}

// reportingOf is what one attempt's record establishes about its failure
// report.
//
// Everything this version cannot place lands in the state that admits it: a
// report the adapter could not classify, a record written before the
// classification existed, and a value some later adapter may add. None of them
// is evidence that the attempt reported nothing.
func reportingOf(f *event.Failure) FailureReporting {
	if f == nil {
		return FailureReportingUnestablished
	}
	switch f.Reporting {
	case event.ReportingDetail:
		return FailureReportingDetail
	case event.ReportingStatusOnly:
		return FailureReportingStatusOnly
	case event.ReportingNoText:
		return FailureReportingNoText
	default:
		return FailureReportingUnestablished
	}
}

// narrowReporting folds one attempt's classification into the sequence's.
//
// An attempt that could not be placed leaves the whole run unplaced, because a
// run cannot be said to report alike when one of its reports was never read.
// Two attempts placed differently are mixed, which is neither of them and is
// deliberately not collapsed into either.
func narrowReporting(sofar, next FailureReporting) FailureReporting {
	switch {
	case sofar == FailureReportingUnestablished || next == FailureReportingUnestablished:
		return FailureReportingUnestablished
	case sofar == next:
		return sofar
	default:
		return FailureReportingMixed
	}
}

// finding reports the sequence, if there was anything repeated about it.
func (fr *failureRun) finding(key scopeKey) (Finding, bool) {
	f, ok := fr.run.finding(KindRepeatedFailure, key)
	if !ok {
		return Finding{}, false
	}

	f.Reporting = fr.reporting
	f.Reports = fr.reports
	f.ExitCode = fr.exitCode
	return f, true
}

func (r *run) finding(kind Kind, key scopeKey) (Finding, bool) {
	if len(r.calls) < 2 {
		return Finding{}, false
	}

	f := Finding{
		Kind:       kind,
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

// shape is what an interval counts a recorded call as.
type shape int

const (
	// shapeUninterpreted is a call this version cannot describe.
	shapeUninterpreted shape = iota
	shapeWholeRead
	shapeRangedRead
	shapeSearch
	shapeShell
	shapeWrite
	shapeEdit
	shapeSubagent
)

// shapeOf reduces a tool call to the shape of the operation it carried.
//
// This asks a different question from classify, which says whether an
// operation could have made repeating an earlier one worthwhile. Answering
// both at once would make one of them wrong: classify deliberately treats a
// background command, a subagent and an unrecognised tool alike, because their
// effects are equally unbounded, while an interval has to tell them apart to
// describe what was recorded. A file operation that named no path is counted
// as uninterpreted for the same reason it is elsewhere, that there is nothing
// to attribute it to.
//
// The shape is independent of what became of the call. A count of reads is a
// count of read calls that reached the log, not of files the agent can be
// shown to have obtained; only writes and edits carry their outcomes, because
// what those leave behind is the part of an interval that could have persisted.
func shapeOf(t *event.ToolCall) shape {
	m := t.Metadata
	switch {
	case m == nil:
		return shapeUninterpreted

	case m.File != nil:
		if m.File.Path == "" {
			return shapeUninterpreted
		}
		switch m.File.Access {
		case event.AccessRead:
			if m.File.Offset != nil || m.File.Limit != nil {
				return shapeRangedRead
			}
			return shapeWholeRead
		case event.AccessWrite:
			return shapeWrite
		case event.AccessEdit:
			return shapeEdit
		default:
			return shapeUninterpreted
		}

	case m.Shell != nil:
		// A background command is still a recorded command. Its effects are
		// unbounded, which is what makes it a barrier elsewhere, but that is
		// a statement about what it could have done and not about what it was.
		return shapeShell

	case m.Search != nil:
		return shapeSearch

	case m.Subagent != nil:
		return shapeSubagent

	default:
		return shapeUninterpreted
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
