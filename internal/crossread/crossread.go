// Package crossread reports the paths read whole in more than one agent scope
// where a recorded launch relates those scopes to each other.
//
// It establishes one thing, and the wording is the whole of it:
//
//	This exact path was read successfully, whole, in more than one of the
//	agent scopes a recorded launch put in one group.
//
// It is not a claim that the reading repeated anything, that either agent held
// what the other read, that one read made the other pointless, that context
// could have been handed over instead, or that delegating this way cost
// anything. None of that is recorded. Two agents reasoning separately over one
// file is the ordinary shape of delegation, and this package measures how often
// it happened rather than judging it.
//
// # Scopes
//
// A scope is where a call was recorded, and there are two kinds. The session
// scope is the work carrying no agent identity. A nested scope is one agent
// identity, exactly as the agent reported it. Nothing else divides a scope: a
// context epoch is a boundary in the session's own reasoning and answers a
// different question, which internal/reacquire already asks.
//
// # Relation
//
// Which scope handed work to which is not decided here. internal/delegation
// owns that: it reads the launch records, applies the rules about a returned
// identity, and reports the pairs it established. This package is given those
// pairs and does one thing with them — it groups a launching scope with the
// scopes it launched directly, and reports a path more than one member of one
// group read.
//
// The grouping stops at one step deliberately. Following the relation
// transitively would put every agent of a session in one group, since almost
// all of them descend from the session scope, and a group that holds everything
// explains nothing about why two scopes were compared. Two agents launched by
// one scope are compared; an agent and its launcher's launcher are not.
//
// # Order
//
// No timestamp, append position, turn, tool name or subagent type takes part in
// any of this. Acquisitions are counted as the records arrive and are grouped
// only when the report is taken, against relations resolved the same way, so
// nested work observed before the launch that names it needs no special case.
// Nothing here says which read came first, and the observation does not need
// one to: what it reports is that one path was read in more than one related
// scope.
package crossread

import (
	"cmp"
	"slices"

	"github.com/exequieldeferrari/axiom/internal/delegation"
	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/work"
)

// ScopeRef names one agent scope within one session.
//
// The agent's own identity is deliberately not carried out of this package. It
// is an opaque handle that names nothing a reader can act on, and an ordinal
// assigned in the order the log first records a scope working says which scope
// this is without putting a second kind of identifier on the page. The
// numbering is Axiom's own and means nothing outside one report.
type ScopeRef struct {
	// Root marks the session scope: the work recorded under no agent
	// identity. Ordinal is zero for it, and is otherwise the nested scope's
	// number within the session.
	Root    bool
	Ordinal int
}

// Scope is one scope that read a path, with how many times it was recorded
// doing so.
type Scope struct {
	Ref ScopeRef

	// Reads counts the qualifying reads this scope recorded of the path. It
	// is at least one. More than one is repetition inside a single scope,
	// which is a different subject and is not what puts the path here.
	Reads int
}

// Group is one launching scope and the scopes it launched directly, reduced to
// the members that read one path.
type Group struct {
	// Launcher is the scope whose recorded launches established the group.
	// It appears in Scopes only where it read the path itself.
	Launcher ScopeRef

	// Scopes are the members that read the path, ordered with the session
	// scope first and nested scopes by their number. There are always at
	// least two.
	Scopes []Scope
}

// Path is one exact recorded path read in more than one related scope.
//
// Identity is the exact string the agent named, compared byte for byte, as
// everywhere else in Axiom. No normalization happens: two strings naming one
// file stay apart, which can only lose a relation and never invent one.
type Path struct {
	SessionID string
	Path      string

	// Groups are the groups whose members read this path, each with the
	// members that did. A path read by a scope, by the scope that launched
	// it, and by an agent that scope launched appears in two groups: those
	// are two separate relations over the same reading, and neither is a
	// count of the other.
	Groups []Group
}

// Report is one pass of cross-scope reading.
type Report struct {
	// Paths holds every path read in more than one related scope. It is
	// complete: showing only part of it is a decision for a report, not for
	// the analysis.
	Paths []Path

	// Launches counts the recorded calls that handed work to a nested agent,
	// whatever became of them, and Relations the delegations they
	// established. Groups counts the launching scopes behind those
	// relations. All three are carried from internal/delegation.
	//
	// They are the denominators this analysis needs. No launch at all is a
	// different fact from launches that named no scope Axiom can relate,
	// which is different again from relations with nothing read across one.
	Launches  int
	Relations int
	Groups    int

	// RelatedReads counts the qualifying reads recorded in scopes that take
	// part in at least one relation, and UnrelatedReads those recorded in
	// scopes that take part in none.
	//
	// The second is the reading this analysis had no relation to look at: a
	// nested agent whose identity no recorded launch returned, and a session
	// scope that delegated nothing, both produce it. It is counted rather
	// than dropped so that work Axiom observed does not disappear from the
	// report.
	RelatedReads   int
	UnrelatedReads int
}

