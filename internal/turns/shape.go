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
	shapeSubagentLaunch
)

// shapeOf reduces a tool call to the shape of the operation it carried.
//
// The profiler classifies calls the same way for its intervals, and the two are
// kept apart on purpose: this package may depend only on the event model and
// the timeline, and importing findings to count reads would tie a measurement
// to the analysis that judges them. The duplication is a few lines and is
// deliberate, in the same way the timeline derives context boundaries the
// profiler also derives. Because it is duplication and not sharing, the two can
// drift, and one of them did: a test holds them to the same table of shapes so
// that the next divergence is a failure rather than a discrepancy between two
// sections of one report.
//
// A launch is recognized from the metadata the adapter derived and never from
// the tool's name, which is the agent's own vocabulary and has already changed
// once. It is the declaration that work was handed to a nested agent, and it is
// not that agent's work: the calls the nested agent went on to make are counted
// separately, by the subagent identifier they carry, and neither count is
// derived from the other.
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

	case m.Subagent != nil:
		return shapeSubagentLaunch

	default:
		return shapeUninterpreted
	}
}
