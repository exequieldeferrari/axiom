package claude

import (
	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/otlp"
)

// Claude Code event names Axiom understands. Everything else it sends, from
// prompts to plugin inventories, is dropped: an event is mapped only when it
// measures what the agent consumed.
const (
	otelAPIRequest = "api_request"
	otelToolResult = "tool_result"
)

// Attribute names Axiom reads. This list is the privacy boundary. Claude Code
// attaches the user's email address, account and organization identifiers,
// terminal and machine detail to every single record, and content-bearing
// attributes appear as soon as anyone sets one of the OTEL_LOG_* flags. Nothing
// outside this list is read, so nothing outside it can be persisted.
const (
	attrSessionID   = "session.id"
	attrPromptID    = "prompt.id"
	attrToolUseID   = "tool_use_id"
	attrToolName    = "tool_name"
	attrModel       = "model"
	attrQuerySource = "query_source"

	attrInputTokens         = "input_tokens"
	attrOutputTokens        = "output_tokens"
	attrCacheReadTokens     = "cache_read_tokens"
	attrCacheCreationTokens = "cache_creation_tokens"
	attrCostMicros          = "cost_usd_micros"
	attrDurationMS          = "duration_ms"
	attrResultSizeBytes     = "tool_result_size_bytes"
)

// Usage converts one Claude Code telemetry record into a canonical usage
// record, reporting false for anything Axiom does not measure.
//
// A record without a session cannot be attributed to anything, so it is
// dropped rather than stored under an empty identifier.
func Usage(rec otlp.Record) (event.Usage, bool) {
	session := rec.Attrs.String(attrSessionID)
	if session == "" {
		return event.Usage{}, false
	}

	u := event.Usage{
		SchemaVersion: event.SchemaVersion,
		Agent:         AgentName,
		Timestamp:     rec.Time,
		SessionID:     session,
		TurnID:        rec.Attrs.String(attrPromptID),
		DurationMS:    optionalInt(rec.Attrs, attrDurationMS),
	}

	switch rec.Name {
	case otelAPIRequest:
		u.Kind = event.UsageModelRequest
		u.Model = rec.Attrs.String(attrModel)
		u.Source = rec.Attrs.String(attrQuerySource)
		u.Tokens = tokens(rec.Attrs)
		u.CostMicros = optionalInt(rec.Attrs, attrCostMicros)
	case otelToolResult:
		u.Kind = event.UsageToolResult
		u.InvocationID = rec.Attrs.String(attrToolUseID)
		u.ToolName = rec.Attrs.String(attrToolName)
		u.ResultBytes = optionalInt(rec.Attrs, attrResultSizeBytes)
	default:
		return event.Usage{}, false
	}

	if u.Timestamp.IsZero() {
		return event.Usage{}, false
	}
	return u, true
}

// tokens collects the counts of one model request, or nil when the agent
// reported none of them. A partial report is kept as-is: the agent omits a
// category it did not use, and inventing zeros would be indistinguishable from
// measuring them.
func tokens(a otlp.Attrs) *event.Tokens {
	in, okIn := a.Int(attrInputTokens)
	out, okOut := a.Int(attrOutputTokens)
	read, okRead := a.Int(attrCacheReadTokens)
	created, okCreated := a.Int(attrCacheCreationTokens)
	if !okIn && !okOut && !okRead && !okCreated {
		return nil
	}
	return &event.Tokens{Input: in, Output: out, CacheRead: read, CacheCreation: created}
}

func optionalInt(a otlp.Attrs, key string) *int64 {
	v, ok := a.Int(key)
	if !ok {
		return nil
	}
	return &v
}
