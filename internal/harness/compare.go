package harness

import "github.com/exequieldeferrari/axiom/internal/event"

// Verdict is what two recorded observations established about one component.
//
// Every value describes the two observations and nothing else. Two
// observations that match establish that those paths held the same bytes at
// the two moments Axiom looked. They do not establish that either agent loaded
// them, that the two captures ran under one harness, or anything whatever
// about how either one behaved.
//
// The values that name a side name the side of the comparison and never a
// point in time. Nothing establishes an order between two captures: the
// operator chose which one is the baseline.
type Verdict string

const (
	// VerdictSame is a component observed on both sides with the same
	// digest: the same bytes at the two recorded moments.
	VerdictSame Verdict = "same"

	// VerdictDiffered is a component observed on both sides with different
	// digests. A digest has no magnitude, so this establishes that the
	// bytes differed and never by how much.
	VerdictDiffered Verdict = "differed"

	// VerdictAppeared is a component the baseline established was not there
	// and the candidate observed.
	VerdictAppeared Verdict = "appeared"

	// VerdictDisappeared is the same the other way round: observed in the
	// baseline, established absent in the candidate.
	VerdictDisappeared Verdict = "disappeared"

	// VerdictAbsent is a component both observations established was not
	// there. It is a fact about one path at two moments, and never a claim
	// that either agent had no such configuration anywhere.
	VerdictAbsent Verdict = "absent"

	// VerdictEnumerated is a component observed on both sides that carries
	// no digest to compare.
	//
	// The only component the collector records without one is the
	// definitions directory, whose observation is the enumeration itself.
	// What this establishes is that both observations reached it, and
	// nothing about what either found: what each found is established by
	// the definitions, which are components of their own.
	VerdictEnumerated Verdict = "enumerated"

	// VerdictNotEstablished is every case the evidence cannot support a
	// comparison of: a component either side recorded as unreadable or as a
	// link it did not read through, and a component one observation holds
	// and the other does not.
	//
	// It is deliberately not a change. What stopped the comparison was the
	// observer, and reporting it as a difference would turn a limit of the
	// observation into a fact about a project.
	VerdictNotEstablished Verdict = "not_established"
)

// Change is what two observations established about one component.
//
// The component is named exactly as it was recorded — the kind the collector
// wrote and the path it looked at, never a path a link resolved to.
type Change struct {
	Kind    event.HarnessKind
	Path    string
	Verdict Verdict
}

// Session returns the provenance recorded under one session identity.
//
// The second result reports whether the log recorded a session start for that
// identity at all. A capture whose records begin after its session did has no
// start to have observed anything at, which is a different fact from a start
// that recorded no provenance.
func (r Report) Session(id string) (Session, bool) {
	for _, s := range r.Sessions {
		if s.ID == id {
			return s, true
		}
	}
	return Session{}, false
}

// Comparable returns the one observation a session's provenance can be
// compared by, and whether there is one.
//
// A session that recorded several distinct observations has none. ADR 0018
// keeps those observations apart because the files can change between two
// starts of one session, and choosing one of them here would present the
// conditions part of a capture was recorded under as the conditions all of it
// was. Consecutive starts that observed identical components are already one
// observation, so a session that started many times under unchanging files has
// one and compares like any other.
func (s Session) Comparable() (Observation, bool) {
	if len(s.Observations) != 1 {
		return Observation{}, false
	}
	return s.Observations[0], true
}

// componentKey identifies one observed path within an observation.
type componentKey struct {
	kind event.HarnessKind
	path string
}

// Compare reports what two observations established about each component.
//
// Nothing is summarized. There is no composite value over the components and
// no count of how many differed: the components are not commensurable, and a
// single number over them would invite exactly the reading ADR 0018 refused.
//
// Components are returned in the order the baseline recorded them, followed by
// any the candidate recorded and the baseline did not. That is the order the
// collectors wrote, rather than one invented here; the two agree wherever both
// sides were recorded by an Axiom that looked at the same paths.
func Compare(baseline, candidate Observation) []Change {
	b, c := index(baseline), index(candidate)
	// Whether a definition can be said to have appeared or disappeared is
	// decided once, for the whole set, by what each side established about
	// the directory holding it.
	set := establishesDefinitions(baseline) && establishesDefinitions(candidate)

	out := make([]Change, 0, len(b)+len(c))
	seen := make(map[componentKey]struct{}, len(b)+len(c))
	for _, o := range []Observation{baseline, candidate} {
		for _, comp := range o.Components {
			k := componentKey{comp.Kind, comp.Path}
			if _, ok := seen[k]; ok {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, Change{
				Kind:    k.kind,
				Path:    k.path,
				Verdict: verdict(k.kind, b[k], c[k], set),
			})
		}
	}
	return out
}

