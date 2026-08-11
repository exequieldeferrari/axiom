package correlate_test

import (
	"testing"

	"github.com/exequieldeferrari/axiom/internal/correlate"
	"github.com/exequieldeferrari/axiom/internal/event"
)

func key(session, turn, invocation string) correlate.Key {
	return correlate.Key{SessionID: session, TurnID: turn, InvocationID: invocation}
}

func TestResultBytesReportsWhatOneCallReturned(t *testing.T) {
	t.Parallel()

	ix := correlate.NewIndex()
	ix.Add(toolResult("session-1", "turn-1", "call-1", bytesOf(7696)))

	got, ok := ix.ResultBytes(key("session-1", "turn-1", "call-1"))
	if !ok || got != 7696 {
		t.Errorf("ResultBytes = %d, %v; want 7696, true", got, ok)
	}
}

// The join is on all three identifiers, because an invocation identifier is
// only known to be unique within the turn that produced it.
func TestResultBytesNeedsTheWholeIdentity(t *testing.T) {
	t.Parallel()

	ix := correlate.NewIndex()
	ix.Add(toolResult("session-1", "turn-1", "call-1", bytesOf(7696)))

	for name, k := range map[string]correlate.Key{
		"another session":    key("session-2", "turn-1", "call-1"),
		"another turn":       key("session-1", "turn-2", "call-1"),
		"another invocation": key("session-1", "turn-1", "call-2"),
		"nothing at all":     {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got, ok := ix.ResultBytes(k); ok {
				t.Errorf("ResultBytes = %d, true; want unknown", got)
			}
		})
	}
}

// An invocation measured twice is ambiguous rather than resolved: the records
// may describe one call twice or two different calls.
func TestResultBytesWithholdsWhatItCannotResolve(t *testing.T) {
	t.Parallel()

	cases := map[string][]event.Usage{
		"never measured": nil,
		"measured twice": {
			toolResult("session-1", "turn-1", "call-1", bytesOf(7696)),
			toolResult("session-1", "turn-1", "call-1", bytesOf(93)),
		},
		"measured without a size": {
			toolResult("session-1", "turn-1", "call-1", nil),
		},
	}

	for name, records := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ix := correlate.NewIndex()
			for _, u := range records {
				ix.Add(u)
			}

			if got, ok := ix.ResultBytes(key("session-1", "turn-1", "call-1")); ok || got != 0 {
				t.Errorf("ResultBytes = %d, %v; want 0, false", got, ok)
			}
		})
	}
}
