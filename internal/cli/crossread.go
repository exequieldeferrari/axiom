package cli

import (
	"fmt"
	"io"

	"github.com/exequieldeferrari/axiom/internal/crossread"
)

const (
	// How many paths the section shows, how many groups it describes under
	// one path, and how many scopes it names inside one group. Everything
	// omitted is accounted for on a line of its own, so no limit hides a
	// relation.
	crossreadShown       = 8
	crossreadGroupsShown = 3
	crossreadScopesShown = 6

	// A group is indented under the path it belongs to, and the scopes that
	// read the path one step further in.
	groupIndent = "      "
	scopeIndent = groupIndent + "  "

	// The four empty states are kept apart because they mean different
	// things. Nothing delegated, delegation with no identity to relate on,
	// related scopes that read nothing, and related scopes that read nothing
	// in common are four separate observations, and a single "none" would
	// read as the last one in all four cases.
	crossreadNoLaunch = "  No recorded call handed work to a nested agent, so no scope here is\n  related to another.\n"
	crossreadNoScope  = "%s recorded, and none of them carried a returned agent identity, so no delegated scope was established to compare. Every launch recorded before Axiom persisted that identity says this, and so does one that reported failing."
	crossreadNoReads  = "  No scope taking part in a delegation relation recorded a successful\n  whole-file read.\n"
	crossreadNoShared = "  No path was read in more than one related agent scope.\n"

	crossreadCaveat = "A path is listed where a successful whole-file read of it was recorded in\nmore than one agent scope, and a recorded launch relates those scopes. That\nis what it says. It does not say that either agent held what the other read,\nthat one reading stood in for the other, or that anything about how the work\nwas handed over follows from it.\n"
	// The relation is the whole basis of the section, so what may establish
	// one and what may not is stated before anything else about it.
	crossreadRelation = "Scopes are related only through a launch whose record carried the agent\nidentity it returned. A launch with no identity recorded relates nothing,\nand a nested agent whose identity no recorded launch returned takes part in\nno group. Timing, proximity, turn identifiers and tool names take no part in\nany of it.\n"
	crossreadGrouping = "A group is one launching scope together with the scopes it launched\ndirectly. The relation is followed no further: a group holding every agent\nof a session would say nothing about why two scopes appear side by side.\nTwo agents launched by one scope are compared; an agent and the scope that\nlaunched its launcher are not.\n"
	// Ordering is refused rather than left unstated: a reader who assumes one
	// would read a sequence into two reads the log does not order.
	crossreadOrder    = "Nothing here is an ordering. A nested agent's work reaches the log before\nthe launch that names it as often as after, so the section says a path was\nread in more than one related scope and never which read came first.\n"
	crossreadIdentity = "Paths are compared as the exact strings the agent recorded. Two spellings of\none file stay apart, which can lose a relation and never invent one.\n"
	crossreadScopes   = "The session scope is the work recorded under no agent identity. A numbered\nagent is one nested identity, numbered within its session in the order the\nlog first mentions it: the numbering is Axiom's own, and it names no agent\noutside this report.\n"

	// Reading in a scope no launch relates to another is set aside rather
	// than dropped, so that work Axiom observed does not disappear.
	crossreadSetAsideOne  = "1 successful whole-file read recorded in a scope that no launch relates to another was set aside."
	crossreadSetAsideMany = "%d successful whole-file reads recorded in scopes that no launch relates to another were set aside."
)

