package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/exequieldeferrari/axiom/internal/correlate"
	"github.com/exequieldeferrari/axiom/internal/delegation"
	"github.com/exequieldeferrari/axiom/internal/turns"
	"github.com/exequieldeferrari/axiom/internal/work"
)

const (
	// How many turns a report shows. The most recently recorded are kept, and
	// everything omitted is accounted for on a line of its own.
	turnsShown = 8
	// A turn's counts are indented under the line that names it, and its
	// consumption one level further, with labels narrowed to match so the
	// values stay in one column.
	turnIndent          = "       "
	turnLabelWidth      = 30
	turnUsageIndent     = turnIndent + "  "
	turnUsageLabelWidth = turnLabelWidth - 2
	// How many epoch ordinals a turn lists before the rest are counted. A turn
	// spanning many context resets would otherwise run a line past the width
	// the report is written to.
	epochOrdinalsShown = 5
	// How many of a turn's launches are described one by one before the rest
	// are counted. A turn that delegated many times would otherwise push the
	// turn it belongs to off the page.
	launchesShown = 5
	// A launch is described under the turn that recorded it, at the same
	// depth as a turn's consumption. It is written as a flowing line rather
	// than in the label column above it, because what it carries is a
	// sentence about one launch and not another of the turn's measurements.
	turnLaunchIndent = turnUsageIndent
	launchSeparator  = "  ·  "
	// What the attributable calls were is written under the launch, one
	// step further in, as the paths under a finding are.
	launchDetailIndent = turnLaunchIndent + "  "

	turnsHeading = "Observed model consumption"

	turnsCaveat = "A recorded turn is a turn identifier at least one recorded tool call named.\nA turn is the context an agent labels with an identifier of its own: it is\nnot established to be one request, one task, or one complete unit of work.\nIdentifiers carried only by a session start or end, or only by a usage\nrecord, named no recorded work and are not listed, because a turn built from\none would be a turn that did nothing. Turns are numbered within a session\nidentity, in the order their work was recorded, and an identifier means\nnothing outside the session that issued it.\n"
	turnsWindow = "The time beside a turn is the earliest and latest recorded on its calls. It\nis not how long the turn took, and nothing above is ordered by it:\nmembership and order come from the order records were appended.\n"
	turnsSpend  = "Observed model consumption is what the agent reported for requests it\nlabelled with that turn. It is not the cost of the tool calls above it, not\nthe cost of the turn, and not a billing figure: nothing recorded says which\nrequest served which call. A turn with none recorded is not a turn that\nconsumed nothing — telemetry exists only while a receiver is running.\n"
	turnsNested = "A nested agent's tool calls were observed carrying the turn that launched\nthem, and its model requests were observed carrying identifiers of their\nown. A turn's observed consumption therefore does not contain everything a\nsubagent it launched spent.\n"

	// Printed only for a turn that recorded one or the other, because the
	// two counts are read as one number split in half unless something says
	// they are not.
	turnsDelegation = "Subagent launches counts calls that handed work to a nested agent, and\ncalls by a nested agent counts the work one did. Neither is derived from\nthe other, and adding them together counts nothing meaningful: a launch\nreported failing started no agent, a launch whose outcome was not recorded\nsettles nothing either way, and nested calls appear with no launch beside\nthem whenever the log begins after one was already running.\n"

	// Printed only where a launch was described one by one below its turn.
	// The claim those lines make is exact and narrow, and it is the only one
	// they are allowed to carry.
	turnsAttribution = "A launch listed under a turn is one the agent returned an agent identity\nfor, and the work under it is the recorded calls that reported that same\nidentity. That is the whole claim. It does not establish that everything\nthe agent did reached the log, that it finished what it was asked, or\nanything about what it consumed, and these counts are not a breakdown of\ncalls by a nested agent above: that counts every nested call, including\nwork no recorded launch accounts for.\n"

	// Printed only where a launch had no identity to match on, so that the
	// absence is read as an absence of evidence rather than of work.
	turnsUnidentified = "A launch with no returned agent identity recorded is one Axiom cannot\nrelate to any call. Every launch recorded before Axiom persisted the\nidentity says this, and so does one that reported failing.\n"

	noRecordedTurns = "  No recorded tool call named a turn, so no turn recorded work.\n"

	// A launch whose identity nothing reported. It describes the log and
	// never the agent: a call that was never recorded is not a call that was
	// never made.
	launchWithoutWork = "no calls recorded with its returned identity"

	// These carry counts from the log, so their length is not known when they
	// are written and they are wrapped rather than broken by hand.
	turnsOutside      = "%s observed under %s that no recorded tool call named. They belong to no turn above and are not attributed to one."
	turnsUnattributed = "%s named no turn, so they appear in no turn above."
	turnsUnrelated    = "%s reported an agent identity that no recorded launch returned, across %s. They are counted here and attributed to no launch above."
)

