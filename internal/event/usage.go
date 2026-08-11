package event

import "time"

// UsageKind identifies what a usage record measures.
type UsageKind string

const (
	// UsageModelRequest is one request to the model.
	UsageModelRequest UsageKind = "model_request"
	// UsageToolResult is one completed tool call, measured by the agent.
	UsageToolResult UsageKind = "tool_result"
)

// Usage is one measurement of what an agent consumed, reported by the agent
// itself rather than derived by Axiom.
//
// Usage records live in their own stream, separate from the behavioral events
// an agent's hooks produce. They are recorded only while a receiver is running,
// so a session with no usage records means Axiom was not listening, never that
// the session consumed nothing.
//
// InvocationID and TurnID are carried so that a later milestone can join a
// measurement to the behavior that caused it. Axiom does not perform that join
// today.
type Usage struct {
	SchemaVersion int       `json:"schema_version"`
	Agent         string    `json:"agent"`
	Kind          UsageKind `json:"kind"`
	Timestamp     time.Time `json:"timestamp"`

	SessionID string `json:"session_id"`
	// TurnID groups everything the agent did for one user prompt.
	TurnID string `json:"turn_id,omitempty"`
	// InvocationID identifies one tool call, matching the identifier the same
	// agent reports to its hooks.
	InvocationID string `json:"invocation_id,omitempty"`

	// ToolName is the tool the agent reported. Agents redact some names, so
	// this is what telemetry called the tool and not necessarily what a hook
	// would have called it.
	ToolName string `json:"tool_name,omitempty"`
	// Model is the model that served a request.
	Model string `json:"model,omitempty"`
	// Source is the agent's own description of what issued the request, which
	// distinguishes the main conversation from housekeeping such as
	// compaction. Values are agent-defined.
	Source string `json:"source,omitempty"`

	// Tokens is nil when the agent reported no token counts.
	Tokens *Tokens `json:"tokens,omitempty"`
	// CostMicros is the agent's own cost estimate in millionths of a US
	// dollar. It is an estimate made by the agent, not a billing figure, and
	// nil when it was not reported.
	CostMicros *int64 `json:"cost_micros,omitempty"`
	// DurationMS is how long the measured operation took.
	DurationMS *int64 `json:"duration_ms,omitempty"`
	// ResultBytes is the size of a tool's result, which is the closest
	// observable measure of how much a tool call added to the conversation.
	ResultBytes *int64 `json:"result_bytes,omitempty"`
}

// Version reports the schema this record was written under.
func (u Usage) Version() int { return u.SchemaVersion }

// Tokens counts the tokens one model request consumed, as the agent reported
// them. A missing count is zero only because the agent said so.
type Tokens struct {
	Input         int64 `json:"input"`
	Output        int64 `json:"output"`
	CacheRead     int64 `json:"cache_read"`
	CacheCreation int64 `json:"cache_creation"`
}
