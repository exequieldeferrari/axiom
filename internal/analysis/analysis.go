// Package analysis reads one recorded log and hands back what the domain
// packages made of it.
//
// It exists because two commands need the same pass. A profile renders one
// log; a comparison analyzes two and renders the differences between them.
// Before this package the pass lived inside the profile command, and a second
// caller would have had to repeat it — at which point the two would eventually
// disagree about something as ordinary as whether a record is a tool call.
//
// # This is a container, not a report
//
// Nothing here is a new measurement. Every field below is a report type an
// existing package already owns and already documents, carried unchanged.
// There is deliberately no combined total, no cross-field derivation and no
// vocabulary of its own: an analysis that summarized these would be a new
// semantic layer, and the semantics belong to the packages that produced them.
//
// The one thing assembled here is Composition, and even that is not counted
// here: internal/work owns the classification and the counting, and this
// package only decides which records to hand it, by the same test every
// accumulator below applies.
//
// # Selection
//
// A session is selected exactly, by the identifier the agent recorded, which
// is the rule internal/cli already applied and the only one that cannot
// silently analyze a different session that started the same way.
package analysis

import (
	"errors"
	"io/fs"

	"github.com/exequieldeferrari/axiom/internal/activity"
	"github.com/exequieldeferrari/axiom/internal/correlate"
	"github.com/exequieldeferrari/axiom/internal/crossread"
	"github.com/exequieldeferrari/axiom/internal/delegation"
	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/harness"
	"github.com/exequieldeferrari/axiom/internal/profiler"
	"github.com/exequieldeferrari/axiom/internal/reacquire"
	"github.com/exequieldeferrari/axiom/internal/store"
	"github.com/exequieldeferrari/axiom/internal/timeline"
	"github.com/exequieldeferrari/axiom/internal/turns"
	"github.com/exequieldeferrari/axiom/internal/work"
)

// Options selects what an analysis covers.
type Options struct {
	// Session limits the analysis to one session identity. Empty analyzes
	// every record in the log.
	Session string
}

// Usage is the outcome of reading the measurement stream beside the log.
//
// A log that is absent and a log that could not be read leave everything
// equally unmeasured, but they do not mean the same thing: the first is the
// ordinary state of a machine where no receiver has run, and the second is a
// problem only the user can resolve.
type Usage struct {
	// Index holds the measurements that were read. It is never nil, and it
	// is empty where there were none.
	Index *correlate.Index
	Stats store.ScanStats

	// Present reports that a usage log exists, whatever could be read from
	// it. It is a fact about the file and never about what was consumed:
	// absent telemetry is unrecorded consumption, not consumption of zero.
	Present bool

	// Unreadable is set when measurements may exist but could not be read,
	// and is nil when there were simply none to read.
	Unreadable error
}

// Log is one analyzed event log: every domain report derived in one pass over
// it, each owned by the package that produced it.
type Log struct {
	Findings   profiler.Report
	Activity   activity.Profile
	Context    timeline.Report
	Reacquire  reacquire.Report
	Turns      turns.Report
	Delegation delegation.Report
	CrossRead  crossread.Report
	Harness    harness.Report

	// Composition is what the analyzed calls were, by shape. It is a
	// complete partition of them: internal/work places every recorded call
	// in exactly one category, so it totals the tool calls Findings counted.
	Composition work.Composition

	// Records counts the records the analysis covered, which is fewer than
	// the log holds whenever a session was selected. It is a count of
	// records and not of tool calls.
	Records int

	// Stats describes the whole log, including the records that could not be
	// decoded. A record Axiom could not read cannot be attributed to a
	// session, so this is never narrowed by a selection: what was lost is
	// exactly what would have said which session it belonged to.
	Stats store.ScanStats

	Usage Usage
}

// Analyze reads the log in dir and derives every report from one pass over it.
//
// It never writes. A missing log is returned as the error the store gave,
// because how an absent log should be reported is a decision for a command and
// not for the analysis.
func Analyze(dir string, opts Options) (Log, error) {
	scanner, err := store.ScanEvents(dir)
	if err != nil {
		return Log{}, err
	}
	defer scanner.Close()

	// Measurements are indexed first because the analysis resolves them as
	// each read arrives, and a measurement read afterwards would arrive too
	// late to be attached to anything.
	usage := loadUsage(dir)

	p := profiler.New()
	a := activity.New(func(session, turn, invocation string) (int64, bool) {
		return usage.Index.ResultBytes(correlate.Key{
			SessionID: session, TurnID: turn, InvocationID: invocation,
		})
	})
	t := timeline.New()
	q := reacquire.New()
	tn := turns.New()
	// Delegation is fed every record and resolves nothing until it is asked
	// for a report: a launch and the work it names arrive in either order,
	// and both orders were observed.
	dl := delegation.New()
	// Reading across related scopes holds only per-scope acquisition counts,
	// and is joined to the relations delegation established when the report
	// is taken.
	cr := crossread.New()
	// Provenance is read back out of the records that carry it. Nothing here
	// observes a file: the observation happened when the session started,
	// and taking it again now would describe this machine today and attach
	// it to work recorded whenever the log was written.
	hn := harness.New()

	out := Log{Usage: usage}
	for scanner.Scan() {
		record := scanner.Record()
		// A session is selected by the identifier the agent recorded,
		// exactly. Matching a prefix would silently analyze a different
		// session that happened to start the same way.
		if opts.Session != "" && record.SessionID != opts.Session {
			continue
		}
		out.Records++

		p.Add(record)
		a.Add(record)
		// Epoch membership comes from the timeline as it observes the record,
		// in the same pass, because append order is the only thing that
		// establishes it.
		at := t.Add(record)
		q.Add(record, at)
		tn.Add(record, at)
		dl.Add(record)
		cr.Add(record)
		hn.Add(record)
		// The same test the accumulators above apply to decide that a record
		// carried a call. What the call was, and the counting of it, belong
		// to internal/work.
		if record.Type == event.TypeToolCall && record.Tool != nil {
			out.Composition.Add(record.Tool)
		}
	}
	if err := scanner.Err(); err != nil {
		return Log{}, err
	}

	out.Findings = p.Report()
	out.Activity = a.Profile()
	out.Context = t.Report()
	out.Reacquire = q.Report()
	out.Turns = tn.Report()
	// Taken once. The launches and the relations they established are one
	// derivation, and reading across scopes groups against those relations
	// rather than deriving them a second time.
	out.Delegation = dl.Report()
	out.CrossRead = cr.Report(out.Delegation)
	out.Harness = hn.Report()
	out.Stats = scanner.Stats()
	return out, nil
}

// loadUsage indexes the measurements recorded beside the event log.
//
// Telemetry is optional and always will be: it exists only for the time a
// receiver was running. Nothing here fails the analysis; the worst outcome is
// an analysis without measurements.
func loadUsage(dir string) Usage {
	out := Usage{Index: correlate.NewIndex()}

	scanner, err := store.ScanUsage(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// No receiver has ever recorded here, which is the common case.
		return out
	case err != nil:
		out.Present, out.Unreadable = true, err
		return out
	}
	defer scanner.Close()
	out.Present = true

	for scanner.Scan() {
		out.Index.Add(scanner.Record())
	}
	out.Stats = scanner.Stats()
	if err := scanner.Err(); err != nil {
		// A log that could not be read in full is discarded rather than used
		// in part: the record that would have made a measurement ambiguous
		// may be exactly the one that was lost.
		out.Index = correlate.NewIndex()
		out.Unreadable = err
	}
	return out
}
