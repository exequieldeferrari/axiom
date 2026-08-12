package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"strconv"
	"strings"

	"github.com/exequieldeferrari/axiom/internal/analysis"
	"github.com/exequieldeferrari/axiom/internal/timeline"
	"github.com/exequieldeferrari/axiom/internal/work"
)

// A capture is compared as a table: a label, one column for each side, and the
// difference between them. The widths hold the longest label this report has
// without wrapping, which keeps the two columns of numbers in one place down
// the whole report.
const (
	compareLabelWidth = 42
	compareValueWidth = 11
	// Sub-rows sit under the category they belong to, and their labels are
	// narrowed to match so the numbers stay in the same column.
	compareSubIndent = "    "
	compareSubWidth  = compareLabelWidth - len(compareSubIndent)
)

const (
	// What the report is, stated in the report. A reader who never opens an
	// ADR still has to be told what was asserted and what was observed.
	compareContract = "A capture is the records Axiom wrote into one data directory, narrowed to\none session identity. Axiom does not establish that two captures are the\nsame task, that they are equivalent attempts at one, that either explains\nanything about the other, or that a difference here is good or bad.\nComparability is asserted by whoever selected the two captures.\n"
	compareMechanic = "A difference is the candidate's count less the baseline's, and nothing else.\nNothing here is a score, a rate, or a ranking.\n"
	// Said where it cannot be missed, because the two categories that move
	// most are the two a reader is most likely to read as a result.
	compareTrajectory = "The categories above are every recorded call in each capture, so shell and\nuninterpreted calls are shown with the rest: leaving either out would make\nthe remaining categories look like the whole of the work. Both were observed\ndiffering between repeated recordings of one workload, and so was the count\nof recorded tool calls they sum into.\n"
	comparePaths      = "Paths are never compared between captures. Each capture records its own\nabsolute paths, so how many paths a relation held is compared and the paths\nthemselves are not. The same goes for a command, which is recorded only as a\ndigest of one exact string.\n"
	compareUsage      = "Consumption is not compared. Whether a usage log exists is a fact about the\ndirectory, and an absent one is consumption that was never recorded rather\nthan consumption of none.\n"
	compareFindings   = "Findings are not compared. What the profiler compares repetition within ends\nat every recorded context reset and at every agent scope, so the same repeated\nwork counts differently depending on where those boundaries fell.\n"
)

// captureOptions selects one side of a comparison.
type captureOptions struct {
	dir string
	// session names the one session identity to compare. Empty means the
	// capture must hold exactly one, which is the ordinary case.
	session string
}

// compareOptions is one comparison: two captures, in the order they were
// given. Nothing about the order means anything except which side is
// subtracted from which.
type compareOptions struct {
	baseline  captureOptions
	candidate captureOptions
}

// runCompare compares two recorded captures. It never writes to either.
func runCompare(args []string, stdout io.Writer) error {
	opts, err := parseCompareFlags(args)
	if err != nil {
		return err
	}
	return compareCaptures(opts, stdout)
}

