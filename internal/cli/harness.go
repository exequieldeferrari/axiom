package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/harness"
)

const (
	// How many observations a session shows. The most recently recorded are
	// kept, and everything omitted is accounted for on a line of its own.
	observationsShown = 4
	// A component's state is indented under the starts that observed it,
	// and a definition under the directory it was enumerated in.
	componentIndent  = "      "
	definitionIndent = "        "
	componentWidth   = 32
	definitionWidth  = componentWidth - 2

	harnessCaveat = "Harness provenance is what Axiom looked at for itself when a session start\nwas recorded, at the project root it resolved from that session's working\ndirectory. It is a record of those exact paths at that moment and nothing\nelse. Contents are never recorded: a digest is a SHA-256 of a file's exact\nbytes, taken with no normalization, and one byte of difference makes a\ndifferent digest. A path that is a link is read only where it leads to a file\ninside the project, and where a link led is never recorded.\n"
	harnessLimits = "It is not the configuration Claude Code loaded. Axiom does not observe user,\nenterprise or command-line configuration, a file reached through an import,\nplugins, skills, MCP servers, the model, or anything else the agent was\ngiven, and only the top level of the definitions directory is enumerated.\nComponents that match establish that these paths held the same bytes: not\nthat two sessions ran under the same harness, and nothing whatever about how\neither one behaved.\n"
	// Said of the log rather than of the agent. Provenance is recorded by
	// the hook that observes it, so a log written before Axiom observed any
	// holds none, and a report that filled the gap in from this machine
	// would be describing today.
	noHarness        = "  No harness provenance was recorded.\n"
	noHarnessMeaning = "Provenance is recorded when a session starts. Where none was recorded there\nis nothing to report, which is not a claim that the agent ran with no\nconfiguration, and Axiom does not go looking now: the files on this machine\ntoday are not evidence about a session recorded earlier.\n"
)

// writeHarness reports what was observed of the agent's project-local
// configuration when each session started.
//
// It follows the epochs because it describes the conditions the work below was
// recorded under, and it is placed before that work so that a reader meets the
// limits of the observation before meeting anything derived from it.
func writeHarness(w io.Writer, r harness.Report) {
	fmt.Fprint(w, "\nObserved harness provenance\n\n")
	if !r.Recorded() {
		fmt.Fprint(w, noHarness)
		fmt.Fprint(w, "\n"+noHarnessMeaning)
		return
	}

	for _, s := range r.Sessions {
		writeHarnessSession(w, s)
	}
	fmt.Fprint(w, "\n"+harnessCaveat)
	fmt.Fprint(w, "\n"+harnessLimits)
}

func writeHarnessSession(w io.Writer, s harness.Session) {
	fmt.Fprintf(w, "  session %s\n", s.ID)
	if len(s.Observations) == 0 {
		fmt.Fprintf(w, "    no harness provenance recorded at %s\n",
			plural(s.Starts, "recorded session start"))
		return
	}

	observations := s.Observations
	if len(observations) > observationsShown {
		omitted := len(observations) - observationsShown
		observations = observations[len(observations)-observationsShown:]
		fmt.Fprintf(w, "    %s omitted\n", plural(omitted, "earlier observation"))
	}
	for _, o := range observations {
		writeObservation(w, o)
	}

	// A start that recorded nothing is reported rather than dropped: its
	// silence is the reason the observations above do not cover the session.
	if s.StartsWithoutProvenance > 0 {
		fmt.Fprintf(w, "    %s recorded no harness provenance\n",
			plural(s.StartsWithoutProvenance, "session start"))
	}
}

func writeObservation(w io.Writer, o harness.Observation) {
	fmt.Fprintf(w, "    %s\n", observedAt(o.Starts))

	definitions := 0
	for _, c := range o.Components {
		if c.Kind == event.HarnessSubagentDefinition {
			definitions++
		}
	}
	for _, c := range o.Components {
		indent, width, label := componentIndent, componentWidth, c.Path
		if c.Kind == event.HarnessSubagentDefinition {
			// Shown under the directory it was enumerated in, by the
			// name that distinguishes it from its siblings, with its
			// label narrowed by the extra indent so that every state
			// stays in one column.
			indent, width = definitionIndent, definitionWidth
			label = definitionName(c.Path)
		}
		fmt.Fprintf(w, "%s%-*s%s\n", indent, width, label, componentState(c, definitions))
	}
}

// observedAt names the starts one observation covers. Several of them means
// the observations matched, which is a fact about what was looked at and never
// a claim that one harness spanned them.
func observedAt(starts []int) string {
	if len(starts) == 1 {
		return fmt.Sprintf("session start %d", starts[0])
	}
	labels := make([]string, 0, len(starts))
	for _, s := range starts {
		labels = append(labels, fmt.Sprintf("%d", s))
	}
	return fmt.Sprintf("session starts %s, same observed components", strings.Join(labels, ", "))
}

// componentState says what was established about one path.
//
// Every state is stated positively, in terms of the observation. None of them
// is a description of the project: a path Axiom found nothing at is reported
// as exactly that, and never as configuration the agent did without.
func componentState(c event.HarnessComponent, definitions int) string {
	switch c.Status {
	case event.HarnessObserved:
		// A directory carries no digest. What it establishes is that
		// enumeration happened and what it turned up, so an empty one says
		// so rather than appearing beside a blank.
		if c.Kind == event.HarnessSubagentDirectory {
			if definitions == 0 {
				return "enumerated, no definition found"
			}
			return fmt.Sprintf("enumerated, %s", plural(definitions, "definition"))
		}
		return "observed  " + shortDigest(c.Digest)
	case event.HarnessAbsent:
		return "nothing found there"
	case event.HarnessNotFollowed:
		// Said of the link and not of its target, which is the whole
		// point: Axiom does not know where it went, having declined to
		// go there.
		return "link not followed"
	default:
		return "not established"
	}
}

// definitionName is the definition's own name within the directory above it.
func definitionName(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}
