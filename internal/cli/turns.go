package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/exequieldeferrari/axiom/internal/correlate"
	"github.com/exequieldeferrari/axiom/internal/turns"
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

	turnsHeading = "Observed model consumption"

	turnsCaveat = "A recorded turn is a turn identifier at least one recorded tool call named.\nA turn is the context an agent labels with an identifier of its own: it is\nnot established to be one request, one task, or one complete unit of work.\nIdentifiers carried only by a session start or end, or only by a usage\nrecord, named no recorded work and are not listed, because a turn built from\none would be a turn that did nothing. Turns are numbered within a session\nidentity, in the order their work was recorded, and an identifier means\nnothing outside the session that issued it.\n"
	turnsWindow = "The time beside a turn is the earliest and latest recorded on its calls. It\nis not how long the turn took, and nothing above is ordered by it:\nmembership and order come from the order records were appended.\n"
	turnsSpend  = "Observed model consumption is what the agent reported for requests it\nlabelled with that turn. It is not the cost of the tool calls above it, not\nthe cost of the turn, and not a billing figure: nothing recorded says which\nrequest served which call. A turn with none recorded is not a turn that\nconsumed nothing — telemetry exists only while a receiver is running.\n"
	turnsNested = "A nested agent's tool calls were observed carrying the turn that launched\nthem, and its model requests were observed carrying identifiers of their\nown. A turn's observed consumption therefore does not contain everything a\nsubagent it launched spent.\n"

	noRecordedTurns = "  No recorded tool call named a turn, so no turn recorded work.\n"

	// These carry counts from the log, so their length is not known when they
	// are written and they are wrapped rather than broken by hand.
	turnsOutside      = "%s observed under %s that no recorded tool call named. They belong to no turn above and are not attributed to one."
	turnsUnattributed = "%s named no turn, so they appear in no turn above."
)

// writeTurns reports the turns that recorded work, and what belonged to each.
//
// It is printed after the epochs because it says how the work inside them was
// divided, and before the profile by path because a turn is the unit the
// consumption stream is joined on: the paths below are behavior alone.
func writeTurns(w io.Writer, ms []correlate.MeasuredTurn, outside correlate.Outside, unattributed int) {
	fmt.Fprint(w, "\nRecorded turns\n\n")
	if len(ms) == 0 {
		fmt.Fprint(w, noRecordedTurns)
		writeTurnAccounting(w, outside, unattributed)
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
	for _, m := range shown {
		if m.SessionID != session {
			session = m.SessionID
			fmt.Fprintf(w, "  session %s\n", session)
		}
		writeTurn(w, m)
		consumption = consumption || m.Observed != nil
	}

	fmt.Fprint(w, turnsCaveat)
	fmt.Fprint(w, "\n"+turnsWindow)
	if consumption {
		fmt.Fprint(w, "\n"+turnsSpend)
		fmt.Fprint(w, "\n"+turnsNested)
	}
	writeTurnAccounting(w, outside, unattributed)
}

func writeTurn(w io.Writer, m correlate.MeasuredTurn) {
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
	turnCount(w, "Subagent calls", m.SubagentCalls)
	turnCount(w, "Uninterpreted", c.Uninterpreted)

	writeTurnConsumption(w, m.Observed)
	fmt.Fprintln(w)
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
func writeTurnAccounting(w io.Writer, outside correlate.Outside, unattributed int) {
	if outside.Requests > 0 {
		sentence(w, fmt.Sprintf(turnsOutside,
			plural(outside.Requests, "model request"), plural(outside.Turns, "turn identifier")))
	}
	if unattributed > 0 {
		sentence(w, fmt.Sprintf(turnsUnattributed, plural(unattributed, "recorded tool call")))
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

func turnOutcomes(w io.Writer, label string, o turns.Outcomes) {
	if o.Total() == 0 {
		return
	}
	turnDetail(w, label, outcomeValue(o.Total(), o.Failed, o.Unestablished))
}
