package claude_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/exequieldeferrari/axiom/internal/claude"
)

const endpoint = "http://127.0.0.1:4318/v1/logs"

func envOf(t *testing.T, content []byte) map[string]any {
	t.Helper()

	var doc struct {
		Env map[string]any `json:"env"`
	}
	if err := json.Unmarshal(content, &doc); err != nil {
		t.Fatalf("settings are not valid JSON: %v\n%s", err, content)
	}
	return doc.Env
}

func installTelemetry(t *testing.T, data []byte) claude.Result {
	t.Helper()

	res, err := claude.InstallTelemetry(data, endpoint)
	if err != nil {
		t.Fatalf("InstallTelemetry: %v", err)
	}
	return res
}

func TestTelemetryInstallsPerSignalVariables(t *testing.T) {
	t.Parallel()

	res := installTelemetry(t, nil)
	if !res.Changed {
		t.Error("Changed = false on a fresh install")
	}

	want := map[string]any{
		"CLAUDE_CODE_ENABLE_TELEMETRY":     "1",
		"OTEL_LOGS_EXPORTER":               "otlp",
		"OTEL_EXPORTER_OTLP_LOGS_PROTOCOL": "http/json",
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": endpoint,
	}
	env := envOf(t, res.Content)
	for key, value := range want {
		if env[key] != value {
			t.Errorf("%s = %v, want %v", key, env[key], value)
		}
	}
	// The generic variables apply to every signal, so setting them would
	// redirect metrics and traces Axiom does not receive.
	for _, key := range []string{"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_PROTOCOL"} {
		if _, ok := env[key]; ok {
			t.Errorf("%s was set, want only per-signal variables", key)
		}
	}
	if len(env) != len(want) {
		t.Errorf("env has %d variables, want %d: %v", len(env), len(want), env)
	}
}

// Enabling metrics would start a second exporter that nothing is listening to.
func TestTelemetryDoesNotEnableOtherSignals(t *testing.T) {
	t.Parallel()

	env := envOf(t, installTelemetry(t, nil).Content)
	for _, key := range []string{"OTEL_METRICS_EXPORTER", "OTEL_TRACES_EXPORTER"} {
		if _, ok := env[key]; ok {
			t.Errorf("%s was set", key)
		}
	}
}

// Content logging is opt-in and Axiom never opts in.
func TestTelemetryNeverEnablesContentLogging(t *testing.T) {
	t.Parallel()

	content := installTelemetry(t, nil).Content
	for _, flag := range claude.ContentFlags {
		if strings.Contains(string(content), flag) {
			t.Errorf("settings mention %s:\n%s", flag, content)
		}
	}
}

func TestTelemetryIsIdempotent(t *testing.T) {
	t.Parallel()

	first := installTelemetry(t, nil)
	second := installTelemetry(t, first.Content)

	if second.Changed {
		t.Error("Changed = true on reinstall")
	}
	if string(first.Content) != string(second.Content) {
		t.Errorf("reinstall rewrote the settings:\n%s\n%s", first.Content, second.Content)
	}
}

func TestTelemetryPreservesUnrelatedSettings(t *testing.T) {
	t.Parallel()

	existing := []byte(`{
  "model": "opus",
  "env": {"EDITOR": "vim", "HTTPS_PROXY": "http://proxy.internal:8080"},
  "permissions": {"allow": ["Bash(git:*)"]}
}`)

	res := installTelemetry(t, existing)
	var doc map[string]any
	if err := json.Unmarshal(res.Content, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if doc["model"] != "opus" {
		t.Errorf("model = %v", doc["model"])
	}
	if _, ok := doc["permissions"]; !ok {
		t.Error("permissions were dropped")
	}
	env := envOf(t, res.Content)
	if env["EDITOR"] != "vim" || env["HTTPS_PROXY"] != "http://proxy.internal:8080" {
		t.Errorf("unrelated environment variables changed: %v", env)
	}
}

// Someone else's collector must not be taken over.
func TestTelemetryRefusesConflicts(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"a generic endpoint":  `{"env":{"OTEL_EXPORTER_OTLP_ENDPOINT":"https://collector.corp:4318"}}`,
		"a generic protocol":  `{"env":{"OTEL_EXPORTER_OTLP_PROTOCOL":"grpc"}}`,
		"another logs target": `{"env":{"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT":"https://collector.corp:4318/v1/logs"}}`,
		"another exporter":    `{"env":{"OTEL_LOGS_EXPORTER":"console"}}`,
		"another protocol":    `{"env":{"OTEL_EXPORTER_OTLP_LOGS_PROTOCOL":"grpc"}}`,
		"telemetry disabled":  `{"env":{"CLAUDE_CODE_ENABLE_TELEMETRY":"0"}}`,
		"a non-string value":  `{"env":{"CLAUDE_CODE_ENABLE_TELEMETRY":true}}`,
	}

	for name, settings := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			res, err := claude.InstallTelemetry([]byte(settings), endpoint)
			var conflict *claude.TelemetryConflictError
			if !errors.As(err, &conflict) {
				t.Fatalf("err = %v, want a conflict", err)
			}
			if res.Changed || res.Content != nil {
				t.Error("a refused install produced content")
			}
			if !strings.Contains(conflict.Error(), conflict.Key) {
				t.Errorf("the error does not name the variable: %v", conflict)
			}
		})
	}
}

