// Package reacquire reports the paths an agent read successfully in more than
// one context epoch of one session identity.
//
// This is measurement, not a finding. The profiler asks what repeated without a
// good reason and stays silent unless the evidence rules the good reasons out;
// it also stops comparing at every recorded context reset, so repetition that
// crosses one is exactly what it declines to judge. This package makes that
// crossing visible without judging it: it counts successful whole-file reads,
// says which epochs they happened in, and rules nothing out.
//
// What a relation here establishes is narrow. The same path was read, and read
// again after a boundary the log recorded. It does not establish that the
// agent's context was discarded at that boundary — an epoch also ends where a
// session ends, which discards nothing — nor that the later read was avoidable,
// nor that anything was lost in between. Recorded boundaries are a lower bound,
// so a crossing Axiom did not see is a relation it does not report.
//
// One observed relationship is carried alongside each acquisition: whether a
// write or edit of the same path was recorded after it, in the same epoch. It
// is an ordering of two recorded operations and nothing more. It does not
// establish that the file changed, it is not a reason for the read, and its
// absence is not evidence that the read achieved nothing.
package reacquire

import (
	"cmp"
	"slices"

	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/timeline"
)

// Acquisition is the successful whole-file reading of one path inside one
// context epoch.
type Acquisition struct {
	// Epoch names the context epoch the reads happened in.
	Epoch timeline.EpochRef
	// Opening is how that epoch began, carried from the timeline exactly as
	// the agent reported it. It is never branched on: a source no version of
	// Axiom has seen still reports as itself.
	Opening timeline.Opening

	// Reads counts the successful whole-file reads of the path in this epoch.
	// It is at least one, and more than one is repetition inside a single
	// context, which is the profiler's subject rather than this one.
	Reads int

	// WriteOrEditAfter reports that a write or edit of this same path was
	// recorded after the first read of it in this epoch, and that the record
	// established what became of that call.
	//
	// It says an operation was recorded, and in what order. It does not say
	// the file changed: a call the agent reported failing was still a call
	// that was recorded, and whether it left anything behind is not
	// observable — which is also why a failure counts here at all, since
	// treating it as nothing would claim the opposite with the same
	// confidence.
	//
	// It is not a reason the read happened either. Nothing here says the read
	// was needed for the operation that followed it, or that the boundary
	// brought either about.
	WriteOrEditAfter bool

	// UnestablishedAfter counts write or edit calls at this same path recorded
	// after the first read of it in this epoch whose outcome the record did
	// not establish.
	//
	// They are held apart from WriteOrEditAfter because a call whose outcome
	// was never established is not known to have run at all. Folding them in
	// would report a call as observed on the strength of a record that does
	// not say so, and dropping them would report that nothing followed the
	// read. They are counted and reported as themselves.
	UnestablishedAfter int
}

// Path is one path read successfully in more than one context epoch of one
// session identity.
//
// Identity is the exact string the agent named, compared byte for byte, as
// everywhere else in Axiom. No normalization happens: two strings naming one
// file stay apart, which can only lose a relation and never invent one.
type Path struct {
	SessionID string
	Path      string

	// Epochs are the epochs the path was read in, in the order those epochs
	// were opened. There are always at least two.
	Epochs []Acquisition
}

// Report is one pass of cross-epoch reading.
type Report struct {
	// Paths holds every path read in more than one epoch of one session
	// identity. It is complete: showing only part of it is a decision for a
	// report, not for the analysis.
	Paths []Path

	// MultiEpochSessions counts the session identities observed with more than
	// one context epoch.
	//
	// It is the denominator this analysis needs. Where it is zero there was no
	// boundary for a path to be read across, which is a different fact from
	// having boundaries and observing nothing read across them.
	MultiEpochSessions int

	// SubagentReads counts successful whole-file reads set aside because a
	// nested agent made them. A subagent reasons in a context of its own, and
	// the epochs here are the session's, so a subagent's reading is not
	// evidence about what crossed one of these boundaries.
	//
	// They are counted rather than dropped so that work the analysis did not
	// look at does not disappear from the report.
	SubagentReads int
}

// Accumulator builds a report from events as they are read.
//
// It is fed the placement the timeline derived for each record, so epoch
// membership comes from the state machine that owns it and is never
// reconstructed here.
type Accumulator struct {
	sessions map[string]*sessionWork
	subagent int
}

// New returns an accumulator with no observations.
func New() *Accumulator {
	return &Accumulator{sessions: make(map[string]*sessionWork)}
}

type sessionWork struct {
	// epochs holds every epoch ordinal observed for the session, including
	// those that recorded no file work: an epoch that read nothing is still an
	// epoch a later read could have crossed.
	epochs map[int]struct{}
	paths  map[string]*pathWork
}

type pathWork struct {
	// order holds the epoch ordinals in the order they were first read in,
	// which is the order the epochs were opened.
	order   []int
	byEpoch map[int]*Acquisition
}

