// Package work reduces recorded tool calls to the shapes Axiom's evidence
// model distinguishes, and counts them.
//
// It answers one question — what was this recorded call — and it answers it in
// one place. Two analyses already asked it separately and drifted apart: a
// launch was counted as a subagent operation by one and as a call Axiom could
// not describe by the other, in the same report, over the same log. A third
// consumer arrived with the launch-to-nested-work relation, and three copies of
// a rule is where a rule stops being one.
//
// This package holds only the classification and the counting. It depends on
// the event model and on nothing else, so any analysis may use it without
// taking on the analysis beside it: counting a read here does not import the
// judgement of whether that read was redundant.
//
// A shape is what a call was, never what became of it. A count of reads is a
// count of read calls that reached the log, not of files an agent can be shown
// to have obtained.
package work

import "github.com/exequieldeferrari/axiom/internal/event"

// Shape is the operation a recorded call carried.
type Shape int

const (
	// Uninterpreted is a call this version cannot describe: a tool outside
	// what the adapter extracts metadata for, and input it could not read. It
	// is Axiom's limit, and never a call that did nothing.
	Uninterpreted Shape = iota
	WholeRead
	RangedRead
	Search
	Shell
	Write
	Edit

	// SubagentLaunch is a call that declared work handed to a nested agent.
	//
	// It is the declaration and not the nested agent's work: the calls that
	// agent went on to make are recorded separately, against the identity
	// they carry, and neither is derived from the other.
	SubagentLaunch
)

// Of reduces a tool call to the shape of the operation it carried.
//
// A launch is recognized from the metadata the adapter derived and never from
// the tool's name, which is the agent's own vocabulary and has already changed
// once.
func Of(t *event.ToolCall) Shape {
	m := t.Metadata
	switch {
	case m == nil:
		return Uninterpreted

	case m.File != nil:
		if m.File.Path == "" {
			return Uninterpreted
		}
		switch m.File.Access {
		case event.AccessRead:
			// A ranged read returns part of a file, which is a different
			// operation from obtaining one, and is counted as itself.
			if m.File.Offset != nil || m.File.Limit != nil {
				return RangedRead
			}
			return WholeRead
		case event.AccessWrite:
			return Write
		case event.AccessEdit:
			return Edit
		default:
			return Uninterpreted
		}

	case m.Shell != nil:
		return Shell

	case m.Search != nil:
		return Search

	case m.Subagent != nil:
		return SubagentLaunch

	default:
		return Uninterpreted
	}
}

// Outcomes counts calls by what the record established became of them.
//
// The three are kept apart for the reason they are kept apart everywhere else
// in Axiom: an outcome that was never established is not a failure, and a
// failed write may still have applied in part, so neither is it nothing. The
// same three states carry a second question for a launch, where what the
// outcome settles is not what persisted but whether the delegation happened at
// all.
type Outcomes struct {
	Succeeded     int
	Failed        int
	Unestablished int
}

// Total counts the calls recorded, whatever became of them.
func (o Outcomes) Total() int { return o.Succeeded + o.Failed + o.Unestablished }

func (o *Outcomes) add(outcome event.Outcome) {
	switch outcome {
	case event.OutcomeSuccess:
		o.Succeeded++
	case event.OutcomeFailure:
		o.Failed++
	default:
		o.Unestablished++
	}
}

// Composition is what a set of recorded calls were.
//
// Every recorded call falls in exactly one category, so the categories sum to
// the calls counted. Writes, edits and launches carry outcomes, for two
// different reasons: what a write or an edit leaves behind is the part that
// could have persisted, while what a launch's outcome settles is whether a
// nested agent was started at all.
type Composition struct {
	WholeReads  int
	RangedReads int
	Searches    int
	Shell       int
	Writes      Outcomes
	Edits       Outcomes

	// Launches counts the calls that declared work handed to a nested agent,
	// by what the record established became of each.
	//
	// Only the succeeded ones establish that a launch call succeeded. A call
	// reported failing declared a launch that the record says did not
	// succeed, and is not a nested agent that started; one whose outcome was
	// never established is neither, and stays apart from both.
	//
	// This counts launches and never the work a launched agent did. A launch
	// recorded before the adapter derived metadata for it is not counted
	// here at all: the record carries no evidence that it was one.
	Launches Outcomes

	// Uninterpreted counts calls this composition does not place. A count
	// here is Axiom's limit, not a call that did nothing.
	Uninterpreted int
}

// Total counts every call the composition placed, in every category.
func (c Composition) Total() int {
	return c.WholeReads + c.RangedReads + c.Searches + c.Shell +
		c.Writes.Total() + c.Edits.Total() + c.Launches.Total() + c.Uninterpreted
}

// Add counts one recorded call.
func (c *Composition) Add(t *event.ToolCall) {
	switch Of(t) {
	case WholeRead:
		c.WholeReads++
	case RangedRead:
		c.RangedReads++
	case Search:
		c.Searches++
	case Shell:
		c.Shell++
	case Write:
		c.Writes.add(t.Outcome)
	case Edit:
		c.Edits.add(t.Outcome)
	case SubagentLaunch:
		c.Launches.add(t.Outcome)
	default:
		c.Uninterpreted++
	}
}