func parseCompareFlags(args []string) (compareOptions, error) {
	flags := flag.NewFlagSet("compare", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	var opts compareOptions
	flags.StringVar(&opts.baseline.session, "baseline-session", "",
		"compare only the events recorded for one session identity in the baseline capture")
	flags.StringVar(&opts.candidate.session, "candidate-session", "",
		"compare only the events recorded for one session identity in the candidate capture")

	// Parsing resumes after each directory, so that a selector may be written
	// on either side of them. The standard parser stops at the first argument
	// that is not a flag, which would silently ignore a --baseline-session
	// written after the two directories and compare a different session than
	// the one that was asked for.
	var positional []string
	for rest := args; ; {
		if err := flags.Parse(rest); err != nil {
			return opts, &UsageError{Msg: err.Error()}
		}
		if flags.NArg() == 0 {
			break
		}
		positional = append(positional, flags.Arg(0))
		rest = flags.Args()[1:]
	}
	// An empty value is refused rather than treated as no selection: a caller
	// whose variable was empty asked for one session, and comparing whatever
	// the directory happened to hold would answer a different question.
	for _, name := range []string{"baseline-session", "candidate-session"} {
		if asked(flags, name) && flags.Lookup(name).Value.String() == "" {
			return opts, &UsageError{Msg: "--" + name + " needs a session identifier"}
		}
	}

	if len(positional) != 2 {
		return opts, &UsageError{
			Msg: "compare needs two data directories: axiom compare <baseline> <candidate>",
		}
	}
	opts.baseline.dir, opts.candidate.dir = positional[0], positional[1]
	return opts, nil
}

// capture is one side of a comparison, resolved to exactly one session.
type capture struct {
	// side is what this capture is called in the report and in a refusal.
	side string
	dir  string
	// session is the one session identity the capture was resolved to.
	session string
	log     analysis.Log
}

func compareCaptures(opts compareOptions, stdout io.Writer) error {
	baseline, err := resolve("baseline", opts.baseline)
	if err != nil {
		return err
	}
	candidate, err := resolve("candidate", opts.candidate)
	if err != nil {
		return err
	}

	writeComparison(stdout, baseline, candidate)
	return nil
}

// resolve reads one capture and establishes that it is one.
//
// Everything that can make a directory something other than one capture is
// refused here rather than qualified later. A comparison whose sides are not
// established is not a comparison a caveat can rescue: the numbers would
// already have been read by then.
func resolve(side string, opts captureOptions) (capture, error) {
	log, err := analysis.Analyze(opts.dir, analysis.Options{Session: opts.session})
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return capture{}, &UsageError{Msg: fmt.Sprintf(
			"no events are recorded in the %s directory %s", side, opts.dir)}
	case err != nil:
		return capture{}, err
	}

	// A selection that matched nothing is a mistake worth naming. Comparing an
	// empty side would read as a capture that did no work.
	if opts.session != "" && log.Records == 0 {
		return capture{}, &UsageError{Msg: fmt.Sprintf(
			"no events are recorded for session %q in the %s directory %s",
			opts.session, side, opts.dir)}
	}

	placed := log.Context.Sessions
	switch {
	case len(placed) > 1 || log.Findings.Sessions > 1:
		return capture{}, &UsageError{Msg: manySessions(side, opts.dir, placed)}
	case len(placed) == 0:
		return capture{}, &UsageError{Msg: fmt.Sprintf(
			"the %s directory %s records no session identity, so it holds no capture to compare",
			side, opts.dir)}
	}

	return capture{side: side, dir: opts.dir, session: placed[0].ID, log: log}, nil
}

// manySessions explains why a directory holding several sessions is not a
// capture, and what to do about it.
//
// Axiom cannot infer which session the operator meant. Choosing the largest,
// the first or the last would be an inference about intent, and analyzing all
// of them together would add up work recorded under identities that nothing
// links: an agent identity, a turn identifier and every relation built on them
// mean nothing outside the session that issued them.
func manySessions(side, dir string, sessions []timeline.Session) string {
	var b strings.Builder
	fmt.Fprintf(&b, "the %s directory %s holds more than one session identity, "+
		"so Axiom cannot tell which one is the capture to compare:\n", side, dir)
	for _, s := range sessions {
		fmt.Fprintf(&b, "    %s\n", s.ID)
	}
	fmt.Fprintf(&b, "  select one with --%s-session <id>", side)
	return b.String()
}

func writeComparison(w io.Writer, baseline, candidate capture) {
	fmt.Fprint(w, "Axiom Compare\n─────────────\n")

	writeCaptureShape(w, baseline, candidate)
	writeCompositionComparison(w, baseline, candidate)
	writeDelegationComparison(w, baseline, candidate)
	writeCrossReadComparison(w, baseline, candidate)
	writeReacquireComparison(w, baseline, candidate)

	fmt.Fprint(w, "\nWhat this compares\n")
	sentence(w, compareContract)
	sentence(w, compareMechanic)
	sentence(w, compareTrajectory)
	sentence(w, comparePaths)
	sentence(w, compareUsage)
	sentence(w, compareFindings)
}

