package turns

import "github.com/exequieldeferrari/axiom/internal/event"

// shape is what a composition counts a recorded call as.
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
)

// shapeOf reduces a tool call to the shape of the operation it carried.
//
// The profiler classifies calls the same way for its intervals, and the two are
// kept apart on purpose: this package may depend only on the event model and
// the timeline, and importing findings to count reads would tie a measurement
// to the analysis that judges them. The duplication is a few lines and is
// deliberate, in the same way the timeline derives context boundaries the
// profiler also derives.
//
// A subagent spawn has no shape of its own here. The current agent does not
// report the metadata Axiom needs to recognize one, so such calls arrive
// uninterpreted, and a category that is empty in every real log would say more
// about Axiom than about the work. Nested calls are counted separately, by the
// subagent identifier they carry.
//
// The shape is independent of what became of the call. A count of reads is a
// count of read calls that reached the log, not of files the agent can be shown
// to have obtained.
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
			// A ranged read returns part of a file, which is a different
			// operation from obtaining one, and is counted as itself.
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
		return shapeShell

	case m.Search != nil:
		return shapeSearch

	default:
		return shapeUninterpreted
	}
}
