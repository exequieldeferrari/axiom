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

// failure records what the agent said about a call that did not succeed. The
// error text itself is read here and kept nowhere: what survives it is a
// digest, the exit status it declared, and how the report was classified.
func failure(p *payload) *event.Failure {
	f := &event.Failure{Kind: event.FailureKindError}
	if p.IsInterrupt {
		f.Kind = event.FailureKindInterrupt
	}
	if p.Error == "" {
		f.Reporting = event.ReportingNoText
		return f
	}

	f.Digest = digest.Error(p.Error)
	status, ok := recognizeStatus(p.Error)
	if !ok {
		f.Reporting = event.ReportingUnrecognized
		return f
	}
	code := status.code
	f.ExitCode = &code
	f.Reporting = status.reporting()
	return f
}

// reportedStatus is the exit status a failure report opened with and whatever
// the agent reported after it.
type reportedStatus struct {
	code int
	// rest is everything past the status line, and separated says whether
	// there was anything past it to begin with. The two are held apart
	// because a report that ended at the status and one that carried on into
	// nothing are different observations.
	rest      string
	separated bool
}

// recognizeStatus reads the leading "Exit code N" line that Claude Code puts
// in front of shell failures. The rest of the error string is documented as
// display text with no stable format, so nothing else is parsed from it.
//
// This is the only place the shape of a failure report is recognized. The exit
// status and the reporting classification are both taken from one reading, so
// the two can never drift into disagreeing about what was recognized.
func recognizeStatus(errText string) (reportedStatus, bool) {
	line, rest, separated := strings.Cut(errText, "\n")

	const prefix = "Exit code "
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, prefix) {
		return reportedStatus{}, false
	}

	code, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, prefix)))
	if err != nil {
		return reportedStatus{}, false
	}
	return reportedStatus{code: code, rest: rest, separated: separated}, true
}

// reporting classifies what the report carried beyond the status it opened
// with.
//
// A report that continued past the status into whitespace alone is a shape no
// capture produced: Claude Code was observed trimming its output before
// reporting it. Axiom will not decide between the two readings that shape
// invites, so it declines to classify it. Sparse is never taken for empty.
func (s reportedStatus) reporting() event.Reporting {
	switch {
	case !s.separated:
		return event.ReportingStatusOnly
	case strings.TrimSpace(s.rest) == "":
		return event.ReportingUnrecognized
	default:
		return event.ReportingDetail
	}
}
