package cli

import (
	"fmt"
	"io"

	"github.com/exequieldeferrari/axiom/internal/reacquire"
	"github.com/exequieldeferrari/axiom/internal/timeline"
)

const (
	// How many paths the section shows. Everything omitted is accounted for on
	// a line of its own, so the limit hides no relation.
	reacquiredShown = 8
	// An acquisition is indented under the path it belongs to.
	acquisitionIndent = "      "

	// The three empty states are kept apart because they mean different
	// things. No boundary at all, boundaries with nothing read across them, and
	// nothing recorded are not interchangeable, and a single "none" would read
	// as the middle one in all three cases.
	noBoundary = "  No session identity recorded more than one context epoch, so there was no\n  boundary for a path to be read across.\n"
	noCrossing = "  No path was read in more than one context epoch of one session identity.\n"

	reacquiredCaveat = "A path is listed where a whole-file read of it succeeded in more than one\ncontext epoch of one session identity. That is an observed ordering of reads\naround a recorded boundary, and nothing more: an epoch boundary is a\nstructural boundary in the log, not proof that the agent's context was\ndiscarded, and an epoch also ends where a session does, which discards\nnothing.\n"
	reacquiredBound  = "Boundaries are recorded only where the agent reported one, so they are a\nlower bound: a boundary Axiom did not observe leaves the reads on either\nside of it looking like one epoch, and no relation is reported.\n"
	// The one relationship the section carries, stated as the ordering it is
	// rather than as a reason for anything, and as the operation it is rather
	// than as a change to a file. Each inference a reader would otherwise
	// supply is refused explicitly.
	reacquiredOrdering = "Where a write or edit of the same path was recorded after the read in that\nepoch, the line says so. It is an ordering of two recorded operations. It\ndoes not say the file changed, because a call the agent reported failing was\nstill a call that was recorded, and it does not say why the read happened.\nWhere none was recorded, that is not evidence the read achieved nothing.\n"
	reacquiredSessions = "Session identities are never compared. Nothing recorded links one to\nanother, so a path read under two of them appears here under neither.\n"
	reacquiredSubagent = "A nested agent reasons in a context of its own, and these epochs are the\nsession's, so its reads are not part of this."
)

// writeReacquired reports the paths read again in a later context epoch.
//
// It is printed after the context epochs because it describes reading placed
// against those boundaries, and a reader who cannot see the boundaries cannot
// audit a line here.
func writeReacquired(w io.Writer, r reacquire.Report) {
	fmt.Fprint(w, "\nRead again in a later context epoch\n\n")

	switch {
	case r.MultiEpochSessions == 0:
		fmt.Fprint(w, noBoundary)
		writeSetAside(w, r)
		return
	case len(r.Paths) == 0:
		fmt.Fprint(w, noCrossing)
		fmt.Fprint(w, "\n"+reacquiredBound)
		writeSetAside(w, r)
		return
	}

	shown := r.Paths
	if len(shown) > reacquiredShown {
		shown = shown[:reacquiredShown]
	}
	for _, p := range shown {
		writeReacquiredPath(w, p)
	}
	if omitted := r.Paths[len(shown):]; len(omitted) > 0 {
		fmt.Fprintf(w, "  and %s read in more than one epoch\n", plural(len(omitted), "more path"))
	}

	fmt.Fprintf(w, "\n%s read in more than one context epoch.\n", plural(len(r.Paths), "path"))
	fmt.Fprint(w, "\n"+reacquiredCaveat)
	fmt.Fprint(w, "\n"+reacquiredBound)
	fmt.Fprint(w, "\n"+reacquiredOrdering)
	fmt.Fprint(w, "\n"+reacquiredSessions)
	writeSetAside(w, r)
}

// writeReacquiredPath names one path and the epochs it was read in.
//
// The session identity is printed in full: it is what 'axiom profile --session'
// takes, and the epochs below are numbered within it, so a reader needs both to
// find the reads this line came from.
func writeReacquiredPath(w io.Writer, p reacquire.Path) {
	fmt.Fprintf(w, "  %s\n", p.Path)
	fmt.Fprintf(w, "%ssession %s\n", acquisitionIndent, p.SessionID)
	for _, a := range p.Epochs {
		fmt.Fprintf(w, "%sepoch %d, %s, %s\n",
			acquisitionIndent, a.Epoch.Ordinal, acquisitionOpening(a.Opening), observedReads(a))
	}
}

// acquisitionOpening says how the epoch began, in the agent's own words where
// it gave any. It matches the wording of the epochs section above, so the two
// can be read against each other.
func acquisitionOpening(o timeline.Opening) string {
	switch o.Kind {
	case timeline.OpeningRecorded:
		return "opened by " + o.Source
	case timeline.OpeningUnspecified:
		return "opened by a start with no source"
	default:
		return "no start recorded"
	}
}

// observedReads states what was read in one epoch and what was recorded after
// it, in terms that describe order and never reason.
//
// What follows the read is named as an operation that was recorded, not as a
// change to the file. The record establishes that a call was made and what
// became of it; whether a file was left different is not observable, and a
// failure is counted here precisely because neither reading of it is.
//
// A call whose outcome the record never established is a third case rather
// than an absence, and the four branches keep it from being appended to a
// sentence that has just said nothing was recorded.
func observedReads(a reacquire.Acquisition) string {
	line := plural(a.Reads, "read")
	switch {
	case a.WriteOrEditAfter && a.UnestablishedAfter > 0:
		line += fmt.Sprintf(", later write or edit recorded, %d more with no outcome recorded",
			a.UnestablishedAfter)
	case a.WriteOrEditAfter:
		line += ", later write or edit recorded"
	case a.UnestablishedAfter > 0:
		line += fmt.Sprintf(", %s afterwards with no outcome recorded",
			writesOrEdits(a.UnestablishedAfter))
	default:
		line += ", no later write or edit recorded"
	}
	return line
}

// writesOrEdits counts an operation whose name does not take a plural s in the
// place plural would put one.
func writesOrEdits(n int) string {
	if n == 1 {
		return "1 write or edit"
	}
	return fmt.Sprintf("%d writes or edits", n)
}

// writeSetAside accounts for reads the analysis did not look at, so that work
// Axiom observed does not disappear from the report.
func writeSetAside(w io.Writer, r reacquire.Report) {
	if r.SubagentReads == 0 {
		return
	}
	sentence(w, fmt.Sprintf("%s %s were set aside.",
		reacquiredSubagent, plural(r.SubagentReads, "successful whole-file read")))
}
