package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/exequieldeferrari/axiom/internal/activity"
)

const (
	// Wide enough for the longest bucket name, with a gap before the count.
	bucketLabelWidth = 14
	bucketCountWidth = 5
	// Path work is indented under the path it belongs to.
	pathIndent = "      "
	// How many paths a report shows. Everything omitted is accounted for on a
	// line of its own, so the limit hides no work.
	pathsShown = 10
	// The width the report's explanations are written to.
	reportWidth = 76

	compositionCaveat = "Each observed tool call is counted once above, and a call rejected before it\nran is never recorded at all. Only a file operation names a path, so only\nthose are attributed below; the rest is work Axiom either cannot attribute\nto a path or cannot interpret at all.\n"
	pathsCaveat       = "Every file operation observed at a path is counted: reads of a whole file,\nranged reads of part of one, writes and edits, and operations that failed. A\ncategory with none observed is left out of a path's line. Time is the sum of\nthe durations the agent reported for those operations, not elapsed time. A\ndash means Axiom recorded nothing complete; it never means zero.\n"
	unmeasuredReads   = "No read was measured, so no path reports read bytes. Sizes are recorded only\nwhile a receiver is running, which is not an absence of bytes.\n"
	noToolCalls       = "  No tool call was recorded.\n"

	// These two carry counts from the log, so their length is not known when
	// they are written and they are wrapped rather than broken by hand.
	attributedShare  = "The lines above describe the %d of %d observed tool calls that named a path."
	measuredCoverage = "Read bytes were measured for %s. A path reports a total only where every successful read of it was measured exactly once, and the total is bytes the agent reported returning, not tokens and not cost."
)

// writeActivity reports the work that was observed and where it happened.
//
// It is printed before the findings because it says what the execution
// consisted of, which is what tells a reader how much of it the findings below
// could speak to.
func writeActivity(w io.Writer, p activity.Profile) {
	fmt.Fprint(w, "\nObserved operations\n\n")
	if p.Operations.Total == 0 {
		fmt.Fprint(w, noToolCalls)
		return
	}

	c := p.Operations
	bucket(w, "File", c.File, "read, written or edited; attributed by path below")
	bucket(w, "Search", c.Search, "pattern recorded, no path named")
	bucket(w, "Shell", c.Shell, "effects not observable; never attributed")
	bucket(w, "Subagent", c.Subagent, "the nested agent's calls are recorded separately")
	bucket(w, "Unrecognized", c.Unrecognized, "Axiom cannot say what these did")
	fmt.Fprint(w, "\n"+compositionCaveat)

	writePaths(w, p)
}

// bucket prints one shape of operation. A bucket with none observed is left
// out: zero here is a fact Axiom established, and the buckets are summed by the
// tool call count above them.
func bucket(w io.Writer, label string, n int, note string) {
	if n == 0 {
		return
	}
	fmt.Fprintf(w, "  %-*s%*d   %s\n", bucketLabelWidth, label, bucketCountWidth, n, note)
}

func writePaths(w io.Writer, p activity.Profile) {
	if len(p.Paths) == 0 {
		fmt.Fprint(w, "\nWork by path\n\n")
		for _, line := range wrap(nothingAttributed(p.Operations), reportWidth-2) {
			fmt.Fprintf(w, "  %s\n", line)
		}
		return
	}

	shown := p.Paths
	if len(shown) > pathsShown {
		shown = shown[:pathsShown]
	}

	// Absolute paths are what agents report, and repeating a long shared
	// directory on every line costs the width the work needs. The prefix is
	// stated once and trimmed from the lines under it, which shortens the
	// display without changing the identity Axiom compared.
	prefix := commonDir(shown)
	if prefix == "" {
		fmt.Fprint(w, "\nWork by path\n\n")
	} else {
		fmt.Fprintf(w, "\nWork by path, under %s\n\n", strings.TrimSuffix(prefix, "/"))
	}

	// Byte totals are left off every line when nothing was measured, rather
	// than printed as a column of dashes that says the same thing ten times.
	measured := p.ReadsMeasured > 0
	for _, path := range shown {
		fmt.Fprintf(w, "  %s\n%s%s\n", strings.TrimPrefix(path.Path, prefix), pathIndent, describe(path, measured))
	}
	if omitted := p.Paths[len(shown):]; len(omitted) > 0 {
		operations := 0
		for _, path := range omitted {
			operations += path.Operations()
		}
		fmt.Fprintf(w, "  and %s (%s)\n", plural(len(omitted), "more path"), plural(operations, "operation"))
	}

	fmt.Fprint(w, "\n"+pathsCaveat)
	// An execution where everything named a path has nothing left over to
	// account for, and saying so would explain the ordinary case.
	if p.Operations.File < p.Operations.Total {
		sentence(w, fmt.Sprintf(attributedShare, p.Operations.File, p.Operations.Total))
	}
	switch {
	case measured:
		sentence(w, fmt.Sprintf(measuredCoverage, coverage(p.ReadsMeasured, p.Reads)))
	case p.Reads > 0:
		fmt.Fprint(w, "\n"+unmeasuredReads)
	}
}