// writeTurns reports the turns that recorded work, and what belonged to each.
//
// It is printed after the epochs because it says how the work inside them was
// divided, and before the profile by path because a turn is the unit the
// consumption stream is joined on: the paths below are behavior alone.
func writeTurns(w io.Writer, ms []correlate.MeasuredTurn, outside correlate.Outside, unattributed int, d delegation.Report) {
	byTurn := launchesByTurn(d)

	fmt.Fprint(w, "\nRecorded turns\n\n")
	if len(ms) == 0 {
		fmt.Fprint(w, noRecordedTurns)
		writeTurnAccounting(w, outside, unattributed, d.Unrelated)
		return
	}

	shown := ms
	if len(shown) > turnsShown {
		omitted := shown[:len(shown)-turnsShown]
		shown = shown[len(shown)-turnsShown:]
		calls := 0
		for _, m := range omitted {
			calls += m.ToolCalls
		}
		fmt.Fprintf(w, "  %s omitted (%s)\n\n", plural(len(omitted), "earlier turn"),
			plural(calls, "tool call"))
	}

	// The session is named once above the turns that belong to it, and in
	// full: it is what 'axiom profile --session' takes, and a prefix is not an
	// identity. Ordinals are the session's, so they would be ambiguous
	// without it.
	session := ""
	consumption := false
	delegated := false
	var rendered launchLines
	for _, m := range shown {
		if m.SessionID != session {
			session = m.SessionID
			fmt.Fprintf(w, "  session %s\n", session)
		}
		rendered.add(writeTurn(w, m, byTurn[m.Ref]))
		consumption = consumption || m.Observed != nil
		delegated = delegated || m.Composition.Launches.Total() > 0 || m.SubagentCalls > 0
	}

	fmt.Fprint(w, turnsCaveat)
	fmt.Fprint(w, "\n"+turnsWindow)
	if delegated {
		fmt.Fprint(w, "\n"+turnsDelegation)
	}
	// Each explanation is printed only where the page holds what it
	// explains, as everywhere else in the report.
	if rendered.identified {
		fmt.Fprint(w, "\n"+turnsAttribution)
	}
	if rendered.unidentified {
		fmt.Fprint(w, "\n"+turnsUnidentified)
	}
	if consumption {
		fmt.Fprint(w, "\n"+turnsSpend)
		fmt.Fprint(w, "\n"+turnsNested)
	}
	writeTurnAccounting(w, outside, unattributed, d.Unrelated)
}

// launchesByTurn groups the recorded launches by the turn their launch call
// was recorded in.
//
// The turn is where a launch is shown and takes no part in relating it to
// anything: the relation is the session and the returned identity, and the
// calls under a launch need not have named the turn it was recorded in.
func launchesByTurn(d delegation.Report) map[turns.Ref][]delegation.Launch {
	if len(d.Launches) == 0 {
		return nil
	}

	out := make(map[turns.Ref][]delegation.Launch)
	for _, l := range d.Launches {
		ref := turns.Ref{SessionID: l.SessionID, TurnID: l.TurnID}
		out[ref] = append(out[ref], l)
	}
	return out
}