// index holds an observation's components by the path they were observed at.
//
// The first record of a path wins, so that the component compared is the one
// the merged ordering named.
func index(o Observation) map[componentKey]*event.HarnessComponent {
	m := make(map[componentKey]*event.HarnessComponent, len(o.Components))
	for i := range o.Components {
		k := componentKey{o.Components[i].Kind, o.Components[i].Path}
		if _, ok := m[k]; !ok {
			m[k] = &o.Components[i]
		}
	}
	return m
}

// establishesDefinitions reports whether an observation established which
// definitions the project held.
//
// Enumeration establishes the set, and so does finding nothing at the
// directory at all: a directory that was not there held no definitions. A
// directory Axiom could not read, one it did not follow, and an observation
// that does not name the directory establish no set, and then a definition
// missing from one side is a definition Axiom did not look for rather than one
// that was not there.
func establishesDefinitions(o Observation) bool {
	for _, c := range o.Components {
		if c.Kind != event.HarnessSubagentDirectory {
			continue
		}
		return c.Status == event.HarnessObserved || c.Status == event.HarnessAbsent
	}
	return false
}

// verdict says what the two sides established about one component.
func verdict(kind event.HarnessKind, b, c *event.HarnessComponent, set bool) Verdict {
	if b != nil && c != nil {
		return compareStates(*b, *c)
	}

	// A component only one observation holds. For the fixed list of paths
	// this is two versions of Axiom having looked at different things,
	// which establishes nothing about either project: a component that is
	// absent from a record was never looked at, which ADR 0018 holds apart
	// from a component that was looked at and found missing.
	//
	// A definition is the one exception, and only where both sides
	// established the set: there, the side that does not name it did look,
	// and found it was not there.
	if !set || kind != event.HarnessSubagentDefinition {
		return VerdictNotEstablished
	}
	switch {
	case c != nil && c.Status == event.HarnessObserved:
		return VerdictAppeared
	case b != nil && b.Status == event.HarnessObserved:
		return VerdictDisappeared
	default:
		// A definition the enumeration named and Axiom did not read. The
		// observer stopped, so nothing about the project is established.
		return VerdictNotEstablished
	}
}

// compareStates says what two recorded states of one path established.
//
// Only observed and absent establish anything to compare. Unreadable and
// not-followed are the observer's limits, and either of them on either side
// leaves the comparison unestablished rather than making it a change.
func compareStates(b, c event.HarnessComponent) Verdict {
	switch {
	case b.Status == event.HarnessObserved && c.Status == event.HarnessObserved:
		return compareObserved(b, c)
	case b.Status == event.HarnessAbsent && c.Status == event.HarnessAbsent:
		return VerdictAbsent
	case b.Status == event.HarnessAbsent && c.Status == event.HarnessObserved:
		return VerdictAppeared
	case b.Status == event.HarnessObserved && c.Status == event.HarnessAbsent:
		return VerdictDisappeared
	default:
		return VerdictNotEstablished
	}
}

// compareObserved compares two components Axiom read.
//
// The comparison is of the digests and of nothing else. A component observed
// with no digest carries no bytes to compare, which is what the definitions
// directory is: its observation is the enumeration. One side carrying a digest
// where the other does not is a pairing no collector produces, and it is
// refused rather than guessed at.
func compareObserved(b, c event.HarnessComponent) Verdict {
	switch {
	case b.Digest == "" && c.Digest == "":
		return VerdictEnumerated
	case b.Digest == "" || c.Digest == "":
		return VerdictNotEstablished
	case b.Digest == c.Digest:
		return VerdictSame
	default:
		return VerdictDiffered
	}
}
