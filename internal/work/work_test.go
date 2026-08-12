package work_test

import (
	"testing"

	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/work"
)

func call(m *event.ToolMetadata, outcome event.Outcome) *event.ToolCall {
	return &event.ToolCall{Name: "Tool", Outcome: outcome, Metadata: m}
}

func file(access string, ranged bool) *event.ToolMetadata {
	op := &event.FileOp{Path: "/repo/a.go", Access: access}
	if ranged {
		ten := 10
		op.Offset = &ten
	}
	return &event.ToolMetadata{File: op}
}

// Every shape the evidence model distinguishes, and the one answer this
// package gives for it. The expectations are written out rather than derived,
// so a change to the classifier has to be made here too.
func TestOf(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		metadata *event.ToolMetadata
		want     work.Shape
	}{
		"no metadata":              {nil, work.Uninterpreted},
		"whole-file read":          {file(event.AccessRead, false), work.WholeRead},
		"ranged read":              {file(event.AccessRead, true), work.RangedRead},
		"write":                    {file(event.AccessWrite, false), work.Write},
		"edit":                     {file(event.AccessEdit, false), work.Edit},
		"file operation, no path":  {&event.ToolMetadata{File: &event.FileOp{Access: event.AccessRead}}, work.Uninterpreted},
		"file access not known":    {&event.ToolMetadata{File: &event.FileOp{Path: "/repo/a.go", Access: "chmod"}}, work.Uninterpreted},
		"shell":                    {&event.ToolMetadata{Shell: &event.ShellOp{CommandDigest: "d"}}, work.Shell},
		"background shell":         {&event.ToolMetadata{Shell: &event.ShellOp{CommandDigest: "d", Background: true}}, work.Shell},
		"search":                   {&event.ToolMetadata{Search: &event.SearchOp{Kind: event.SearchContent, PatternDigest: "p"}}, work.Search},
		"subagent launch":          {&event.ToolMetadata{Subagent: &event.SubagentOp{Type: "general-purpose"}}, work.SubagentLaunch},
		"metadata naming no shape": {&event.ToolMetadata{}, work.Uninterpreted},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := work.Of(call(c.metadata, event.OutcomeSuccess)); got != c.want {
				t.Errorf("Of(%s) = %v, want %v", name, got, c.want)
			}
		})
	}
}

// A launch is recognized from the metadata the adapter derived and never from
// the tool's name, which has already changed once.
func TestLaunchIsRecognizedFromMetadataNotName(t *testing.T) {
	t.Parallel()

	named := &event.ToolCall{Name: "Task", Outcome: event.OutcomeSuccess}
	if got := work.Of(named); got != work.Uninterpreted {
		t.Errorf("Of(a Task with no metadata) = %v, want %v", got, work.Uninterpreted)
	}

	unnamed := call(&event.ToolMetadata{Subagent: &event.SubagentOp{Type: "x"}}, event.OutcomeSuccess)
	unnamed.Name = "SomethingElse"
	if got := work.Of(unnamed); got != work.SubagentLaunch {
		t.Errorf("Of(launch metadata under another name) = %v, want %v", got, work.SubagentLaunch)
	}
}

// Every call counted lands in exactly one category, so a composition can be
// reconciled against the calls it was built from.
func TestCompositionReconciles(t *testing.T) {
	t.Parallel()

	calls := []*event.ToolCall{
		call(file(event.AccessRead, false), event.OutcomeSuccess),
		call(file(event.AccessRead, true), event.OutcomeSuccess),
		call(file(event.AccessWrite, false), event.OutcomeFailure),
		call(file(event.AccessEdit, false), event.Outcome("")),
		call(&event.ToolMetadata{Shell: &event.ShellOp{CommandDigest: "d"}}, event.OutcomeSuccess),
		call(&event.ToolMetadata{Search: &event.SearchOp{Kind: event.SearchFilename}}, event.OutcomeSuccess),
		call(&event.ToolMetadata{Subagent: &event.SubagentOp{Type: "x"}}, event.OutcomeSuccess),
		call(nil, event.OutcomeSuccess),
	}

	var c work.Composition
	for _, t := range calls {
		c.Add(t)
	}

	if got := c.Total(); got != len(calls) {
		t.Errorf("the composition accounts for %d of %d calls: %+v", got, len(calls), c)
	}
	// The three outcome states stay apart: neither a failure nor an
	// unestablished outcome may be absorbed into the other.
	if c.Writes != (work.Outcomes{Failed: 1}) {
		t.Errorf("writes = %+v, want one failure", c.Writes)
	}
	if c.Edits != (work.Outcomes{Unestablished: 1}) {
		t.Errorf("edits = %+v, want one with no outcome recorded", c.Edits)
	}
	if c.Launches != (work.Outcomes{Succeeded: 1}) {
		t.Errorf("launches = %+v, want one that succeeded", c.Launches)
	}
}

func TestOutcomesTotal(t *testing.T) {
	t.Parallel()

	o := work.Outcomes{Succeeded: 2, Failed: 3, Unestablished: 4}
	if got := o.Total(); got != 9 {
		t.Errorf("Total() = %d, want 9", got)
	}
	if got := (work.Outcomes{}).Total(); got != 0 {
		t.Errorf("an empty Total() = %d, want 0", got)
	}
}
