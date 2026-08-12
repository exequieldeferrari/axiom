package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"strconv"
	"strings"
	"time"

	"github.com/exequieldeferrari/axiom/internal/activity"
	"github.com/exequieldeferrari/axiom/internal/correlate"
	"github.com/exequieldeferrari/axiom/internal/profiler"
	"github.com/exequieldeferrari/axiom/internal/reacquire"
	"github.com/exequieldeferrari/axiom/internal/store"
	"github.com/exequieldeferrari/axiom/internal/timeline"
)

// runProfile analyzes the recorded event log. It never writes to it.
func runProfile(args []string, stdout io.Writer) error {
	opts, err := parseProfileFlags(args)
	if err != nil {
		return err
	}

	dir, err := store.DefaultDir()
	if err != nil {
		return err
	}
	return profileLog(dir, opts, stdout)
}

// profileOptions selects what a report covers.
type profileOptions struct {
	// session limits the analysis to one session identity. Empty analyzes the
	// whole log, which is the default and is unchanged by this option
	// existing.
	session string
}

func parseProfileFlags(args []string) (profileOptions, error) {
	flags := flag.NewFlagSet("profile", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	var opts profileOptions
	flags.StringVar(&opts.session, "session", "", "analyze only the events recorded for one session identity")

	if err := flags.Parse(args); err != nil {
		return opts, &UsageError{Msg: err.Error()}
	}
	if rest := flags.Args(); len(rest) > 0 {
		return opts, &UsageError{Msg: fmt.Sprintf("unexpected argument %q", rest[0])}
	}
	// An empty value is refused rather than treated as no selection at all. A
	// caller whose variable was empty asked for one session and would
	// otherwise be given a report of every session, saying nothing about the
	// difference.
	if asked(flags, "session") && opts.session == "" {
		return opts, &UsageError{Msg: "--session needs a session identifier"}
	}
	return opts, nil
}

// asked reports whether a flag was given on the command line, as opposed to
// holding its default.
func asked(flags *flag.FlagSet, name string) bool {
	given := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == name {
			given = true
		}
	})
	return given
}