// launchLines records which kinds of launch line a report ended up printing,
// so that each is explained where it appears and nowhere else.
type launchLines struct {
	identified   bool
	unidentified bool
}

func (l *launchLines) add(other launchLines) {
	l.identified = l.identified || other.identified
	l.unidentified = l.unidentified || other.unidentified
}

func writeTurn(w io.Writer, m correlate.MeasuredTurn, launches []delegation.Launch) launchLines {
	fmt.Fprintf(w, "    turn %d  ·  %s\n", m.Ordinal, window(m.First, m.Last))

	turnDetail(w, epochLabel(len(m.Epochs)), epochsOf(m.Epochs))
	turnDetail(w, "Tool calls", strconv.Itoa(m.ToolCalls))
	c := m.Composition
	// A category with none recorded is left out, as everywhere else in the
	// report: zero is a fact Axiom established, and printing seven of them on
	// every turn would bury the counts that are not.
	turnCount(w, "Whole-file reads", c.WholeReads)
	turnCount(w, "Ranged reads", c.RangedReads)
	turnCount(w, "Searches", c.Searches)
	turnCount(w, "Shell", c.Shell)
	turnOutcomes(w, "Writes", c.Writes)
	turnOutcomes(w, "Edits", c.Edits)
	turnOutcomes(w, "Subagent launches", c.Launches)
	// Described immediately under the count they belong to, before the work
	// a nested agent did: a launch is the delegation, and the line below is
	// every nested call in the turn, which is a different quantity.
	lines := writeTurnLaunches(w, launches)
	turnCount(w, "Calls by a nested agent", m.SubagentCalls)
	turnCount(w, "Uninterpreted", c.Uninterpreted)

	writeTurnConsumption(w, m.Observed)
	fmt.Fprintln(w)
	return lines
}

// writeTurnLaunches describes the turn's launches one by one.
//
// Only a launch that returned an identity gets a line of its own: it is the
// only kind there is anything to say about. The rest are counted on one line,
// because a log written before the identity was persisted would otherwise gain
// a line per launch that says the same thing every time.
//
// Ordinals number every launch the turn recorded, in the order they were
// appended, so a described launch keeps its place among the ones that were
// not. A gap in the numbering is a launch on the line below.
func writeTurnLaunches(w io.Writer, launches []delegation.Launch) launchLines {
	var lines launchLines
	unidentified, shown := 0, 0
	omitted := 0

	for i, l := range launches {
		if l.Work == nil {
			unidentified++
			continue
		}
		if shown == launchesShown {
			omitted++
			continue
		}
		shown++
		lines.identified = true
		turnLaunch(w, i+1, *l.Work)
	}

	if omitted > 0 {
		fmt.Fprintf(w, "%s%s not described\n", turnLaunchIndent,
			quantity(omitted, "1 further launch", fmt.Sprintf("%d further launches", omitted)))
	}
	if unidentified > 0 {
		lines.unidentified = true
		fmt.Fprintf(w, "%s%s with no returned agent identity recorded\n", turnLaunchIndent,
			quantity(unidentified, "1 launch", fmt.Sprintf("%d launches", unidentified)))
	}
	return lines
}

// turnLaunch describes one launch, inside the report's width.
//
// The identity itself is never printed. It is the agent's own opaque handle:
// it is what Axiom matched on and it names nothing a reader can use, so an
// ordinal within the turn says which launch this is without putting a second
// kind of identifier on the page.
//
// What the attributable calls were goes on a line of its own. A turn is
// indented deeply enough by the time it reaches a launch that a composition
// beside a count has room for two categories, and a composition cut down to
// two categories is one a reader cannot reconcile with the turn above it.
func turnLaunch(w io.Writer, ordinal int, done delegation.Work) {
	fmt.Fprintf(w, "%ssubagent launch %d", turnLaunchIndent, ordinal)
	// A launch nothing reported has no count to give: naming one would put a
	// zero on the page where the log has an absence.
	if done.Calls > 0 {
		fmt.Fprintf(w, "%s%s", launchSeparator, plural(done.Calls, "call"))
	}
	fmt.Fprintf(w, "\n%s%s\n", launchDetailIndent, describeLaunch(done))
}

