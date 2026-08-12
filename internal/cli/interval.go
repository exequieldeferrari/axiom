package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/exequieldeferrari/axiom/internal/profiler"
)

const (
	// The heading names an ordering and a bound. It says where the calls sit
	// in the record and stops there, because a heading over a list of work
	// beside a failure is read as the reason the failure went away unless it
	// refuses to be.
	intervalHeading = "Recorded before the later success"
	// A path is indented under the line that introduces the paths, following
	// the sections above that list paths the same way.
	intervalPathIndent = associatedIndent + "  "

	// Stated as a fact about the log, and immediately as not a fact about the
	// execution. The controlled capture this was validated against produced
	// exactly this case while a file on disk changed from inside the command,
	// where no tool call reports it.
	intervalEmpty = "No tool operation was recorded between them."

	intervalCaveat = "Recorded before the later success lists the tool calls Axiom recorded\nbetween the last failed attempt and the first later observed success of the\nsame command, in the same session and subagent. It is an ordering of\nrecorded calls: none of it is established to have made the difference, none\nof it is established to have been needed, and a shorter one is not a better\none. A count is of calls that reached the log, not of operations shown to\nhave achieved anything, and a path names a file a call was recorded at, not\na file left different. Where nothing was recorded between them, that\ndescribes the log and not the execution: a call rejected before it ran is\nnever recorded, and a command can change state that no tool call reports.\n"
	// Kept apart from the caveat above so that the empty case does not carry
	// an explanation of turns it has no room for, and so that a reader meets
	// this where the three states are.
	intervalTurns = "A turn boundary is where input Axiom does not observe may have arrived, so\nwhether one fell between the two observations is reported rather than\nassumed. The two observations count as much as the calls between them: a\nboundary can fall between a failed attempt and a success with nothing\nrecorded in between. Where any of them carried no turn identifier, the\nquestion was never settled, which is not the same as there being no\nboundary.\n"
)

// writeInterval reports what was recorded between a sequence of failed
// attempts and the success that bounded it.
//
// It is subordinate to the finding and has no ranking of its own. The finding
// is the observation; this is the ordering around it, and presenting it as a
// population of its own would make the interval the subject and invite the
// reader to compare intervals against each other.
func writeInterval(w io.Writer, iv profiler.Interval) {
	fmt.Fprintf(w, "\n%s%s\n", findingIndent, intervalHeading)

	if iv.Operations == 0 {
		fmt.Fprintf(w, "%s%s\n", associatedIndent, intervalEmpty)
		associated(w, "Turn boundary", turnBoundary(iv.TurnBoundary))
		return
	}

	associated(w, "Operations recorded", strconv.Itoa(iv.Operations))
	// A category with none recorded is left out, as everywhere else in the
	// report: zero is a fact Axiom established, and printing eight of them
	// would bury the counts that are not.
	category(w, "Whole-file reads", iv.WholeReads)
	category(w, "Ranged reads", iv.RangedReads)
	category(w, "Searches", iv.Searches)
	// Commands other than this one, and further attempts of this one: the
	// interval ends at the first success, not at the next attempt.
	category(w, "Shell commands", iv.Shell)
	outcomes(w, "Writes", iv.Writes)
	outcomes(w, "Edits", iv.Edits)
	category(w, "Subagent calls", iv.Subagents)
	category(w, "Unrecognized", iv.Uninterpreted)
	writeIntervalPaths(w, iv)
	associated(w, "Turn boundary", turnBoundary(iv.TurnBoundary))
}

func category(w io.Writer, label string, n int) {
	if n == 0 {
		return
	}
	associated(w, label, strconv.Itoa(n))
}

// outcomes prints one category of file operation with what the record
// establishes became of its calls.
//
// The count is of calls recorded. What the record did not settle is named
// beside it rather than folded into either outcome, because an outcome that
// was never established is not a failure, and a call reported failing may
// still have applied in part.
func outcomes(w io.Writer, label string, o profiler.Outcomes) {
	if o.Total() == 0 {
		return
	}

	var qualified []string
	if o.Failed > 0 {
		qualified = append(qualified, fmt.Sprintf("%d reported failing", o.Failed))
	}
	if o.Unestablished > 0 {
		qualified = append(qualified, fmt.Sprintf("%d with no outcome recorded", o.Unestablished))
	}

	value := strconv.Itoa(o.Total())
	if len(qualified) > 0 {
		value += "  (" + strings.Join(qualified, ", ") + ")"
	}
	associated(w, label, value)
}

// writeIntervalPaths names the files a write or edit was recorded at.
//
// Retention is bounded, so the paths are a sample and the line below says how
// much of one. The counts above are not bounded and describe every operation,
// which is what keeps a shortened list from shortening the interval.
func writeIntervalPaths(w io.Writer, iv profiler.Interval) {
	if len(iv.Paths) == 0 {
		return
	}

	fmt.Fprintf(w, "%s%s\n", associatedIndent, "Writes or edits recorded at")
	for _, path := range iv.Paths {
		fmt.Fprintf(w, "%s%s\n", intervalPathIndent, path)
	}
	if iv.OmittedPaths > 0 {
		fmt.Fprintf(w, "%sand %s recorded\n", intervalPathIndent, plural(iv.OmittedPaths, "more path"))
	}
}

// turnBoundary says what the record establishes, in three states that are not
// interchangeable. The unknown one is written as a question that was not
// settled, so that it cannot be read as the absence of a boundary.
func turnBoundary(b profiler.TurnBoundary) string {
	switch b {
	case profiler.TurnBoundaryRecorded:
		return "recorded between them"
	case profiler.TurnBoundaryUnknown:
		return "not established"
	default:
		return "none recorded"
	}
}
