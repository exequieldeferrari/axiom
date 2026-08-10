package claude

import (
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/exequieldeferrari/axiom/internal/digest"
	"github.com/exequieldeferrari/axiom/internal/event"
)

// Ingest decodes one Claude Code hook payload and converts it to a canonical
// event, stamping it with now.
//
// A nil event and a nil error mean the payload was well formed but carries
// nothing Axiom records, such as a hook event outside this milestone.
func Ingest(r io.Reader, now time.Time) (*event.Event, error) {
	p, err := decode(r)
	if err != nil {
		return nil, err
	}

	switch p.HookEventName {
	case eventSessionStart, eventSessionEnd, eventPostToolUse, eventPostToolUseFailed:
	default:
		return nil, nil
	}

	if p.SessionID == "" {
		return nil, ErrMissingSessionID
	}

	ev := event.Event{
		SchemaVersion: event.SchemaVersion,
		Agent:         AgentName,
		Timestamp:     now,
		SessionID:     p.SessionID,
		TurnID:        p.PromptID,
		SubagentID:    p.AgentID,
		Cwd:           p.Cwd,
	}

	switch p.HookEventName {
	case eventSessionStart:
		ev.Type = event.TypeSessionStart
		ev.Session = &event.Session{Source: p.Source, Model: p.Model}
	case eventSessionEnd:
		ev.Type = event.TypeSessionEnd
		ev.Session = &event.Session{Reason: p.Reason}
	case eventPostToolUse:
		if p.ToolName == "" {
			return nil, ErrMissingToolName
		}
		ev.Type = event.TypeToolCall
		ev.Tool = toolCall(p, event.OutcomeSuccess)
	case eventPostToolUseFailed:
		if p.ToolName == "" {
			return nil, ErrMissingToolName
		}
		ev.Type = event.TypeToolCall
		ev.Tool = toolCall(p, event.OutcomeFailure)
		ev.Tool.Failure = failure(p)
	}

	return &ev, nil
}

func toolCall(p *payload, outcome event.Outcome) *event.ToolCall {
	return &event.ToolCall{
		Name:         p.ToolName,
		InvocationID: p.ToolUseID,
		Outcome:      outcome,
		DurationMS:   p.DurationMS,
		Metadata:     extractMetadata(p.ToolName, p.ToolInput),
	}
}

func failure(p *payload) *event.Failure {
	f := &event.Failure{Kind: event.FailureKindError}
	if p.IsInterrupt {
		f.Kind = event.FailureKindInterrupt
	}
	if p.Error != "" {
		f.Digest = digest.Error(p.Error)
		if code, ok := parseExitCode(p.Error); ok {
			f.ExitCode = &code
		}
	}
	return f
}

// parseExitCode reads the leading "Exit code N" line that Claude Code puts in
// front of shell failures. The rest of the error string is documented as
// display text with no stable format, so nothing else is parsed from it.
func parseExitCode(errText string) (int, bool) {
	line := errText
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}

	const prefix = "Exit code "
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, prefix) {
		return 0, false
	}

	code, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, prefix)))
	if err != nil {
		return 0, false
	}
	return code, true
}