// sentence prints an explanation whose length depends on the log.
func sentence(w io.Writer, text string) {
	fmt.Fprintln(w)
	for _, line := range wrap(text, reportWidth) {
		fmt.Fprintln(w, line)
	}
}

// describe states the work at one path.
//
// Categories with nothing observed are left out, because zero is established
// and naming it on every line would bury the counts that are not. A value Axiom
// could not establish is the opposite case and is always shown, as a dash.
func describe(p activity.Path, measured bool) string {
	var parts []string
	if p.Reads > 0 {
		parts = append(parts, plural(p.Reads, "read"))
	}
	if p.RangedReads > 0 {
		parts = append(parts, fmt.Sprintf("%d ranged", p.RangedReads))
	}
	// Writes and edits are summed here and only here. Both establish that the
	// path was modified, which is the fact this line reports.
	if modifications := p.Writes + p.Edits; modifications > 0 {
		parts = append(parts, plural(modifications, "modification"))
	}
	if p.Failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", p.Failed))
	}

	if p.Turns == nil {
		parts = append(parts, "turns —")
	} else {
		parts = append(parts, plural(*p.Turns, "turn"))
	}
	// A path nothing read has no byte total to be missing, so the field is left
	// out rather than shown as a value Axiom could not establish.
	if measured && p.Reads+p.RangedReads > 0 {
		if p.ReadBytes == nil {
			parts = append(parts, "read bytes —")
		} else {
			parts = append(parts, size(p.ReadBytes)+" read")
		}
	}
	if p.ObservedTime == nil {
		parts = append(parts, "time —")
	} else {
		parts = append(parts, p.ObservedTime.String())
	}
	return strings.Join(parts, ", ")
}

// nothingAttributed explains an empty profile without turning it into a claim
// that the execution left the repository alone.
func nothingAttributed(c activity.Composition) string {
	text := "No operation named a path, so there is nothing to attribute."
	if rest := unattributed(c); len(rest) > 0 {
		text += fmt.Sprintf(" That is not a claim that no file changed: %s were observed, and none of them names a path.",
			list(rest))
	}
	return text
}

// wrap breaks a sentence into lines that fit the report.
//
// Most explanations in the report are written on the lines they are printed on.
// The ones here cannot be: their length depends on the counts a log happened to
// produce.
func wrap(text string, width int) []string {
	var (
		lines []string
		line  string
	)
	for _, word := range strings.Fields(text) {
		switch {
		case line == "":
			line = word
		case len(line)+1+len(word) <= width:
			line += " " + word
		default:
			lines = append(lines, line)
			line = word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// unattributed names the observed work that no path can be attributed to.
func unattributed(c activity.Composition) []string {
	var parts []string
	if c.Shell > 0 {
		parts = append(parts, plural(c.Shell, "shell command"))
	}
	if c.Search > 0 {
		parts = append(parts, searches(c.Search))
	}
	if c.Subagent > 0 {
		parts = append(parts, plural(c.Subagent, "subagent call"))
	}
	if c.Unrecognized > 0 {
		parts = append(parts, plural(c.Unrecognized, "unrecognized call"))
	}
	return parts
}

func list(parts []string) string {
	switch len(parts) {
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " and " + parts[1]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}
}

func searches(n int) string {
	if n == 1 {
		return "1 search"
	}
	return fmt.Sprintf("%d searches", n)
}

// coverage says how much of the reading a byte total could be built from. It is
// only ever called where something was measured, so one read means that read.
func coverage(measured, total int) string {
	if total == 1 {
		return "the only read"
	}
	return fmt.Sprintf("%d of %d reads", measured, total)
}

// commonDir returns the longest directory prefix every path shares, ending in a
// separator, or empty when there is nothing worth trimming.
//
// It stops at separators so that two files in sibling directories cannot share
// a prefix that names neither of them, and it is display only: the paths
// themselves are never altered.
func commonDir(paths []activity.Path) string {
	if len(paths) < 2 {
		return ""
	}

	prefix := paths[0].Path
	for _, p := range paths[1:] {
		prefix = shared(prefix, p.Path)
	}
	if cut := strings.LastIndex(prefix, "/"); cut > 0 {
		return prefix[:cut+1]
	}
	return ""
}

func shared(a, b string) string {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] != b[i] {
			return a[:i]
		}
	}
	return a[:n]
}