// writeCrossRead reports the paths read in more than one related agent scope.
//
// It is printed under the recorded turns, where the launches it relates are
// described, and above the context epochs: a reader who cannot see the
// launches cannot audit a line here, and the epochs below answer a different
// question about the session's own reading.
func writeCrossRead(w io.Writer, r crossread.Report) {
	fmt.Fprint(w, "\nRead across related agent scopes\n\n")

	switch {
	case r.Launches == 0:
		// Nothing is set aside here. The line above already says that no
		// scope in the log was related to another, and counting the reading
		// it did not look at would say the same thing a second time.
		fmt.Fprint(w, crossreadNoLaunch)
		return
	case r.Relations == 0:
		// Carries a count from the log, so its line breaks are not known
		// when it is written and it is wrapped rather than broken by hand.
		indented(w, fmt.Sprintf(crossreadNoScope, launchCount(r.Launches)))
		writeCrossReadSetAside(w, r)
		return
	case r.RelatedReads == 0:
		fmt.Fprint(w, crossreadNoReads)
		writeCrossReadSetAside(w, r)
		return
	case len(r.Paths) == 0:
		fmt.Fprint(w, crossreadNoShared)
		fmt.Fprint(w, "\n"+crossreadGrouping)
		writeCrossReadSetAside(w, r)
		return
	}

	shown := r.Paths
	if len(shown) > crossreadShown {
		shown = shown[:crossreadShown]
	}
	for _, p := range shown {
		writeCrossReadPath(w, p)
	}
	if omitted := len(r.Paths) - len(shown); omitted > 0 {
		fmt.Fprintf(w, "  and %s read in more than one related scope\n\n",
			plural(omitted, "more path"))
	}

	// The count is of paths and not of groups: a path in two groups is one
	// path here, and adding the groups up would count one reading twice.
	fmt.Fprintf(w, "%s read in more than one related agent scope.\n",
		plural(len(r.Paths), "path"))
	fmt.Fprint(w, "\n"+crossreadCaveat)
	fmt.Fprint(w, "\n"+crossreadRelation)
	fmt.Fprint(w, "\n"+crossreadGrouping)
	fmt.Fprint(w, "\n"+crossreadOrder)
	fmt.Fprint(w, "\n"+crossreadIdentity)
	fmt.Fprint(w, "\n"+crossreadScopes)
	writeCrossReadSetAside(w, r)
}

// writeCrossReadPath names one path and the groups whose scopes read it.
//
// The session identity is printed in full: it is what 'axiom profile --session'
// takes, and the agent numbers below are assigned within it, so a reader needs
// both to find the reads a line came from.
func writeCrossReadPath(w io.Writer, p crossread.Path) {
	fmt.Fprintf(w, "  %s\n", p.Path)
	fmt.Fprintf(w, "%ssession %s\n", groupIndent, p.SessionID)

	shown := p.Groups
	if len(shown) > crossreadGroupsShown {
		shown = shown[:crossreadGroupsShown]
	}
	for _, g := range shown {
		writeCrossReadGroup(w, g)
	}
	if omitted := len(p.Groups) - len(shown); omitted > 0 {
		fmt.Fprintf(w, "%sand %s read it\n", groupIndent,
			quantity(omitted, "1 more group of related scopes",
				fmt.Sprintf("%d more groups of related scopes", omitted)))
	}
	fmt.Fprintln(w)
}

// writeCrossReadGroup names one group and the scopes in it that read the path.
//
// The launching scope names the group whether or not it read the path itself,
// so a fan-out where only the launched agents read it still says which scope
// put them in one group.
func writeCrossReadGroup(w io.Writer, g crossread.Group) {
	fmt.Fprintf(w, "%s%s and the agents it launched\n", groupIndent, scopeName(g.Launcher))

	shown := g.Scopes
	if len(shown) > crossreadScopesShown {
		shown = shown[:crossreadScopesShown]
	}
	for _, s := range shown {
		fmt.Fprintf(w, "%s%s, %s\n", scopeIndent, scopeName(s.Ref), plural(s.Reads, "read"))
	}
	if omitted := len(g.Scopes) - len(shown); omitted > 0 {
		fmt.Fprintf(w, "%sand %s in this group\n", scopeIndent,
			quantity(omitted, "1 more scope", fmt.Sprintf("%d more scopes", omitted)))
	}
}

// scopeName says which scope a line is about, in terms a reader can hold.
//
// The agent's own identity is never printed. It is an opaque handle that names
// nothing outside the log, and the number beside "agent" is what this report
// assigned it, in the order the log first mentions it.
func scopeName(ref crossread.ScopeRef) string {
	if ref.Root {
		return "the session scope"
	}
	return fmt.Sprintf("agent %d", ref.Ordinal)
}

// indented prints an empty state whose length depends on the log, at the depth
// the section's fixed empty states are written to.
func indented(w io.Writer, text string) {
	for _, line := range wrap(text, reportWidth-2) {
		fmt.Fprintf(w, "  %s\n", line)
	}
}

// launchCount counts launches in a noun that does not take a plural s where
// plural would put one.
func launchCount(n int) string {
	return quantity(n, "1 launch", fmt.Sprintf("%d launches", n))
}

// writeCrossReadSetAside accounts for the reading the analysis had no relation
// to look at, so that it does not read as reading that found nothing.
func writeCrossReadSetAside(w io.Writer, r crossread.Report) {
	if r.UnrelatedReads == 0 {
		return
	}
	sentence(w, quantity(r.UnrelatedReads, crossreadSetAsideOne,
		fmt.Sprintf(crossreadSetAsideMany, r.UnrelatedReads)))
}