// writeCaptureShape says what each side is, before anything is compared.
//
// The rows here carry no difference column. They are what makes the blocks
// below interpretable — how much of each capture Axiom could read, and how
// much of it there was — and a difference printed beside them would turn the
// count of recorded calls into the headline the rest of the report avoids.
func writeCaptureShape(w io.Writer, baseline, candidate capture) {
	fmt.Fprint(w, "\nCapture shape\n\n")
	for _, c := range []capture{baseline, candidate} {
		fmt.Fprintf(w, "  %-10s %s\n", c.side, c.dir)
		fmt.Fprintf(w, "  %-10s session %s\n", "", c.session)
	}

	fmt.Fprintln(w)
	writeCompareHeader(w, false)
	shapeRow(w, "Context epochs", number(baseline.log.Context.Epochs()), number(candidate.log.Context.Epochs()))
	shapeRow(w, "Epochs with recorded work", number(epochsWithWork(baseline)), number(epochsWithWork(candidate)))
	shapeRow(w, "Recorded tool calls", number(baseline.log.Findings.ToolCalls), number(candidate.log.Findings.ToolCalls))
	shapeRow(w, "Records skipped", number(baseline.log.Stats.Skipped()), number(candidate.log.Stats.Skipped()))
	shapeRow(w, "Usage log", usageState(baseline), usageState(candidate))
}

// epochsWithWork counts the epochs that recorded a tool call.
//
// An epoch with none is still an epoch: a boundary the agent reported and a
// later read could have crossed. Both counts are shown because they answer
// different questions, and the second alone would hide a boundary.
func epochsWithWork(c capture) int {
	n := 0
	for _, s := range c.log.Context.Sessions {
		for _, e := range s.Epochs {
			if e.ToolCalls > 0 {
				n++
			}
		}
	}
	return n
}

// usageState says whether measurements exist beside the capture. It is never a
// measurement: an absent log is consumption that was not recorded.
func usageState(c capture) string {
	switch {
	case c.log.Usage.Unreadable != nil:
		return "unreadable"
	case c.log.Usage.Present:
		return "present"
	default:
		return "absent"
	}
}

// writeCompositionComparison compares what the recorded calls were.
//
// Every category internal/work distinguishes is printed, including the ones
// that hold nothing on both sides. The categories are a partition of the
// recorded calls, and one left out because it was empty, or because it moves
// between recordings, would leave a table that no longer accounts for the
// calls the capture shape counted.
func writeCompositionComparison(w io.Writer, baseline, candidate capture) {
	b, c := baseline.log.Composition, candidate.log.Composition

	fmt.Fprint(w, "\nRecorded work by shape\n\n")
	writeCompareHeader(w, true)
	compareRow(w, "Whole-file reads", b.WholeReads, c.WholeReads)
	compareRow(w, "Ranged reads", b.RangedReads, c.RangedReads)
	compareRow(w, "Searches", b.Searches, c.Searches)
	compareRow(w, "Shell", b.Shell, c.Shell)
	compareOutcomes(w, "Writes", b.Writes, c.Writes)
	compareOutcomes(w, "Edits", b.Edits, c.Edits)
	compareOutcomes(w, "Subagent launches", b.Launches, c.Launches)
	compareRow(w, "Uninterpreted", b.Uninterpreted, c.Uninterpreted)
}

// compareOutcomes prints a category and what the record established became of
// its calls.
//
// The three states are never folded into the total above them. A write
// reported failing may still have applied in part, a launch reported failing
// started no nested agent, and a call whose outcome was never established is
// neither: a single number would report all three as the same event.
func compareOutcomes(w io.Writer, label string, b, c work.Outcomes) {
	compareRow(w, label, b.Total(), c.Total())
	compareSubRow(w, "succeeded", b.Succeeded, c.Succeeded)
	compareSubRow(w, "failed", b.Failed, c.Failed)
	compareSubRow(w, "outcome not established", b.Unestablished, c.Unestablished)
}

// writeDelegationComparison compares the structure the launches established.
//
// Nothing here is derived: internal/delegation decides what a launch is, what
// a returned identity establishes and which pairs are relations, and this
// counts what it reported.
func writeDelegationComparison(w io.Writer, baseline, candidate capture) {
	fmt.Fprint(w, "\nDelegation\n\n")
	writeCompareHeader(w, true)
	compareRow(w, "Launches recorded",
		len(baseline.log.Delegation.Launches), len(candidate.log.Delegation.Launches))
	compareRow(w, "Launches returning an agent identity",
		identified(baseline), identified(candidate))
	compareRow(w, "Relations established",
		len(baseline.log.Delegation.Relations), len(candidate.log.Delegation.Relations))
	compareRow(w, "Launching scopes",
		baseline.log.CrossRead.Groups, candidate.log.CrossRead.Groups)
}