// describeLaunch says what the calls reporting a launch's identity were.
//
// A launch nothing reported is stated as a fact about the log. Reading it as
// an agent that did no work would answer from evidence the log never held: a
// call rejected before it ran is never recorded, and a log can end before the
// work reaches it.
func describeLaunch(done delegation.Work) string {
	if done.Calls == 0 {
		return launchWithoutWork
	}
	// The composition is fitted to the line rather than truncated at a fixed
	// number of categories, so it is as complete as the width allows and
	// never wider than it.
	return fitParts(delegatedParts(done.Composition), reportWidth-len(launchDetailIndent))
}

// delegatedParts names the shapes of the attributable calls, in one fixed
// order, using the same vocabulary the work-by-path lines use.
//
// A category with none recorded is left out, as everywhere else in the report.
// Outcomes are carried where the category already carries them: a nested
// agent's write that reported failing is not a write that persisted.
func delegatedParts(c work.Composition) []string {
	var parts []string
	for _, p := range []struct {
		n     int
		one   string
		many  string
		outcs work.Outcomes
	}{
		{n: c.WholeReads, one: "whole-file read", many: "whole-file reads"},
		{n: c.RangedReads, one: "ranged read", many: "ranged reads"},
		{n: c.Searches, one: "search", many: "searches"},
		{n: c.Shell, one: "shell call", many: "shell calls"},
		{outcs: c.Writes, one: "write", many: "writes"},
		{outcs: c.Edits, one: "edit", many: "edits"},
		{outcs: c.Launches, one: "launch", many: "launches"},
		{n: c.Uninterpreted, one: "uninterpreted call", many: "uninterpreted calls"},
	} {
		switch {
		case p.outcs.Total() > 0:
			part := quantity(p.outcs.Total(), "1 "+p.one,
				fmt.Sprintf("%d %s", p.outcs.Total(), p.many))
			// A call whose outcome the record did not settle is held
			// apart from one it settled, exactly as the counts above the
			// launch hold them apart.
			if unsettled := p.outcs.Failed + p.outcs.Unestablished; unsettled > 0 {
				part += fmt.Sprintf(" (%d not established)", unsettled)
			}
			parts = append(parts, part)
		case p.n > 0:
			parts = append(parts, quantity(p.n, "1 "+p.one, fmt.Sprintf("%d %s", p.n, p.many)))
		}
	}
	return parts
}

