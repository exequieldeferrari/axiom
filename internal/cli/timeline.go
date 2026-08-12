package cli

import (
	"fmt"
	"io"
	"time"

	"github.com/exequieldeferrari/axiom/internal/timeline"
)

const (
	// How many sessions and epochs a report shows. The most recently recorded
	// are kept, and everything omitted is accounted for on a line of its own.
	sessionsShown = 5
	epochsShown   = 6
	// An epoch's counts are indented under the line that names it.
	epochIndent = "       "

	epochCaveat = "A context epoch is the work recorded for one session identity between the\npoints where the agent reported starting a context. Compaction was observed\nstarting one under the same identity; /clear was observed ending a session\nand starting the next under a different one. Axiom never links two\nidentities: where one appears after another here, that is the order they\nwere recorded in and nothing more.\n"
	epochTurns  = "A turn can span a context reset, so turns with work are counted inside each\nepoch and do not add up to a session total.\n"
	epochOpen   = "An epoch with nothing recorded after it is the last thing this log holds\nfor that session. It is not a claim that the agent is still running.\n"
	// Stated of the epochs rather than of the sessions: a record Axiom could
	// not place still names a session identity, which the counts above report,
	// and this section would otherwise appear to deny it.
	noEpochs = "  No context epoch was recorded.\n"
)

// writeTimeline reports the structure the log recorded: the session identities
// in it, and the context epochs within each.
//
// It is printed first because it says what the analysis below was cut into. The
// profiler compares repetition only within one of these epochs, so a reader who
// cannot see the boundaries cannot tell a quiet report from a report whose
// evidence was divided by resets.
func writeTimeline(w io.Writer, r timeline.Report) {
	fmt.Fprint(w, "\nContext epochs\n\n")
	if len(r.Sessions) == 0 {
		fmt.Fprint(w, noEpochs)
		writeUnidentified(w, r)
		return
	}

	sessions := r.Sessions
	if len(sessions) > sessionsShown {
		omitted := sessions[:len(sessions)-sessionsShown]
		sessions = sessions[len(sessions)-sessionsShown:]
		epochs, calls := 0, 0
		for _, s := range omitted {
			epochs += len(s.Epochs)
			calls += s.ToolCalls()
		}
		fmt.Fprintf(w, "  %s omitted (%s, %s)\n\n", plural(len(omitted), "earlier session"),
			plural(epochs, "epoch"), plural(calls, "tool call"))
	}

	open := false
	for _, s := range sessions {
		writeSession(w, s)
		for _, e := range s.Epochs {
			open = open || e.Closing.Kind == timeline.ClosingOpen
		}
	}

	fmt.Fprint(w, "\n"+epochCaveat)
	fmt.Fprint(w, "\n"+epochTurns)
	if open {
		fmt.Fprint(w, "\n"+epochOpen)
	}
	writeUnidentified(w, r)
}

// writeSession names one session identity and lists its epochs.
//
// The identifier is printed in full, unshortened: it is what 'axiom profile
// --session' takes, and a prefix is not an identity.
func writeSession(w io.Writer, s timeline.Session) {
	fmt.Fprintf(w, "  session %s", s.ID)
	if len(s.Epochs) == 0 {
		fmt.Fprint(w, ", no context epoch recorded\n")
	} else {
		fmt.Fprintf(w, "  ·  %s\n", plural(len(s.Epochs), "epoch"))
	}

	// One time is printed per session, and it is the earliest one recorded for
	// it rather than a span. Times come from whichever hook process got there
	// first, so a range built from them could read backwards, and the order of
	// the epochs below is the order they were recorded in, not their times.
	if len(s.Epochs) > 0 && !s.Epochs[0].First.IsZero() {
		fmt.Fprintf(w, "    first recorded %s\n", recorded(s.Epochs[0].First))
	}

	epochs := s.Epochs
	if len(epochs) > epochsShown {
		omitted := epochs[:len(epochs)-epochsShown]
		epochs = epochs[len(epochs)-epochsShown:]
		calls := 0
		for _, e := range omitted {
			calls += e.ToolCalls
		}
		fmt.Fprintf(w, "    %s omitted (%s)\n", plural(len(omitted), "earlier epoch"),
			plural(calls, "tool call"))
	}
	for _, e := range epochs {
		fmt.Fprintf(w, "    %d  %s, %s\n%s%s\n", e.Ordinal, opened(e.Opening), ended(e.Closing),
			epochIndent, observed(e))
	}

	// An end that closed nothing is the only trace of the context it belonged
	// to, so it is reported rather than dropped.
	if s.EndsWithoutEpoch > 0 {
		fmt.Fprintf(w, "    %s recorded with no context open\n",
			plural(s.EndsWithoutEpoch, "session end"))
	}
}

// writeUnidentified accounts for records that named no session.
func writeUnidentified(w io.Writer, r timeline.Report) {
	if r.Unidentified == 0 {
		return
	}
	sentence(w, fmt.Sprintf("%s carried no session identity. Without one there is no context to place them in, so they appear in no epoch above.",
		plural(r.Unidentified, "record")))
}

// opened says how an epoch began, in the agent's own words where it gave any.
func opened(o timeline.Opening) string {
	switch o.Kind {
	case timeline.OpeningRecorded:
		return "opened by " + o.Source
	case timeline.OpeningUnspecified:
		return "opened by a start with no source"
	default:
		// Work arrived for a session Axiom had seen no start for: a log that
		// begins after the session did, or a record that arrived after the
		// session was reported ending.
		return "no start recorded"
	}
}

// ended says how an epoch ended, without saying anything about what is running.
func ended(c timeline.Closing) string {
	switch c.Kind {
	case timeline.ClosingReset:
		return "ended by a context reset"
	case timeline.ClosingEnded:
		if c.Reason == "" {
			return "ended with the session, reason not recorded"
		}
		return fmt.Sprintf("ended with the session (%s)", c.Reason)
	default:
		return "nothing recorded after it"
	}
}

// observed states what was recorded inside an epoch.
//
// A turn count with no turn established is shown as unrecorded rather than as
// zero: an agent that reported no turn identifier did not report work outside a
// turn, it reported nothing about which turn the work belonged to.
func observed(e timeline.Epoch) string {
	if e.ToolCalls == 0 {
		return "no tool call recorded"
	}

	line := plural(e.ToolCalls, "tool call")
	if e.Turns == 0 {
		line += ", turns not recorded"
	} else {
		line += fmt.Sprintf(", %s with work", plural(e.Turns, "turn"))
	}
	if e.SubagentCalls > 0 {
		line += fmt.Sprintf(", %s by a subagent", plural(e.SubagentCalls, "call"))
	}
	return line
}

// recorded renders one recorded time. Times are shown in UTC, matching how they
// are recorded, so a report means the same thing wherever it is read.
func recorded(t time.Time) string {
	return t.UTC().Format(time.DateTime) + " UTC"
}