// identified counts the launches whose record carried the identity of the
// agent they created. A launch without one relates to nothing, which is what
// every record written before that identity was persisted says, and so does a
// launch that reported failing.
func identified(c capture) int {
	n := 0
	for _, l := range c.log.Delegation.Launches {
		if l.Work != nil {
			n++
		}
	}
	return n
}

// writeCrossReadComparison compares reading across related agent scopes.
//
// The path count is never shown alone. No launch at all, launches that named
// no scope to relate, and related scopes that read nothing in common are three
// different observations that all report zero paths, and the denominators are
// what tells them apart.
func writeCrossReadComparison(w io.Writer, baseline, candidate capture) {
	b, c := baseline.log.CrossRead, candidate.log.CrossRead

	fmt.Fprint(w, "\nRead across related agent scopes\n\n")
	writeCompareHeader(w, true)
	compareRow(w, "Paths read in more than one related scope", len(b.Paths), len(c.Paths))
	compareRow(w, "Launches recorded", b.Launches, c.Launches)
	compareRow(w, "Relations established", b.Relations, c.Relations)
	compareRow(w, "Launching scopes", b.Groups, c.Groups)
}

// writeReacquireComparison compares reading across context epochs.
//
// The same rule as above: a capture with one epoch had no boundary for a path
// to be read across, which is a different fact from a capture that had
// boundaries and read nothing across them. Both report zero.
func writeReacquireComparison(w io.Writer, baseline, candidate capture) {
	b, c := baseline.log.Reacquire, candidate.log.Reacquire

	fmt.Fprint(w, "\nRead again in a later context epoch\n\n")
	writeCompareHeader(w, true)
	compareRow(w, "Paths read in more than one epoch", len(b.Paths), len(c.Paths))
	compareRow(w, "Sessions with more than one epoch", b.MultiEpochSessions, c.MultiEpochSessions)
	compareRow(w, "Context epochs",
		baseline.log.Context.Epochs(), candidate.log.Context.Epochs())
	compareRow(w, "Epochs with recorded work", epochsWithWork(baseline), epochsWithWork(candidate))
}

func writeCompareHeader(w io.Writer, difference bool) {
	fmt.Fprintf(w, "  %-*s%*s%*s", compareLabelWidth, "",
		compareValueWidth, "baseline", compareValueWidth, "candidate")
	if difference {
		fmt.Fprint(w, "   difference")
	}
	fmt.Fprintln(w)
}

func compareRow(w io.Writer, label string, b, c int) {
	fmt.Fprintf(w, "  %-*s%*s%*s   %s\n", compareLabelWidth, label,
		compareValueWidth, number(b), compareValueWidth, number(c), difference(b, c))
}

func compareSubRow(w io.Writer, label string, b, c int) {
	fmt.Fprintf(w, "  %s%-*s%*s%*s   %s\n", compareSubIndent, compareSubWidth, label,
		compareValueWidth, number(b), compareValueWidth, number(c), difference(b, c))
}

// shapeRow states one property of each capture, with no difference beside it.
func shapeRow(w io.Writer, label, b, c string) {
	fmt.Fprintf(w, "  %-*s%*s%*s\n", compareLabelWidth, label,
		compareValueWidth, b, compareValueWidth, c)
}

func number(n int) string { return strconv.Itoa(n) }

// difference states how the two counts differ, and only that.
//
// An unchanged count is written as a word rather than as a zero, so that a
// dimension that did not move cannot be misread as a dimension that measured
// nothing. Everything else is the signed count, with no unit, no rate and no
// adjective: what a difference means is not something Axiom observed.
func difference(b, c int) string {
	switch d := c - b; {
	case d == 0:
		return "same"
	case d > 0:
		return "+" + strconv.Itoa(d)
	default:
		return strconv.Itoa(d)
	}
}