// Accumulator collects the acquisitions a report is built from.
//
// It holds no delegation state. What it collects is per session, scope and
// path, and it is joined to the delegation relations only when the report is
// taken.
type Accumulator struct {
	sessions map[string]*sessionState
}

// New returns an accumulator with no observations.
func New() *Accumulator {
	return &Accumulator{sessions: make(map[string]*sessionState)}
}

// sessionState is the reading one session identity recorded.
//
// Sessions are never compared. An agent identity is the agent's own and is not
// known to mean anything outside the session that issued it, which is the rule
// the rest of Axiom already follows.
type sessionState struct {
	// ordinals numbers each nested scope in the order the log first records
	// it making a call.
	ordinals map[string]int

	// reads counts each scope's qualifying reads by path. The empty key is
	// the session scope.
	reads map[string]map[string]int
}

// Add records one event.
//
// A call that named no session is dropped: an agent identity is scoped to a
// session the record does not name, so there is nothing it could be related
// within.
func (a *Accumulator) Add(ev event.Event) {
	if ev.Type != event.TypeToolCall || ev.Tool == nil || ev.SessionID == "" {
		return
	}

	s := a.session(ev.SessionID)
	if ev.SubagentID != "" {
		s.note(ev.SubagentID)
	}

	// A read the agent reported failing is not established to have delivered
	// the file's contents, and neither is one whose outcome was never
	// established: the record says what became of a call and never what it
	// returned. A ranged read returns part of a file, so it obtains something
	// else. All three are left out, exactly as the cross-epoch analysis
	// leaves them out, and for the same reason.
	if work.Of(ev.Tool) == work.WholeRead && ev.Tool.Outcome == event.OutcomeSuccess {
		s.acquire(ev.SubagentID, ev.Tool.Metadata.File.Path)
	}
}

func (a *Accumulator) session(id string) *sessionState {
	s, ok := a.sessions[id]
	if !ok {
		s = &sessionState{
			ordinals: make(map[string]int),
			reads:    make(map[string]map[string]int),
		}
		a.sessions[id] = s
	}
	return s
}

// note assigns a nested scope its number the first time the log records it
// working.
func (s *sessionState) note(agent string) {
	if _, ok := s.ordinals[agent]; !ok {
		s.ordinals[agent] = len(s.ordinals) + 1
	}
}

// acquire counts one qualifying read against the scope that made it.
func (s *sessionState) acquire(scope, path string) {
	paths, ok := s.reads[scope]
	if !ok {
		paths = make(map[string]int)
		s.reads[scope] = paths
	}
	paths[path]++
}

// Report groups the acquisitions observed so far against the delegation the
// record established.
//
// The relations come from internal/delegation and are used exactly as given:
// nothing here decides whether a launch established one, so a launch with no
// returned identity, an identity a scope returned for itself, and an identity
// returned twice are settled in one place rather than two. Reading alone can
// never produce a group.
//
// It does not consume the accumulator: adding more events and reporting again
// is valid.
func (a *Accumulator) Report(d delegation.Report) Report {
	out := Report{
		Launches:  len(d.Launches),
		Relations: len(d.Relations),
	}

	grouped := group(d.Relations)
	// Counted from the relations rather than from the sessions that read,
	// so that a session which delegated and recorded no reading Axiom could
	// place still says it delegated.
	for _, g := range grouped {
		out.Groups += len(g.launchers)
	}

	for id, s := range a.sessions {
		g := grouped[id]
		if g != nil {
			s.number(g)
			out.Paths = append(out.Paths, s.paths(id, g)...)
		}
		related, unrelated := s.countReads(g)
		out.RelatedReads += related
		out.UnrelatedReads += unrelated
	}

	slices.SortFunc(out.Paths, comparePaths)
	return out
}

// launched holds one session's delegations as the groups they make: each
// launching scope, in the order it first launched, with the scopes it launched
// directly.
type launched struct {
	launchers []string
	children  map[string][]string
}