// quantity picks between two written-out forms.
//
// plural adds an s, which is right for the nouns the rest of the report
// counts and wrong for the ones here: a launch and a search do not pluralize
// that way, and "1 launchs" beside a measurement undermines it.
func quantity(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// fitParts joins as many parts as the budget allows and counts the rest.
//
// Nothing is dropped silently: what does not fit is said to be there. A budget
// too small for even the first part yields the count alone, which still says
// that categories exist.
func fitParts(parts []string, budget int) string {
	if len(parts) == 0 {
		return ""
	}

	fitted, used := 0, 0
	for _, p := range parts {
		width := len(p)
		if fitted > 0 {
			width += len(", ")
		}
		// Every part after the first has to leave room for the count of
		// whatever follows it, so the line cannot be completed by a
		// remainder that does not fit.
		remaining := len(parts) - fitted - 1
		reserve := 0
		if remaining > 0 {
			reserve = len(fmt.Sprintf(", and %d more", remaining))
		}
		if used+width+reserve > budget {
			break
		}
		used += width
		fitted++
	}

	if fitted == len(parts) {
		return strings.Join(parts, ", ")
	}
	return strings.Join(append(parts[:fitted:fitted],
		fmt.Sprintf("and %d more", len(parts)-fitted)), ", ")
}

// writeTurnConsumption reports what was observed under the turn's identity.
//
// A turn with nothing recorded says so on one line rather than showing an empty
// block or no block at all. Consumption is half of what this section answers,
// so its absence has to be visible, and a block of zeros would answer the
// question wrongly.
func writeTurnConsumption(w io.Writer, c *correlate.TurnConsumption) {
	if c == nil {
		turnDetail(w, turnsHeading, "not recorded")
		return
	}

	fmt.Fprintf(w, "%s%s\n", turnIndent, turnsHeading)
	turnUsage(w, "Model requests", thousands(int64(c.Requests)))
	// A withheld dimension is left out rather than printed as zero: the agent
	// reporting nothing and the agent reporting none are different facts.
	if c.Tokens != nil {
		turnUsage(w, "Input tokens", thousands(c.Tokens.Input))
		turnUsage(w, "Output tokens", thousands(c.Tokens.Output))
		turnUsage(w, "Cache read", thousands(c.Tokens.CacheRead))
		turnUsage(w, "Cache creation", thousands(c.Tokens.CacheCreation))
	}
	// Named as it is named against a finding. It is one measurement reported
	// at two scopes, and two labels for it would read as two different things.
	if c.CostMicros != nil {
		turnUsage(w, "Model cost", dollars(*c.CostMicros))
	}
}

// writeTurnAccounting states what the section could not place, so that a
// reader adding up the turns above knows what is missing from them.
func writeTurnAccounting(w io.Writer, outside correlate.Outside, unattributed int, unrelated delegation.Unrelated) {
	if outside.Requests > 0 {
		sentence(w, fmt.Sprintf(turnsOutside,
			plural(outside.Requests, "model request"), plural(outside.Turns, "turn identifier")))
	}
	if unattributed > 0 {
		sentence(w, fmt.Sprintf(turnsUnattributed, plural(unattributed, "recorded tool call")))
	}
	// Nested work no launch accounts for is stated rather than dropped, and
	// never given to a launch nearby. Two launches of one type in one turn,
	// with their nested work interleaved, were captured: proximity would
	// have attributed half of it to the wrong agent.
	if unrelated.Calls > 0 {
		sentence(w, fmt.Sprintf(turnsUnrelated,
			quantity(unrelated.Calls, "1 recorded call by a nested agent",
				fmt.Sprintf("%d recorded calls by a nested agent", unrelated.Calls)),
			quantity(unrelated.Agents, "1 agent identity",
				fmt.Sprintf("%d agent identities", unrelated.Agents))))
	}
}

func epochLabel(n int) string {
	if n == 1 {
		return "Context epoch"
	}
	return "Context epochs"
}

// epochsOf names the context epochs a turn recorded work in.
//
// More than one is a turn whose work straddled a recorded context reset, and
// they are listed rather than reduced to one: forcing a turn into a single
// epoch would place work where it was not recorded. Past a few, the rest are
// counted, which keeps the line inside the report's width without losing the
// fact that the turn spanned them.
func epochsOf(ordinals []int) string {
	shown := ordinals
	if len(shown) > epochOrdinalsShown {
		shown = shown[:epochOrdinalsShown]
	}

	parts := make([]string, 0, len(shown))
	for _, o := range shown {
		parts = append(parts, strconv.Itoa(o))
	}

	list := strings.Join(parts, ", ")
	if omitted := len(ordinals) - len(shown); omitted > 0 {
		list += fmt.Sprintf(" and %d more", omitted)
	}
	return list
}

func turnDetail(w io.Writer, label, value string) {
	fmt.Fprintf(w, "%s%-*s%s\n", turnIndent, turnLabelWidth, label, value)
}

func turnUsage(w io.Writer, label, value string) {
	fmt.Fprintf(w, "%s%-*s%s\n", turnUsageIndent, turnUsageLabelWidth, label, value)
}

func turnCount(w io.Writer, label string, n int) {
	if n == 0 {
		return
	}
	turnDetail(w, label, strconv.Itoa(n))
}

func turnOutcomes(w io.Writer, label string, o work.Outcomes) {
	if o.Total() == 0 {
		return
	}
	turnDetail(w, label, outcomeValue(o.Total(), o.Failed, o.Unestablished))
}
