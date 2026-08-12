// Package claude adapts Claude Code hook payloads into canonical Axiom events
// and manages Axiom's entries in Claude Code settings files.
//
// The adapter reads only documented hook contracts. Claude Code's internal
// transcript format is deliberately not a dependency.
package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// AgentName identifies Claude Code in canonical events.
const AgentName = "claude-code"

// MaxPayloadBytes bounds how much of a hook payload Axiom will read. Payloads
// embed tool input, so a large Write carries a whole file; the cap keeps a hook
// process from growing without limit on hostile or unusual input. Anything
// larger is dropped rather than truncated, because a truncated payload cannot
// be decoded into a trustworthy event.
const MaxPayloadBytes = 4 << 20

// Hook events Axiom observes. PreToolUse is intentionally absent: Axiom is a
// passive observer and must not sit in front of a tool call.
const (
	eventSessionStart      = "SessionStart"
	eventSessionEnd        = "SessionEnd"
	eventPostToolUse       = "PostToolUse"
	eventPostToolUseFailed = "PostToolUseFailure"
)

var (
	// ErrPayloadTooLarge reports a payload above MaxPayloadBytes.
	ErrPayloadTooLarge = errors.New("hook payload exceeds the maximum size")
	// ErrMissingSessionID reports a payload that cannot be correlated.
	ErrMissingSessionID = errors.New("hook payload has no session_id")
	// ErrMissingToolName reports a tool event with no tool.
	ErrMissingToolName = errors.New("tool event has no tool_name")
)

// payload mirrors the documented hook input fields Axiom uses. Fields absent
// from a given event simply stay zero.
type payload struct {
	HookEventName string `json:"hook_event_name"`
	SessionID     string `json:"session_id"`
	PromptID      string `json:"prompt_id"`
	AgentID       string `json:"agent_id"`
	Cwd           string `json:"cwd"`

	Source string `json:"source"`
	Model  string `json:"model"`
	Reason string `json:"reason"`

	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
	ToolUseID string          `json:"tool_use_id"`
	// ToolResponse is what the tool returned. It is held as raw bytes and
	// read by exactly one allowlisted extraction, which takes a single
	// opaque identifier out of a launch's response and nothing else. It is
	// never decoded as a whole and never reaches an event.
	ToolResponse json.RawMessage `json:"tool_response"`
	DurationMS   *int64          `json:"duration_ms"`
	Error        string          `json:"error"`
	IsInterrupt  bool            `json:"is_interrupt"`
}

// decode reads one hook payload under a hard size limit.
func decode(r io.Reader) (*payload, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxPayloadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read hook payload: %w", err)
	}
	if len(data) > MaxPayloadBytes {
		return nil, ErrPayloadTooLarge
	}

	var p payload
	if err := json.Unmarshal(data, &p); err != nil {
		// The payload is never included: it may contain source code or secrets.
		return nil, errors.New("hook payload is not valid JSON")
	}
	return &p, nil
}