// group arranges the established relations by session, keeping each launching
// scope and its directly launched scopes in the order they were established.
//
// Nothing is decided here about what a relation is: the pairs arrive already
// established, and this only sorts them into the groups the report is built
// from.
func group(relations []delegation.Relation) map[string]*launched {
	out := make(map[string]*launched)
	for _, r := range relations {
		g, ok := out[r.SessionID]
		if !ok {
			g = &launched{children: make(map[string][]string)}
			out[r.SessionID] = g
		}
		if _, seen := g.children[r.Launcher]; !seen {
			g.launchers = append(g.launchers, r.Launcher)
		}
		g.children[r.Launcher] = append(g.children[r.Launcher], r.AgentID)
	}
	return out
}

// number gives a scope named by a relation an ordinal where the log recorded
// no call by it, so that nothing this report prints is left unnumbered.
//
// It runs after every record has been added, so the numbers the log
// established keep the order it established them in, and these follow.
func (s *sessionState) number(g *launched) {
	for _, launcher := range g.launchers {
		if launcher != "" {
			s.note(launcher)
		}
		for _, c := range g.children[launcher] {
			s.note(c)
		}
	}
}

// countReads splits a session's reading into what a relation holds and what it
// does not. A session no relation names holds none of it.
func (s *sessionState) countReads(g *launched) (related, unrelated int) {
	var in map[string]struct{}
	if g != nil {
		in = make(map[string]struct{}, len(g.launchers))
		for _, launcher := range g.launchers {
			in[launcher] = struct{}{}
			for _, c := range g.children[launcher] {
				in[c] = struct{}{}
			}
		}
	}

	for scope, paths := range s.reads {
		reads := 0
		for _, n := range paths {
			reads += n
		}
		if _, ok := in[scope]; ok {
			related += reads
			continue
		}
		unrelated += reads
	}
	return related, unrelated
}

// paths reduces one session's groups to the paths more than one member of a
// group read.
func (s *sessionState) paths(session string, g *launched) []Path {
	groups := make(map[string][]Group)

	for _, launcher := range g.launchers {
		members := make([]string, 0, len(g.children[launcher])+1)
		members = append(members, launcher)
		members = append(members, g.children[launcher]...)

		byPath := make(map[string][]Scope)
		for _, m := range members {
			for path, reads := range s.reads[m] {
				byPath[path] = append(byPath[path], Scope{Ref: s.ref(m), Reads: reads})
			}
		}
		for path, scopes := range byPath {
			// One scope is one member however many times it read the
			// path. Repetition inside a scope is a different subject,
			// under rules this analysis does not apply.
			if len(scopes) < 2 {
				continue
			}
			slices.SortFunc(scopes, compareScopes)
			groups[path] = append(groups[path], Group{Launcher: s.ref(launcher), Scopes: scopes})
		}
	}

	out := make([]Path, 0, len(groups))
	for path, gs := range groups {
		slices.SortFunc(gs, compareGroups)
		out = append(out, Path{SessionID: session, Path: path, Groups: gs})
	}
	return out
}

// ref names a scope as the report names it.
func (s *sessionState) ref(agent string) ScopeRef {
	if agent == "" {
		return ScopeRef{Root: true}
	}
	return ScopeRef{Ordinal: s.ordinals[agent]}
}

// comparePaths puts the path related across the most groups first, and settles
// every tie on recorded strings so that two runs over one log cannot disagree.
//
// Ranking is by how much of the delegation structure the reading appeared in,
// which is the subject of the analysis. It is never a ranking of how much any
// of it mattered.
func comparePaths(a, b Path) int {
	if c := cmp.Compare(len(b.Groups), len(a.Groups)); c != 0 {
		return c
	}
	if c := cmp.Compare(scopesIn(b), scopesIn(a)); c != 0 {
		return c
	}
	if c := cmp.Compare(a.SessionID, b.SessionID); c != 0 {
		return c
	}
	return cmp.Compare(a.Path, b.Path)
}

// scopesIn counts the scope memberships a path's groups hold. A scope in two
// groups counts in both: the number orders the list and is never reported as a
// number of agents.
func scopesIn(p Path) int {
	n := 0
	for _, g := range p.Groups {
		n += len(g.Scopes)
	}
	return n
}

func compareGroups(a, b Group) int { return compareRefs(a.Launcher, b.Launcher) }

func compareScopes(a, b Scope) int { return compareRefs(a.Ref, b.Ref) }

// compareRefs puts the session scope first and orders nested scopes by their
// number, which is the order the log first recorded them working.
func compareRefs(a, b ScopeRef) int {
	if a.Root != b.Root {
		if a.Root {
			return -1
		}
		return 1
	}
	return cmp.Compare(a.Ordinal, b.Ordinal)
}