// Add records one event and where the timeline placed it.
//
// A record the timeline could not place belongs to no epoch and contributes
// nothing: without an epoch there is no boundary to say it fell on either side
// of, and inventing one would be the mistake this analysis exists to avoid.
func (a *Accumulator) Add(ev event.Event, at timeline.Placement) {
	if !at.Placed {
		return
	}

	s := a.session(at.Epoch.SessionID)
	// Every placed record establishes that its epoch exists, whatever else it
	// did, so the count of epochs does not depend on any of them doing work.
	s.epochs[at.Epoch.Ordinal] = struct{}{}

	if ev.Type != event.TypeToolCall || ev.Tool == nil {
		return
	}
	m := ev.Tool.Metadata
	if m == nil || m.File == nil || m.File.Path == "" {
		return
	}

	switch m.File.Access {
	case event.AccessRead:
		a.read(ev, at, s, m.File)
	case event.AccessWrite, event.AccessEdit:
		s.writeOrEdit(at.Epoch.Ordinal, m.File.Path, ev.Tool.Outcome)
	}
}

// read records one acquisition, if the record establishes one.
//
// A ranged read returns part of a file, so it acquires something else and is
// not compared: the same rule the profiler applies, for the same reason. A read
// the agent reported failing is not established to have delivered the file's
// contents, and neither is one whose outcome was never established — the record
// says what became of a call and never what it returned. Both are left out
// rather than read as an acquisition that may not have happened.
func (a *Accumulator) read(ev event.Event, at timeline.Placement, s *sessionWork, f *event.FileOp) {
	if f.Offset != nil || f.Limit != nil {
		return
	}
	if ev.Tool.Outcome != event.OutcomeSuccess {
		return
	}
	if ev.SubagentID != "" {
		a.subagent++
		return
	}
	s.acquire(at, f.Path)
}

func (a *Accumulator) session(id string) *sessionWork {
	s, ok := a.sessions[id]
	if !ok {
		s = &sessionWork{
			epochs: make(map[int]struct{}),
			paths:  make(map[string]*pathWork),
		}
		a.sessions[id] = s
	}
	return s
}

func (s *sessionWork) acquire(at timeline.Placement, path string) {
	w, ok := s.paths[path]
	if !ok {
		w = &pathWork{byEpoch: make(map[int]*Acquisition)}
		s.paths[path] = w
	}

	ordinal := at.Epoch.Ordinal
	acq, ok := w.byEpoch[ordinal]
	if !ok {
		acq = &Acquisition{Epoch: at.Epoch, Opening: at.Opening}
		w.byEpoch[ordinal] = acq
		w.order = append(w.order, ordinal)
	}
	acq.Reads++
}

// writeOrEdit notes a write or edit against the acquisition of the same path in
// the same epoch, when one has already been recorded there.
//
// Nothing is noted when the path has not been read in this epoch yet: an
// operation recorded before the read is not after it, and this field says only
// that one observation followed another.
//
// A failure counts. The record establishes that the call was made and what
// became of it, which is the whole of what is claimed; whether the file was
// left changed is not observable either way, and a failed edit may still have
// applied in part, which is why the profiler treats one as a barrier. An
// outcome that was never established is counted apart, because such a record
// does not establish that the call ran.
func (s *sessionWork) writeOrEdit(ordinal int, path string, outcome event.Outcome) {
	w, ok := s.paths[path]
	if !ok {
		return
	}
	acq, ok := w.byEpoch[ordinal]
	if !ok {
		return
	}

	switch outcome {
	case event.OutcomeSuccess, event.OutcomeFailure:
		acq.WriteOrEditAfter = true
	default:
		acq.UnestablishedAfter++
	}
}

// Report summarizes everything added so far. It does not consume the
// accumulator: adding more events and reporting again is valid.
func (a *Accumulator) Report() Report {
	out := Report{SubagentReads: a.subagent}

	for id, s := range a.sessions {
		if len(s.epochs) > 1 {
			out.MultiEpochSessions++
		}
		for path, w := range s.paths {
			// One epoch is one acquisition, however many times the path was
			// read in it. Repetition inside a single context is the
			// profiler's subject, under rules this analysis does not apply.
			if len(w.order) < 2 {
				continue
			}
			out.Paths = append(out.Paths, Path{
				SessionID: id,
				Path:      path,
				Epochs:    acquisitions(w),
			})
		}
	}

	slices.SortFunc(out.Paths, compare)
	return out
}

// acquisitions lists a path's epochs in the order they were opened, which is
// the order they were first read in. Ordinals increase with append order, so
// the two are the same ordering.
func acquisitions(w *pathWork) []Acquisition {
	out := make([]Acquisition, 0, len(w.order))
	for _, ordinal := range w.order {
		out = append(out, *w.byEpoch[ordinal])
	}
	return out
}

// compare puts the path read across the most epochs first, and settles every
// tie on recorded strings so that two runs over one log cannot disagree.
//
// Ranking is by how many epochs the reading spanned, which is the subject of
// the analysis. It is never a ranking of how much any of it mattered.
func compare(a, b Path) int {
	if c := cmp.Compare(len(b.Epochs), len(a.Epochs)); c != 0 {
		return c
	}
	if c := cmp.Compare(a.SessionID, b.SessionID); c != 0 {
		return c
	}
	return cmp.Compare(a.Path, b.Path)
}