func profileLog(dir string, opts profileOptions, stdout io.Writer) error {
	scanner, err := store.ScanEvents(dir)
	if errors.Is(err, fs.ErrNotExist) {
		fmt.Fprint(stdout, "No events recorded yet.\nRun 'axiom init', then use Claude Code.\n")
		return nil
	}
	if err != nil {
		return err
	}
	defer scanner.Close()

	// Measurements are indexed first because the profile resolves them as each
	// read arrives, and a measurement read afterwards would arrive too late to
	// be attached to anything.
	usage := loadUsage(dir)
	p := profiler.New()
	a := activity.New(func(session, turn, invocation string) (int64, bool) {
		return usage.index.ResultBytes(correlate.Key{
			SessionID: session, TurnID: turn, InvocationID: invocation,
		})
	})
	t := timeline.New()
	q := reacquire.New()

	analyzed := 0
	for scanner.Scan() {
		record := scanner.Record()
		// A session is selected by the identifier the agent recorded, exactly.
		// Matching a prefix would silently analyze a different session that
		// happened to start the same way.
		if opts.session != "" && record.SessionID != opts.session {
			continue
		}
		analyzed++
		p.Add(record)
		a.Add(record)
		// Epoch membership comes from the timeline as it observes the record,
		// in the same pass, because append order is the only thing that
		// establishes it.
		q.Add(record, t.Add(record))
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	// An identifier that matched nothing is a mistake worth naming. Reporting
	// an empty profile instead would look like a session that did no work.
	if opts.session != "" && analyzed == 0 {
		fmt.Fprintf(stdout, "No events recorded for session %q.\n", opts.session)
		// A record Axiom could not decode cannot be attributed to a session,
		// so one of them may have been this one. Saying nothing here would
		// turn "not found" into a stronger claim than the log supports.
		if skipped := scanner.Stats().Skipped(); skipped > 0 {
			fmt.Fprintf(stdout, "%s skipped and could not be attributed to any session.\n",
				plural(skipped, "record"))
		}
		fmt.Fprint(stdout, "Run 'axiom profile' to see the sessions in the log.\n")
		return nil
	}

	report := p.Report()
	writeReport(stdout, reportInput{
		findings:  report,
		activity:  a.Profile(),
		context:   t.Report(),
		reacquire: q.Report(),
		measured:  usage.index.Measure(report.Findings),
		stats:     scanner.Stats(),
		usage:     usage,
		scope:     opts,
	})
	return nil
}

// usageLog is the outcome of reading the usage stream.
//
// A log that is absent and a log that could not be read leave findings equally
// unmeasured, but they do not mean the same thing: the first is the ordinary
// state of a machine where no receiver has run, and the second is a problem
// only the user can resolve.
type usageLog struct {
	index *correlate.Index
	stats store.ScanStats
	// unreadable is set when measurements may exist but Axiom could not read
	// them, and is nil when there were simply none to read.
	unreadable error
}

// loadUsage indexes the measurements recorded beside the event log.
//
// Telemetry is optional and always will be: it exists only for the time a
// receiver was running. Nothing here fails the analysis; the worst outcome is
// findings without measurements.
func loadUsage(dir string) usageLog {
	log := usageLog{index: correlate.NewIndex()}

	scanner, err := store.ScanUsage(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// No receiver has ever recorded here, which is the common case.
		return log
	case err != nil:
		log.unreadable = err
		return log
	}
	defer scanner.Close()

	for scanner.Scan() {
		log.index.Add(scanner.Record())
	}
	log.stats = scanner.Stats()
	if err := scanner.Err(); err != nil {
		// A log that could not be read in full is discarded rather than used
		// in part: the record that would have made a measurement ambiguous
		// may be exactly the one that was lost.
		log.index = correlate.NewIndex()
		log.unreadable = err
	}
	return log
}

const (
	countLabelWidth = 20
	// Wide enough to leave a gap after the longest detail label.
	detailLabelWidth = 34
	// Wide enough for the longest confidence level.
	confidenceWidth  = 6
	headlineWidth    = 42
	digestDisplayLen = 12
	// Indented to the column a headline starts in: two spaces, the
	// confidence, and the space after it.
	findingIndent = "         "
	// Associated consumption is indented under its own heading, and its
	// labels are narrowed to match so the values stay in one column.
	associatedIndent     = findingIndent + "  "
	associatedLabelWidth = detailLabelWidth - 2
	// The scope is filled in by consumptionScope, and the break keeps the
	// longest of them inside the report's width.
	associationCaveat = "This is the observed model consumption\nfor %s, not the cost of the repetition.\n"
	// The scope is stated as the session and the reset rather than as the
	// epoch, because the two are not the same in every log: the profiler ends
	// its runs at recorded starts, and an epoch above may also have been
	// closed by a session end, which ends no run.
	scopeExplanation  = "Analysis is scoped to a single session and subagent, and every recorded\ncontext reset ends it: work repeated after a reset, or in a later session,\nis not counted, because the agent's context may legitimately have been lost\nin between.\n"
	observedCaveat    = "Repeated-call tool time is how long the repeated calls took to execute, not\ncounting the first. It is not the total time of the operation, and it\nmeasures nothing about context, tokens, or cost. Axiom reports what it\nobserved; a file may still have been changed by something outside the agent.\n"
	measuredCaveat    = "Redundant tool output is the size of the results the repeated calls returned,\nas the agent itself measured them. It is a count of bytes, not tokens and not\ncost, and it appears only where every repeated call was measured.\n"
	failureCaveat     = "A repeated failed attempt is one shell command tried again after it failed,\nwithin a single turn, with nothing in between that Axiom can see changing\nstate. The same observed failure means the agent reported the failures\nidentically, not that they had the same cause. Where a later attempt was\nobserved succeeding it is reported as that and nothing more: what came\nbetween is not evidence of what made the difference.\n"
	noFindingsMessage = "  No high-confidence redundant work or repeated failed attempts detected.\n"
)

// reportInput is everything one report is written from.
type reportInput struct {
	findings  profiler.Report
	activity  activity.Profile
	context   timeline.Report
	reacquire reacquire.Report
	measured  []correlate.Measured
	stats     store.ScanStats
	usage     usageLog
	scope     profileOptions
}

func writeReport(w io.Writer, in reportInput) {
	r, usage, findings := in.findings, in.usage, in.measured

	fmt.Fprint(w, "Axiom Profile\n─────────────\n\n")
	// Printed only where the analysis covered less than the log, so that a
	// whole-log report reads exactly as it did before selection existed.
	if in.scope.session != "" {
		fmt.Fprintf(w, "%-*s%s\n", countLabelWidth, "Scope", "session "+in.scope.session)
	}
	count(w, "Events", r.Events)
	count(w, "Sessions analyzed", r.Sessions)
	count(w, "Tool calls", r.ToolCalls)

	// Skipped records are counted for the whole log, whether or not the report
	// was scoped to one session. A record Axiom could not decode cannot be
	// attributed to a session: what was lost is exactly what would have said.
	if skipped := in.stats.Skipped(); skipped > 0 {
		fmt.Fprintf(w, "\nWarning: %s skipped (%s); findings may be incomplete.\n",
			plural(skipped, "record"), describeSkipped(in.stats))
	}
	// A usage record Axiom cannot read costs a measurement, not a finding.
	if skipped := usage.stats.Skipped(); skipped > 0 {
		fmt.Fprintf(w, "\nWarning: %s skipped (%s); some measurements are missing.\n",
			plural(skipped, "usage record"), describeSkipped(usage.stats))
	}
	// Telemetry that is absent needs no explanation. Telemetry that exists and
	// cannot be read does, or the missing measurements look like an absence of
	// redundant output rather than an absence of evidence.
	if usage.unreadable != nil {
		fmt.Fprintf(w, "\nWarning: the usage log could not be read (%v); findings are unmeasured.\n",
			usage.unreadable)
	}

	writeTimeline(w, in.context)
	writeReacquired(w, in.reacquire)
	writeActivity(w, in.activity)

	fmt.Fprint(w, "\nFindings\n\n")
	if len(findings) == 0 {
		fmt.Fprint(w, noFindingsMessage)
		fmt.Fprint(w, "\n"+scopeExplanation)
		return
	}

	measured, failures := false, false
	for _, f := range findings {
		writeFinding(w, f)
		measured = measured || f.RedundantBytes != nil
		failures = failures || f.Kind == profiler.KindRepeatedFailure
	}
	fmt.Fprintf(w, "%s.\n\n%s", plural(len(findings), "finding"), observedCaveat)
	if measured {
		fmt.Fprint(w, "\n"+measuredCaveat)
	}
	if failures {
		fmt.Fprint(w, "\n"+failureCaveat)
	}
}

func writeFinding(w io.Writer, f correlate.Measured) {
	fmt.Fprintf(w, "  %-*s %-*s %s\n", confidenceWidth,
		strings.ToUpper(string(f.Confidence)), headlineWidth, headline(f.Kind), attribution(f.Finding))
	fmt.Fprintf(w, "%s%s\n", findingIndent, evidence(f.Finding))

	switch f.Kind {
	case profiler.KindRepeatedRead:
		detail(w, "Potentially redundant reads", strconv.Itoa(f.Redundant))
	case profiler.KindRepeatedShell:
		detail(w, "Potentially redundant executions", strconv.Itoa(f.Redundant))
	case profiler.KindRepeatedFailure:
		detail(w, "Failed attempts", strconv.Itoa(f.Occurrences))
		detail(w, "Repeated after a failure", strconv.Itoa(f.Redundant))
		// An exit status the attempts disagreed on is left out rather than
		// summarized: the finding would be naming a status that no longer
		// describes every attempt under it.
		if f.ExitCode != nil {
			detail(w, "Same exit code", strconv.Itoa(*f.ExitCode))
		}
	}
	// Shown only when every repeated call was measured exactly once. An
	// absent line means the total is unknown, which is the usual case: it is
	// not a measurement of zero.
	//
	// A failed call is never measured this way. The agent reports no size for
	// one, and labelling anything here as redundant output would describe a
	// failed attempt as work that produced something.
	if f.RedundantBytes != nil && f.Kind != profiler.KindRepeatedFailure {
		detail(w, "Redundant tool output", size(f.RedundantBytes))
	}
	detail(w, "Repeated-call tool time", observedTime(f.Finding))

	switch f.Kind {
	case profiler.KindRepeatedRead:
		detail(w, "File", f.Path)
	case profiler.KindRepeatedShell:
		detail(w, "Command digest", shortDigest(f.CommandDigest))
	case profiler.KindRepeatedFailure:
		detail(w, "Command digest", shortDigest(f.CommandDigest))
		if f.FailureDigest != "" {
			detail(w, "Failure digest", shortDigest(f.FailureDigest))
		}
		// Reported only where it was observed. Nothing is printed otherwise,
		// because a command that was never tried again tells Axiom nothing
		// about whether the agent got past it.
		if f.LaterSuccess {
			detail(w, "Same command later succeeded", "yes")
		}
	}
	detail(w, "Window", window(f.First, f.Last))
	if f.Associated != nil {
		writeAssociated(w, *f.Associated)
	}
	fmt.Fprintln(w)
}

// writeAssociated reports what the agent consumed while doing everything else
// it did in the same turns.
//
// It is set apart from the finding's own measurements, and says in a sentence
// what it is, because a token count printed beside a defect reads as the cost
// of that defect unless something stops it. Nothing here is attributable to
// the repetition, and the heading has to carry that before the numbers do.
func writeAssociated(w io.Writer, c correlate.Consumption) {
	heading, scope := consumptionScope(c)
	fmt.Fprintf(w, "\n%s%s\n", findingIndent, heading)

	associated(w, "Model requests", thousands(int64(c.Requests)))
	// A withheld dimension is left out rather than printed as zero: the agent
	// reporting nothing and the agent reporting none are different facts.
	if c.Tokens != nil {
		associated(w, "Input tokens", thousands(c.Tokens.Input))
		associated(w, "Output tokens", thousands(c.Tokens.Output))
		associated(w, "Cache read", thousands(c.Tokens.CacheRead))
		associated(w, "Cache creation", thousands(c.Tokens.CacheCreation))
	}
	if c.CostMicros != nil {
		associated(w, "Model cost", dollars(*c.CostMicros))
	}
	for line := range strings.Lines(fmt.Sprintf(associationCaveat, scope)) {
		fmt.Fprintf(w, "%s%s", associatedIndent, line)
	}
}

// consumptionScope says how much of a finding the block below it covers.
//
// The turns a finding spans come from the behavior stream and are stated as
// they are. Telemetry decides how many of them Axiom saw, so incomplete
// evidence is reported as incomplete coverage rather than as a finding that
// happened in fewer places than it did.
//
// The returned scope names the turns the totals actually came from, so the
// caveat below the numbers cannot be read as covering the rest.
func consumptionScope(c correlate.Consumption) (heading, scope string) {
	if c.ObservedTurns < c.AffectedTurns {
		heading = fmt.Sprintf("Observed model consumption in %d of the %d turns where this happened",
			c.ObservedTurns, c.AffectedTurns)
		if c.ObservedTurns == 1 {
			return heading, "the turn it was recorded in"
		}
		return heading, "the turns it was recorded in"
	}

	if c.AffectedTurns == 1 {
		return "Observed model consumption in the turn where this happened", "that turn"
	}
	return fmt.Sprintf("Observed model consumption in the %d turns where this happened", c.AffectedTurns),
		"those turns"
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
	case profiler.KindRepeatedFailure:
		return "Repeated failed attempt"
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
	case profiler.KindRepeatedFailure:
		// The two levels differ in what is known about the failures, so they
		// have to read differently. Medium covers attempts that reported
		// different failures and attempts that reported none, which are
		// different observations but the same position: identical reporting
		// was not established. Neither wording says anything about a cause.
		if f.Confidence == profiler.ConfidenceHigh {
			return fmt.Sprintf("Failed %d times, each reporting the same observed failure", f.Occurrences)
		}
		return fmt.Sprintf("Failed %d times; identical failure reporting was not established", f.Occurrences)
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

func associated(w io.Writer, label, value string) {
	fmt.Fprintf(w, "%s%-*s%s\n", associatedIndent, associatedLabelWidth, label, value)
}

// thousands groups a count so that six figures of cache reads can be read at a
// glance instead of counted digit by digit.
func thousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}

	var b strings.Builder
	for i, digit := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(digit)
	}
	return sign + b.String()
}

// dollars renders the agent's own cost estimate. The estimate is recorded in
// millionths, and four places keep a fraction of a cent visible without
// implying the agent measured more precision than it reported.
func dollars(micros int64) string {
	return fmt.Sprintf("$%.4f", float64(micros)/1e6)
}
