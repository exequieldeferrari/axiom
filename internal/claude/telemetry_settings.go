package claude

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Environment variables Axiom sets to send Claude Code's telemetry to a local
// receiver. Only the logs signal is configured, and only through its
// per-signal variables: the generic OTEL_EXPORTER_OTLP_* variables apply to
// every signal, so setting those would silently redirect a user's metrics too.
const (
	envEnableTelemetry = "CLAUDE_CODE_ENABLE_TELEMETRY"
	envLogsExporter    = "OTEL_LOGS_EXPORTER"
	envLogsProtocol    = "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL"
	envLogsEndpoint    = "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"

	envGenericEndpoint = "OTEL_EXPORTER_OTLP_ENDPOINT"
	envGenericProtocol = "OTEL_EXPORTER_OTLP_PROTOCOL"
)

// ContentFlags are the Claude Code settings that would export prompts,
// responses, tool arguments, tool output, or whole API bodies. Axiom never
// writes them. The list exists so that the intent is enforced by a test rather
// than remembered.
var ContentFlags = []string{
	"OTEL_LOG_USER_PROMPTS",
	"OTEL_LOG_ASSISTANT_RESPONSES",
	"OTEL_LOG_TOOL_DETAILS",
	"OTEL_LOG_TOOL_CONTENT",
	"OTEL_LOG_RAW_API_BODIES",
}

// TelemetryConflictError reports existing telemetry configuration that Axiom
// will not take over. Redirecting a signal someone else configured could send
// a team's usage data to a local file instead of the collector they chose.
type TelemetryConflictError struct {
	Key      string
	Existing string
	Want     string
}

func (e *TelemetryConflictError) Error() string {
	if e.Want == "" {
		return fmt.Sprintf(
			"%s is already set to %q, and it applies to every signal; axiom will not redirect telemetry it did not configure",
			e.Key, e.Existing)
	}
	return fmt.Sprintf("%s is already set to %q, not %q", e.Key, e.Existing, e.Want)
}

// InstallTelemetry adds the telemetry configuration to a Claude Code settings
// document, leaving every other environment variable untouched.
func InstallTelemetry(data []byte, endpoint string) (Result, error) {
	doc, err := decodeDocument(data)
	if err != nil {
		return Result{}, err
	}
	env, err := envObject(doc)
	if err != nil {
		return Result{}, err
	}

	// A generic endpoint or protocol proves an existing pipeline. Per-signal
	// values do take precedence over it, but layering onto a configuration
	// Axiom does not understand is exactly the case to refuse rather than
	// guess at.
	for _, key := range []string{envGenericEndpoint, envGenericProtocol} {
		if existing, ok := envValue(env, key); ok {
			return Result{}, &TelemetryConflictError{Key: key, Existing: existing}
		}
	}

	want := map[string]string{
		envEnableTelemetry: "1",
		envLogsExporter:    "otlp",
		envLogsProtocol:    "http/json",
		envLogsEndpoint:    endpoint,
	}
	// Decide before mutating, so a conflict on one key cannot leave the others
	// half-written.
	for _, key := range sortedKeys(want) {
		existing, ok := envValue(env, key)
		if ok && existing != want[key] {
			return Result{}, &TelemetryConflictError{Key: key, Existing: existing, Want: want[key]}
		}
	}

	changed := false
	for _, key := range sortedKeys(want) {
		if existing, ok := envValue(env, key); ok && existing == want[key] {
			continue
		}
		env[key] = want[key]
		changed = true
	}
	doc["env"] = env

	content, err := marshalDocument(doc)
	if err != nil {
		return Result{}, err
	}
	return Result{Changed: changed, Content: content}, nil
}

// TelemetryEndpoint reports the URL an exporter should post log records to.
func TelemetryEndpoint(addr string) string {
	return "http://" + addr + "/v1/logs"
}

func envObject(doc map[string]any) (map[string]any, error) {
	switch v := doc["env"].(type) {
	case nil:
		return map[string]any{}, nil
	case map[string]any:
		return v, nil
	default:
		return nil, errors.New(`existing "env" value is not an object, refusing to modify it`)
	}
}

// envValue reports a variable's value. A non-string value is reported as a
// conflict rather than overwritten, because Claude Code's own handling of it
// is not something Axiom should assume.
func envValue(env map[string]any, key string) (string, bool) {
	v, ok := env[key]
	if !ok || v == nil {
		return "", false
	}
	if s, ok := v.(string); ok {
		return s, true
	}
	return fmt.Sprintf("%v", v), true
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TelemetrySummary describes the configuration for a human, in the order the
// variables matter.
func TelemetrySummary(endpoint string) string {
	var b strings.Builder
	for _, kv := range [][2]string{
		{envEnableTelemetry, "1"},
		{envLogsExporter, "otlp"},
		{envLogsProtocol, "http/json"},
		{envLogsEndpoint, endpoint},
	} {
		fmt.Fprintf(&b, "  %s=%s\n", kv[0], kv[1])
	}
	return b.String()
}