// A conflict on one variable must not leave the others written.
func TestARefusedInstallWritesNothing(t *testing.T) {
	t.Parallel()

	settings := []byte(`{"env":{"OTEL_LOGS_EXPORTER":"console"}}`)
	if _, err := claude.InstallTelemetry(settings, endpoint); err == nil {
		t.Fatal("install succeeded, want a conflict")
	}

	// The caller keeps its original bytes, so nothing partial can be written.
	env := envOf(t, settings)
	if len(env) != 1 || env["OTEL_LOGS_EXPORTER"] != "console" {
		t.Errorf("the input document was modified: %v", env)
	}
}

// An unchanged endpoint that Axiom itself wrote is not a conflict.
func TestReinstallingTheSameEndpointIsNotAConflict(t *testing.T) {
	t.Parallel()

	settings := []byte(`{"env":{"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT":"` + endpoint + `"}}`)
	res := installTelemetry(t, settings)

	if !res.Changed {
		t.Error("Changed = false, want the remaining variables added")
	}
	if envOf(t, res.Content)["OTEL_LOGS_EXPORTER"] != "otlp" {
		t.Error("the remaining variables were not added")
	}
}

func TestTelemetryRefusesAnUnreadableDocument(t *testing.T) {
	t.Parallel()

	for name, settings := range map[string]string{
		"invalid JSON":         `{"env":`,
		"env is not an object": `{"env":"none"}`,
	} {
		if _, err := claude.InstallTelemetry([]byte(settings), endpoint); err == nil {
			t.Errorf("%s: install succeeded", name)
		}
	}
}

func TestTelemetryEndpointAddsTheSignalPath(t *testing.T) {
	t.Parallel()

	if got := claude.TelemetryEndpoint("127.0.0.1:4318"); got != endpoint {
		t.Errorf("TelemetryEndpoint = %q, want %q", got, endpoint)
	}
}

func TestTelemetrySummaryListsWhatWasSet(t *testing.T) {
	t.Parallel()

	summary := claude.TelemetrySummary(endpoint)
	for _, want := range []string{"CLAUDE_CODE_ENABLE_TELEMETRY=1", "OTEL_LOGS_EXPORTER=otlp", "http/json", endpoint} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary omits %q:\n%s", want, summary)
		}
	}
}

// Hooks and telemetry land in one document, so a dry run shows what a real run
// would write.
func TestInstallFileWritesHooksAndTelemetryTogether(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := dir + "/settings.json"
	opts := claude.InstallOptions{ExePath: "/usr/local/bin/axiom", TelemetryEndpoint: endpoint}

	res, err := claude.InstallFile(path, opts)
	if err != nil {
		t.Fatalf("InstallFile: %v", err)
	}
	if !res.Changed {
		t.Error("Changed = false")
	}

	var doc map[string]any
	if err := json.Unmarshal(res.Content, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := doc["hooks"]; !ok {
		t.Error("hooks were not installed")
	}
	if envOf(t, res.Content)["OTEL_LOGS_EXPORTER"] != "otlp" {
		t.Error("telemetry was not installed")
	}

	// Both halves are idempotent together.
	again, err := claude.InstallFile(path, opts)
	if err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	if again.Changed {
		t.Error("Changed = true on reinstall")
	}
}

// Installing hooks alone must not enable telemetry, which changes where a
// user's data goes.
func TestInstallFileLeavesTelemetryAloneByDefault(t *testing.T) {
	t.Parallel()

	res, err := claude.InstallFile(t.TempDir()+"/settings.json", claude.InstallOptions{ExePath: "/usr/local/bin/axiom"})
	if err != nil {
		t.Fatalf("InstallFile: %v", err)
	}
	if strings.Contains(string(res.Content), "OTEL") {
		t.Errorf("hook install touched telemetry:\n%s", res.Content)
	}
}
