package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strconv"
	"strings"
	"time"

	"github.com/exequieldeferrari/axiom/internal/profiler"
	"github.com/exequieldeferrari/axiom/internal/store"
)

// runProfile analyzes the recorded event log. It never writes to it.
func runProfile(args []string, stdout io.Writer) error {
	if len(args) > 0 {
		return &UsageError{Msg: fmt.Sprintf("unexpected argument %q", args[0])}
	}

	dir, err := store.DefaultDir()
	if err != nil {
		return err
	}
	return profileLog(dir, stdout)
}

func profileLog(dir string, stdout io.Writer) error {
	scanner, err := store.ScanEvents(dir)
	if errors.Is(err, fs.ErrNotExist) {
		fmt.Fprint(stdout, "No events recorded yet.\nRun 'axiom init', then use Claude Code.\n")
		return nil
	}
	if err != nil {
		return err
	}
	defer scanner.Close()

	p := profiler.New()
	for scanner.Scan() {
		p.Add(scanner.Record())
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	writeReport(stdout, p.Report(), scanner.Stats())
	return nil
}

const (
	countLabelWidth = 20
	// Wide enough to leave a gap after the longest detail label.
	detailLabelWidth  = 34
	headlineWidth     = 42
	digestDisplayLen  = 12
	findingIndent     = "        "
	scopeExplanation  = "Analysis is scoped to a single session and subagent: work repeated in a\nlater session is not counted, because the agent's context may legitimately\nhave been lost in between.\n"
	observedCaveat    = "Repeated-call tool time is how long the repeated calls took to execute, not\ncounting the first. It is not the total time of the operation, and it\nmeasures nothing about context, tokens, or cost. Axiom reports what it\nobserved; a file may still have been changed by something outside the agent.\n"
	noFindingsMessage = "  No high-confidence redundant work detected.\n"
)

func writeReport(w io.Writer, r profiler.Report, stats store.ScanStats) {
	fmt.Fprint(w, "Axiom Profile\n─────────────\n\n")
	count(w, "Events", r.Events)
	count(w, "Sessions analyzed", r.Sessions)
	count(w, "Tool calls", r.ToolCalls)

	if skipped := stats.Skipped(); skipped > 0 {
		fmt.Fprintf(w, "\nWarning: %s skipped (%s); findings may be incomplete.\n",
			plural(skipped, "record"), describeSkipped(stats))
	}

	fmt.Fprint(w, "\nRedundant work\n\n")
	if len(r.Findings) == 0 {
		fmt.Fprint(w, noFindingsMessage)
		fmt.Fprint(w, "\n"+scopeExplanation)
		return
	}

	for _, f := range r.Findings {
		writeFinding(w, f)
	}
	fmt.Fprintf(w, "%s.\n\n%s", plural(len(r.Findings), "finding"), observedCaveat)
}

func writeFinding(w io.Writer, f profiler.Finding) {
	fmt.Fprintf(w, "  %-5s %-*s %s\n",
		strings.ToUpper(string(f.Confidence)), headlineWidth, headline(f.Kind), attribution(f))
	fmt.Fprintf(w, "%s%s\n", findingIndent, evidence(f))

	switch f.Kind {
	case profiler.KindRepeatedRead:
		detail(w, "Potentially redundant reads", strconv.Itoa(f.Redundant))
	case profiler.KindRepeatedShell:
		detail(w, "Potentially redundant executions", strconv.Itoa(f.Redundant))
	}
	detail(w, "Repeated-call tool time", observedTime(f))

	switch f.Kind {
	case profiler.KindRepeatedRead:
		detail(w, "File", f.Path)
	case profiler.KindRepeatedShell:
		detail(w, "Command digest", shortDigest(f.CommandDigest))
	}
	detail(w, "Window", window(f.First, f.Last))
	fmt.Fprintln(w)
}

// attribution names the context the work belongs to. A subagent has its own
// context, so naming only the session would credit the wrong actor and make
// two findings from different contexts look like one repeated twice.
func attribution(f profiler.Finding) string {
	if f.SubagentID == "" {
		return "session " + shortID(f.SessionID)
	}
	// The subagent identifier is printed as recorded. Its format is the agent's
	// to choose, so shortening it the way a session UUID is shortened could cut
	// a meaningful name down to a prefix two subagents share.
	return "session " + shortID(f.SessionID) + " · subagent " + f.SubagentID
}

func headline(k profiler.Kind) string {
	switch k {
	case profiler.KindRepeatedRead:
		return "Repeated file read"
	case profiler.KindRepeatedShell:
		return "Repeated shell operation"
	default:
		return string(k)
	}
}

// evidence states what was observed, in the terms Axiom can actually defend.
func evidence(f profiler.Finding) string {
	switch f.Kind {
	case profiler.KindRepeatedRead:
		return fmt.Sprintf("Read %d times, with no agent modification observed in between", f.Occurrences)
	case profiler.KindRepeatedShell:
		return fmt.Sprintf("Executed %d times, with only read-only operations in between", f.Occurrences)
	default:
		return fmt.Sprintf("Repeated %d times", f.Occurrences)
	}
}

func observedTime(f profiler.Finding) string {
	if f.ObservedTotal == nil {
		return "not reported"
	}
	return f.ObservedTotal.String()
}

// window renders the span of a run. Times are shown in UTC, matching how they
// are recorded, so a report means the same thing wherever it is read.
func window(first, last time.Time) string {
	f, l := first.UTC(), last.UTC()
	if f.Format(time.DateOnly) == l.Format(time.DateOnly) {
		return fmt.Sprintf("%s %s → %s UTC",
			f.Format(time.DateOnly), f.Format(time.TimeOnly), l.Format(time.TimeOnly))
	}
	return fmt.Sprintf("%s → %s UTC", f.Format(time.DateTime), l.Format(time.DateTime))
}

func shortDigest(d string) string {
	if len(d) <= digestDisplayLen {
		return d
	}
	return d[:digestDisplayLen] + "…"
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func describeSkipped(s store.ScanStats) string {
	var parts []string
	if s.Malformed > 0 {
		parts = append(parts, fmt.Sprintf("%d malformed", s.Malformed))
	}
	if s.Truncated > 0 {
		parts = append(parts, fmt.Sprintf("%d truncated", s.Truncated))
	}
	if s.UnknownVersion > 0 {
		parts = append(parts, fmt.Sprintf("%d from a newer schema", s.UnknownVersion))
	}
	return strings.Join(parts, ", ")
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func count(w io.Writer, label string, n int) {
	fmt.Fprintf(w, "%-*s%d\n", countLabelWidth, label, n)
}

func detail(w io.Writer, label, value string) {
	fmt.Fprintf(w, "%s%-*s%s\n", findingIndent, detailLabelWidth, label, value)
}
